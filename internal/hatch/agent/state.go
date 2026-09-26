package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vpsbill/internal/hatch/protocol"
)

// InstanceRecord is what the agent remembers about an instance beyond what
// the runtime reports: the sold specification, its static address, NAT
// rules and the monthly traffic ledger.
type InstanceRecord struct {
	Name             string                 `json:"name"`
	Virtualization   string                 `json:"virtualization"`
	Template         string                 `json:"template"`
	VCPU             int                    `json:"vcpu"`
	RAMMB            int                    `json:"ram_mb"`
	DiskGB           int                    `json:"disk_gb"`
	NetworkDownMbps  int                    `json:"network_down_mbps"`
	NetworkUpMbps    int                    `json:"network_up_mbps"`
	MonthlyTrafficGB int                    `json:"monthly_traffic_gb"`
	PortMappingLimit int                    `json:"port_mapping_limit"`
	PrivateIPv4      string                 `json:"private_ipv4"`
	Mappings         []protocol.PortMapping `json:"mappings"`
	Traffic          TrafficRecord          `json:"traffic"`
	// PasswordSet is false until the root password of the current install
	// has been applied; ensure retries finish the job while it is false.
	PasswordSet bool      `json:"password_set"`
	CreatedAt   time.Time `json:"created_at"`
}

// TrafficRecord accumulates counter deltas so totals survive container
// restarts (which reset counters) and agent restarts.
type TrafficRecord struct {
	Month   string `json:"month"`
	RXBytes int64  `json:"rx_bytes"`
	TXBytes int64  `json:"tx_bytes"`
	LastRX  int64  `json:"last_rx"`
	LastTX  int64  `json:"last_tx"`
}

type stateFile struct {
	Instances map[string]*InstanceRecord `json:"instances"`
}

// Store is the agent's durable state, saved atomically after each change.
type Store struct {
	mu    sync.Mutex
	path  string
	state stateFile
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	store := &Store{path: filepath.Join(dir, "state.json"), state: stateFile{Instances: map[string]*InstanceRecord{}}}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &store.state); err != nil {
		return nil, err
	}
	if store.state.Instances == nil {
		store.state.Instances = map[string]*InstanceRecord{}
	}
	return store, nil
}

// Get returns a copy of the named record.
func (s *Store) Get(name string) (InstanceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.state.Instances[name]
	if !ok {
		return InstanceRecord{}, false
	}
	return cloneRecord(record), true
}

func (s *Store) List() []InstanceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]InstanceRecord, 0, len(s.state.Instances))
	for _, record := range s.state.Instances {
		result = append(result, cloneRecord(record))
	}
	return result
}

// Update applies fn to the named record under the lock and persists the
// result. fn receives nil when the record does not exist; returning nil
// deletes it.
func (s *Store) Update(name string, fn func(*InstanceRecord) (*InstanceRecord, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current *InstanceRecord
	if existing, ok := s.state.Instances[name]; ok {
		copied := cloneRecord(existing)
		current = &copied
	}
	next, err := fn(current)
	if err != nil {
		return err
	}
	previous, existed := s.state.Instances[name]
	if next == nil {
		delete(s.state.Instances, name)
	} else {
		s.state.Instances[name] = next
	}
	if err := s.saveLocked(); err != nil {
		if existed {
			s.state.Instances[name] = previous
		} else {
			delete(s.state.Instances, name)
		}
		return err
	}
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0o600)
}

func cloneRecord(record *InstanceRecord) InstanceRecord {
	copied := *record
	copied.Mappings = append([]protocol.PortMapping(nil), record.Mappings...)
	return copied
}

func (r InstanceRecord) PasswordPending() bool { return !r.PasswordSet }
