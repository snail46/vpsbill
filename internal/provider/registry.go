package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Factory builds a driver for one node. It must not perform network I/O;
// connectivity is verified by the caller through HostInfo.
type Factory func(Config) (Driver, error)

// Descriptor describes a provider type to the admin UI, which renders the
// node form from it.
type Descriptor struct {
	Type                string   `json:"type"`
	Name                string   `json:"name"`
	CredentialLabel     string   `json:"credential_label"`
	BaseURLHint         string   `json:"base_url_hint"`
	VirtualizationTypes []string `json:"virtualization_types"`
	// AgentManaged providers are reached through an agent that dials in, so
	// the node has no base URL of its own.
	AgentManaged bool          `json:"agent_managed"`
	Options      []OptionField `json:"options"`
}

// OptionField is one non-secret per-node setting stored in provider_options.
type OptionField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Kind        string `json:"kind"` // text, number, bool or list (comma separated)
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

type registration struct {
	descriptor Descriptor
	factory    Factory
}

var (
	registryMu sync.RWMutex
	registry   = map[string]registration{}
)

// ErrUnknownType is returned by Open for an unregistered provider type.
var ErrUnknownType = errors.New("unknown provider type")

// Register makes a provider type available. Adapters call it from init, and
// cmd/server imports each adapter package for its side effect.
func Register(descriptor Descriptor, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if descriptor.Type == "" || factory == nil {
		panic("provider: invalid registration")
	}
	if _, exists := registry[descriptor.Type]; exists {
		panic("provider: duplicate registration for " + descriptor.Type)
	}
	registry[descriptor.Type] = registration{descriptor: descriptor, factory: factory}
}

func Lookup(providerType string) (Descriptor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	entry, ok := registry[providerType]
	return entry.descriptor, ok
}

// Types lists registered providers in a stable order.
func Types() []Descriptor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	result := make([]Descriptor, 0, len(registry))
	for _, entry := range registry {
		result = append(result, entry.descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	return result
}

func Open(providerType string, config Config) (Driver, error) {
	registryMu.RLock()
	entry, ok := registry[providerType]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownType, providerType)
	}
	return entry.factory(config)
}

// SecretOpener decrypts a stored node credential (security.SecretBox).
type SecretOpener interface {
	Open(ciphertext []byte) (string, error)
}

// Sealed is a node endpoint as stored: the credential is still encrypted.
type Sealed struct {
	Type                 string
	BaseURL              string
	CredentialCiphertext []byte
	Options              json.RawMessage
}

// OpenSealed decrypts the node credential and opens a driver in one step, so
// the plaintext credential never leaves this call.
func OpenSealed(box SecretOpener, endpoint Sealed, timeout time.Duration) (Driver, error) {
	if _, ok := Lookup(endpoint.Type); !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownType, endpoint.Type)
	}
	credential, err := box.Open(endpoint.CredentialCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt node credential: %w", err)
	}
	return Open(endpoint.Type, Config{BaseURL: endpoint.BaseURL, Credential: credential, Options: endpoint.Options, Timeout: timeout})
}

// NormalizeOptions keeps only the descriptor's declared option keys, coerces
// each to its declared kind and enforces required fields, so adapters can
// decode provider_options without defensive parsing.
func NormalizeOptions(descriptor Descriptor, input map[string]any) (json.RawMessage, error) {
	result := make(map[string]any, len(descriptor.Options))
	for _, field := range descriptor.Options {
		value, present := input[field.Key]
		normalized, empty, err := normalizeOption(field.Kind, value, present)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.Label, err)
		}
		if empty {
			if field.Required {
				return nil, fmt.Errorf("%s不能为空", field.Label)
			}
			continue
		}
		result[field.Key] = normalized
	}
	return json.Marshal(result)
}

func normalizeOption(kind string, value any, present bool) (any, bool, error) {
	if !present || value == nil {
		return nil, true, nil
	}
	switch kind {
	case "number":
		switch number := value.(type) {
		case float64:
			return number, false, nil
		case string:
			if strings.TrimSpace(number) == "" {
				return nil, true, nil
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
			if err != nil {
				return nil, false, errors.New("必须是数字")
			}
			return parsed, false, nil
		}
		return nil, false, errors.New("必须是数字")
	case "bool":
		switch flag := value.(type) {
		case bool:
			return flag, false, nil
		case string:
			parsed, err := strconv.ParseBool(flag)
			if err != nil {
				return nil, false, errors.New("必须是布尔值")
			}
			return parsed, false, nil
		}
		return nil, false, errors.New("必须是布尔值")
	case "list":
		var items []string
		switch list := value.(type) {
		case string:
			items = strings.Split(list, ",")
		case []any:
			for _, item := range list {
				text, ok := item.(string)
				if !ok {
					return nil, false, errors.New("必须是文本列表")
				}
				items = append(items, text)
			}
		default:
			return nil, false, errors.New("必须是文本列表")
		}
		cleaned := make([]string, 0, len(items))
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				cleaned = append(cleaned, item)
			}
		}
		return cleaned, len(cleaned) == 0, nil
	default:
		text, ok := value.(string)
		if !ok {
			return nil, false, errors.New("必须是文本")
		}
		text = strings.TrimSpace(text)
		return text, text == "", nil
	}
}

// AgentEndpoint derives the stored base URL of an agent-managed node from its
// token. It identifies the node without revealing the token.
func AgentEndpoint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "agent://" + hex.EncodeToString(sum[:8])
}
