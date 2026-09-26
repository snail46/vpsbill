package provider

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Factory builds a driver for one node. It must not perform network I/O;
// connectivity is verified by the caller through HostInfo.
type Factory func(Config) (Driver, error)

// Descriptor describes a provider type to the admin UI.
type Descriptor struct {
	Type                string   `json:"type"`
	Name                string   `json:"name"`
	CredentialLabel     string   `json:"credential_label"`
	BaseURLHint         string   `json:"base_url_hint"`
	VirtualizationTypes []string `json:"virtualization_types"`
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

// OpenSealed decrypts the node credential and opens a driver in one step, so
// the plaintext credential never leaves this call.
func OpenSealed(box SecretOpener, providerType, baseURL string, credentialCiphertext []byte, timeout time.Duration) (Driver, error) {
	credential, err := box.Open(credentialCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt node credential: %w", err)
	}
	return Open(providerType, Config{BaseURL: baseURL, Credential: credential, Timeout: timeout})
}
