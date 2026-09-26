package postgres

import (
	"encoding/json"

	"vpsbill/internal/provider"
)

// NodeEndpoint is everything needed to open a provider driver for a node.
// Structs that act on a node embed it so every query selects the same columns.
type NodeEndpoint struct {
	ProviderType     string          `json:"provider_type"`
	BaseURL          string          `json:"base_url"`
	APIKeyCiphertext []byte          `json:"-"`
	ProviderOptions  json.RawMessage `json:"provider_options"`
}

func (e NodeEndpoint) Sealed() provider.Sealed {
	return provider.Sealed{Type: e.ProviderType, BaseURL: e.BaseURL, CredentialCiphertext: e.APIKeyCiphertext, Options: e.ProviderOptions}
}

// nullableJSON passes an empty document as SQL NULL so column defaults apply.
func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
