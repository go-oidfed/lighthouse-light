package backends

import (
	"fmt"
	"strings"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// TrustMarksStore implements model.TrustMarkedEntitiesStorageBackend over the
// shared trustMarkState subject rows (mirrors the DB where the marked-entities
// store IS the set of trust mark subjects). Approve/Block/Request upsert the
// subject status; subjects are persisted in trust_mark_subjects.json.
type TrustMarksStore struct {
	state *trustMarkState
}

func newTrustMarksStore(state *trustMarkState) *TrustMarksStore {
	return &TrustMarksStore{state: state}
}

func (s *TrustMarksStore) writeStatus(trustMarkType, entityID string, status model.Status) error {
	// Request handlers may pass zero-copy views into the (reused) request
	// buffer (fiber binds query/body strings without copying). Clone before
	// persisting so rows created here cannot be corrupted by a later request
	// reusing that buffer.
	trustMarkType = strings.Clone(trustMarkType)
	entityID = strings.Clone(entityID)
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(trustMarkType)
	if rec == nil {
		return model.NotFoundErrorFmt("unknown trust mark type: %s", trustMarkType)
	}
	sub := s.state.findSubjectByEntity(rec.ID, entityID)
	if sub == nil {
		sub = &model.TrustMarkSubject{
			ID:              hashID(rec.TrustMarkType, entityID),
			TrustMarkSpecID: rec.ID,
			EntityID:        entityID,
			Status:          status,
		}
		s.state.subjects[rec.ID] = append(s.state.subjects[rec.ID], sub)
	}
	sub.Status = status
	return s.state.saveLocked()
}

// Block marks a trust mark as blocked for an entity.
func (s *TrustMarksStore) Block(trustMarkType, entityID string) error {
	return s.writeStatus(trustMarkType, entityID, model.StatusBlocked)
}

// Approve marks a trust mark as active for an entity.
func (s *TrustMarksStore) Approve(trustMarkType, entityID string) error {
	return s.writeStatus(trustMarkType, entityID, model.StatusActive)
}

// Request marks a trust mark as pending for an entity.
func (s *TrustMarksStore) Request(trustMarkType, entityID string) error {
	return s.writeStatus(trustMarkType, entityID, model.StatusPending)
}

// TrustMarkedStatus returns the status of a trust mark for an entity; unknown
// combinations report StatusInactive.
func (s *TrustMarksStore) TrustMarkedStatus(trustMarkType, entityID string) (model.Status, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	rec := s.state.findSpecByIdent(trustMarkType)
	if rec == nil {
		return model.StatusInactive, nil
	}
	sub := s.state.findSubjectByEntity(rec.ID, entityID)
	if sub == nil {
		return model.StatusInactive, nil
	}
	return sub.Status, nil
}

// HasTrustMark checks if an entity has an active trust mark for a type.
func (s *TrustMarksStore) HasTrustMark(trustMarkType, entityID string) (bool, error) {
	status, err := s.TrustMarkedStatus(trustMarkType, entityID)
	if err != nil {
		return false, err
	}
	return status == model.StatusActive, nil
}

// Active returns all entities with an active trust mark; an empty
// trustMarkType returns entities with ≥1 active mark.
func (s *TrustMarksStore) Active(trustMarkType string) ([]string, error) {
	return s.entitiesWithStatus(trustMarkType, model.StatusActive)
}

// Blocked returns all entities blocked from a trust mark type.
func (s *TrustMarksStore) Blocked(trustMarkType string) ([]string, error) {
	return s.entitiesWithStatus(trustMarkType, model.StatusBlocked)
}

// Pending returns all entities with a pending trust mark.
func (s *TrustMarksStore) Pending(trustMarkType string) ([]string, error) {
	return s.entitiesWithStatus(trustMarkType, model.StatusPending)
}

func (s *TrustMarksStore) entitiesWithStatus(trustMarkType string, status model.Status) ([]string, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	var out []string
	for i := range s.state.specs {
		rec := &s.state.specs[i]
		if trustMarkType != "" && rec.TrustMarkType != trustMarkType {
			continue
		}
		for _, sub := range s.state.subjects[rec.ID] {
			if sub.Status == status {
				out = append(out, sub.EntityID)
			}
		}
	}
	return out, nil
}

// Delete removes a trust mark for an entity; unknown types are a no-op.
func (s *TrustMarksStore) Delete(trustMarkType, entityID string) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(trustMarkType)
	if rec == nil {
		return nil
	}
	subs := s.state.subjects[rec.ID]
	for i, sub := range subs {
		if sub.EntityID == entityID {
			s.state.subjects[rec.ID] = append(subs[:i], subs[i+1:]...)
			return s.state.saveLocked()
		}
	}
	return nil
}

// Load is a no-op: data is loaded at construction time.
func (s *TrustMarksStore) Load() error { return nil }

// String satisfies fmt.Stringer for logging.
func (s *TrustMarksStore) String() string {
	return fmt.Sprintf("trust_marks")
}
