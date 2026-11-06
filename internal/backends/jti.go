package backends

import (
	"time"

	"github.com/go-oidfed/lighthouse/storage/model"
	"github.com/rs/zerolog/log"
)

// JTIStore implements model.JTIStorageBackend on a JSON file. Expired entries
// are dropped lazily on Exists/Cleanup/load. The file is a bare object of
// jti -> expiresAt (Unix seconds) pairs.
type JTIStore struct {
	*fileState
	rows map[string]int64
}

func newJTIStore(state *fileState) (*JTIStore, error) {
	s := &JTIStore{fileState: state}
	if err := rejectWrappedFormat(state.file, "jti", "entries"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	if s.rows == nil {
		s.rows = make(map[string]int64)
	}
	// Drop already-expired entries at boot; persist only if something changed.
	if s.dropExpiredLocked() {
		if err := saveLocked(s.file, s.rows); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// dropExpiredLocked removes expired entries, reporting whether any were
// removed. Caller must hold the lock.
func (s *JTIStore) dropExpiredLocked() bool {
	now := time.Now().Unix()
	changed := false
	for jti, expires := range s.rows {
		if expires <= now {
			delete(s.rows, jti)
			changed = true
		}
	}
	return changed
}

// Exists reports whether a JTI is still in use (non-expired). Expired entries
// are deleted on access.
func (s *JTIStore) Exists(jti string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.rows[jti]
	if !ok {
		return false, nil
	}
	if expires <= time.Now().Unix() {
		delete(s.rows, jti)
		if err := saveLocked(s.file, s.rows); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// Store marks a JTI as used until expiresAt.
func (s *JTIStore) Store(jti string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[jti] = expiresAt.Unix()
	return saveLocked(s.file, s.rows)
}

// Cleanup removes all expired JTIs and persists if anything changed.
func (s *JTIStore) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dropExpiredLocked() {
		if err := saveLocked(s.file, s.rows); err != nil {
			return err
		}
	}
	return nil
}

// Len returns the number of persisted entries (for diagnostics).
func (s *JTIStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Ensure JTIStore implements model.JTIStorageBackend.
var _ model.JTIStorageBackend = (*JTIStore)(nil)

// Log helper for cleanup wiring in main.
var _ = log.Debug
