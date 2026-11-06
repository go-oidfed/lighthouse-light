package backends

import (
	"encoding/json"
	"fmt"
	"sync"

	"gorm.io/datatypes"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// KVStore implements model.KeyValueStore fully in memory: there is no kv.json
// file. Config-owned values (signing alg, lifetimes, entity metadata, general
// policies/constraints/claims) are seeded at boot via applyConfigValues, and
// any runtime writes are lost on restart (no light caller writes KV at
// runtime; the config file is the single source of truth for these values).
type KVStore struct {
	mu    sync.RWMutex
	byKey map[[2]string]datatypes.JSON
}

func newKVStore() *KVStore {
	return &KVStore{byKey: make(map[[2]string]datatypes.JSON)}
}

// Get retrieves the value for (scope, key). Returns (nil, nil) if not found.
func (s *KVStore) Get(scope, key string) (datatypes.JSON, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.byKey[[2]string{scope, key}]; ok {
		return v, nil
	}
	return nil, nil
}

// GetAs retrieves and unmarshals the value into out. Returns (false, nil) if
// not found.
func (s *KVStore) GetAs(scope, key string, out any) (bool, error) {
	raw, err := s.Get(scope, key)
	if err != nil {
		return false, err
	}
	if raw == nil {
		return false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("kv %s/%s: failed to unmarshal value: %w", scope, key, err)
	}
	return true, nil
}

// Set stores/replaces the value for a (scope, key).
func (s *KVStore) Set(scope, key string, value datatypes.JSON) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[[2]string{scope, key}] = value
	return nil
}

// SetAny marshals v to JSON and stores it at (scope, key).
func (s *KVStore) SetAny(scope, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("kv %s/%s: failed to marshal value: %w", scope, key, err)
	}
	return s.Set(scope, key, raw)
}

// Delete removes the entry for (scope, key). No error if missing.
func (s *KVStore) Delete(scope, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, [2]string{scope, key})
	return nil
}

// ensure KVStore implements model.KeyValueStore.
var _ model.KeyValueStore = (*KVStore)(nil)
