package backends

import (
	"sort"
	"strings"
	"time"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// TrustMarkInstancesStore implements model.IssuedTrustMarkInstanceStore on a
// JSON file. It holds a reference to the shared spec state to resolve subject
// IDs (DB parity: instances link to subject rows).
type TrustMarkInstancesStore struct {
	*fileState
	rows  []model.IssuedTrustMarkInstance
	byJTI map[string]*model.IssuedTrustMarkInstance
	state *trustMarkState
}

// SetState wires the shared spec state for FindSubjectID (Build).
func (s *TrustMarkInstancesStore) SetState(state *trustMarkState) {
	s.state = state
}

func newTrustMarkInstancesStore(state *fileState) (*TrustMarkInstancesStore, error) {
	s := &TrustMarkInstancesStore{fileState: state}
	if err := rejectWrappedFormat(state.file, "trust_mark_instances", "instances"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	s.reindex()
	return s, nil
}

func (s *TrustMarkInstancesStore) reindex() {
	s.byJTI = make(map[string]*model.IssuedTrustMarkInstance, len(s.rows))
	for i := range s.rows {
		s.byJTI[s.rows[i].JTI] = &s.rows[i]
	}
}

// Create records a new issued trust mark instance.
func (s *TrustMarkInstancesStore) Create(instance *model.IssuedTrustMarkInstance) error {
	if instance.JTI == "" {
		return model.ValidationError("JTI is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byJTI[instance.JTI]; exists {
		return model.AlreadyExistsErrorFmt("trust mark instance already exists: %s", instance.JTI)
	}
	now := int(time.Now().Unix())
	instance.CreatedAt = now
	instance.UpdatedAt = now
	cp := *instance
	// Clone request-derived string fields (Subject, TrustMarkType): request
	// handlers bind query/body values as zero-copy views into the reused
	// request buffer, so the values would be corrupted when a later request
	// reuses that buffer — including rows later persisted back to the file.
	cp.JTI = strings.Clone(instance.JTI)
	cp.Subject = strings.Clone(instance.Subject)
	cp.TrustMarkType = strings.Clone(instance.TrustMarkType)
	// handlers bind query/body values as zero-copy views into the reused
	// request buffer, so the values would be corrupted when a later request
	// reuses that buffer — including rows later persisted back to the file.
	s.rows = append(s.rows, cp)
	s.byJTI[cp.JTI] = &s.rows[len(s.rows)-1]
	return saveLocked(s.file, s.rows)
}

// GetByJTI retrieves an instance by its JTI.
func (s *TrustMarkInstancesStore) GetByJTI(jti string) (*model.IssuedTrustMarkInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.byJTI[jti]
	if !ok {
		return nil, model.NotFoundErrorFmt("trust mark instance not found: %s", jti)
	}
	out := *inst
	return &out, nil
}

// Revoke marks an instance as revoked.
func (s *TrustMarkInstancesStore) Revoke(jti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.byJTI[jti]
	if !ok {
		return model.NotFoundErrorFmt("trust mark instance not found: %s", jti)
	}
	inst.Revoked = true
	inst.UpdatedAt = int(time.Now().Unix())
	return saveLocked(s.file, s.rows)
}

// RevokeBySubjectID revokes all active instances for a subject; returns the
// number revoked.
func (s *TrustMarkInstancesStore) RevokeBySubjectID(subjectID uint) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for _, inst := range s.byJTI {
		if inst.TrustMarkSubjectID == subjectID && !inst.Revoked {
			inst.Revoked = true
			inst.UpdatedAt = int(time.Now().Unix())
			count++
		}
	}
	if count > 0 {
		if err := saveLocked(s.file, s.rows); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// GetStatus returns the status of an instance (revoked > expired > active).
// Unknown JTIs return a NotFoundError.
func (s *TrustMarkInstancesStore) GetStatus(jti string) (model.TrustMarkInstanceStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.byJTI[jti]
	if !ok {
		return "", model.NotFoundErrorFmt("trust mark instance not found: %s", jti)
	}
	if inst.Revoked {
		return model.TrustMarkStatusRevoked, nil
	}
	if inst.ExpiresAt > 0 && int(time.Now().Unix()) > inst.ExpiresAt {
		return model.TrustMarkStatusExpired, nil
	}
	return model.TrustMarkStatusActive, nil
}

// ListBySubject returns all instances for a type+entity, newest first.
func (s *TrustMarkInstancesStore) ListBySubject(trustMarkType, entityID string) ([]model.IssuedTrustMarkInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.IssuedTrustMarkInstance
	for _, inst := range s.byJTI {
		if inst.TrustMarkType == trustMarkType && inst.Subject == entityID {
			out = append(out, *inst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// ListActiveSubjects returns distinct entities with a valid (non-revoked,
// non-expired) instance for the type.
func (s *TrustMarkInstancesStore) ListActiveSubjects(trustMarkType string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := int(time.Now().Unix())
	seen := make(map[string]bool)
	var out []string
	for _, inst := range s.byJTI {
		if inst.TrustMarkType != trustMarkType || inst.Revoked {
			continue
		}
		if inst.ExpiresAt > 0 && inst.ExpiresAt <= now {
			continue
		}
		if !seen[inst.Subject] {
			seen[inst.Subject] = true
			out = append(out, inst.Subject)
		}
	}
	return out, nil
}

// HasActiveInstance checks for a valid instance for type+entity.
func (s *TrustMarkInstancesStore) HasActiveInstance(trustMarkType, entityID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := int(time.Now().Unix())
	for _, inst := range s.byJTI {
		if inst.TrustMarkType == trustMarkType && inst.Subject == entityID && !inst.Revoked {
			if inst.ExpiresAt == 0 || inst.ExpiresAt > now {
				return true, nil
			}
		}
	}
	return false, nil
}

// DeleteExpired removes instances expired before now-retentionDays; returns
// the number removed.
func (s *TrustMarkInstancesStore) DeleteExpired(retentionDays int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := int(time.Now().AddDate(0, 0, -retentionDays).Unix())
	var count int64
	kept := s.rows[:0]
	for i := range s.rows {
		inst := &s.rows[i]
		if inst.ExpiresAt > 0 && inst.ExpiresAt < cutoff {
			count++
			continue
		}
		kept = append(kept, *inst)
	}
	s.rows = kept
	s.reindex()
	if count > 0 {
		if err := saveLocked(s.file, s.rows); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// FindSubjectID returns the trust mark subject ID for type+entity (0 if the
// subject has no record; instances may still be issued without one).
func (s *TrustMarkInstancesStore) FindSubjectID(trustMarkType, entityID string) (uint, error) {
	if s.state == nil {
		return 0, nil
	}
	return s.findSubjectIDFromState(s.state, trustMarkType, entityID), nil
}

// findSubjectIDFromState resolves the subject ID via the shared state.
func (s *TrustMarkInstancesStore) findSubjectIDFromState(state *trustMarkState, trustMarkType, entityID string) uint {
	state.mu.RLock()
	defer state.mu.RUnlock()
	rec := state.findSpecByIdent(trustMarkType)
	if rec == nil {
		return 0
	}
	sub := state.findSubjectByEntity(rec.ID, entityID)
	if sub == nil {
		return 0
	}
	return sub.ID
}

var _ model.IssuedTrustMarkInstanceStore = (*TrustMarkInstancesStore)(nil)
