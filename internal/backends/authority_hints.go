package backends

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// authorityHintRow is the persisted shape of one authority hint. IDs are
// content-addressed (hashID(entity_id)) and never serialized.
type authorityHintRow struct {
	EntityID    string              `json:"entity_id"`
	Description string              `json:"description,omitempty"`
	JWKSSync    *model.JWKSSyncConf `json:"jwks_sync,omitempty"`
}

// AuthorityHintsStore implements model.AuthorityHintsStore on a JSON file.
type AuthorityHintsStore struct {
	*fileState
	rows  []authorityHintRow
	hints []model.AuthorityHint
	byID  map[uint]*model.AuthorityHint
}

func newAuthorityHintsStore(state *fileState) (*AuthorityHintsStore, error) {
	s := &AuthorityHintsStore{fileState: state}
	if err := rejectLegacyIDs(state.file, "authority_hints", "hints"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(state.file, "authority_hints", "hints"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	seen := make(map[uint]string, len(s.rows))
	for i := range s.rows {
		row := s.rows[i]
		id := hashID(row.EntityID)
		if err := checkNoIDCollision("authority_hints", seen, id, row.EntityID); err != nil {
			return nil, err
		}
		s.hints = append(s.hints, model.AuthorityHint{
			ID:          id,
			EntityID:    row.EntityID,
			Description: row.Description,
			JWKSSync:    row.JWKSSync,
		})
	}
	s.reindex()
	return s, nil
}

func (s *AuthorityHintsStore) reindex() {
	s.byID = make(map[uint]*model.AuthorityHint, len(s.hints))
	for i := range s.hints {
		s.byID[s.hints[i].ID] = &s.hints[i]
	}
}

// persist writes the in-memory hints back to the file (IDs are derived, not
// stored). Caller must hold the lock.
func (s *AuthorityHintsStore) persist() error {
	rows := make([]authorityHintRow, 0, len(s.hints))
	for i := range s.hints {
		h := &s.hints[i]
		rows = append(rows, authorityHintRow{
			EntityID:    h.EntityID,
			Description: h.Description,
			JWKSSync:    h.JWKSSync,
		})
	}
	return saveLocked(s.file, rows)
}

func (s *AuthorityHintsStore) findByIdent(ident string) (*model.AuthorityHint, error) {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		if h, ok := s.byID[uint(id)]; ok {
			return h, nil
		}
	}
	for i := range s.hints {
		if s.hints[i].EntityID == ident {
			return &s.hints[i], nil
		}
	}
	return nil, model.NotFoundError("authority hint not found")
}

// List returns all authority hints ordered by ID.
func (s *AuthorityHintsStore) List() ([]model.AuthorityHint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.AuthorityHint, len(s.hints))
	copy(out, s.hints)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Create adds a new authority hint. Duplicate entity IDs are rejected.
func (s *AuthorityHintsStore) Create(hint model.AddAuthorityHint) (*model.AuthorityHint, error) {
	if hint.EntityID == "" {
		return nil, model.ValidationError("entity_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.hints {
		if s.hints[i].EntityID == hint.EntityID {
			return nil, model.AlreadyExistsError("authority hint already exists")
		}
	}
	item := model.AuthorityHint{
		ID:          hashID(hint.EntityID),
		EntityID:    hint.EntityID,
		Description: hint.Description,
		JWKSSync:    hint.JWKSSync,
	}
	s.hints = append(s.hints, item)
	if err := s.persist(); err != nil {
		return nil, err
	}
	s.byID[item.ID] = &s.hints[len(s.hints)-1]
	return &s.hints[len(s.hints)-1], nil
}

// Get returns a hint by numeric ID or entity ID.
func (s *AuthorityHintsStore) Get(ident string) (*model.AuthorityHint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.findByIdent(ident)
}

// Update performs a full replacement on the hint identified by ID or entity
// ID.
func (s *AuthorityHintsStore) Update(ident string, update model.AddAuthorityHint) (*model.AuthorityHint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	if update.EntityID != "" {
		for i := range s.hints {
			if s.hints[i].EntityID == update.EntityID && s.hints[i].ID != item.ID {
				return nil, model.AlreadyExistsError("authority hint already exists")
			}
		}
		item.EntityID = update.EntityID
	}
	item.Description = update.Description
	item.JWKSSync = update.JWKSSync
	if err := s.persist(); err != nil {
		return nil, err
	}
	return item, nil
}

// Patch partially updates a hint: "description" sets/clears the description;
// "jwks_sync" sets/replaces/clears the sync config (mode validated).
func (s *AuthorityHintsStore) Patch(ident string, patch map[string]any) (*model.AuthorityHint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	if v, ok := patch["description"]; ok {
		if v == nil {
			item.Description = ""
		} else {
			desc, ok := v.(string)
			if !ok {
				return nil, model.ValidationError("description must be a string")
			}
			item.Description = desc
		}
	}
	if v, ok := patch["jwks_sync"]; ok {
		if v == nil {
			item.JWKSSync = nil
		} else {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, model.ValidationError("jwks_sync must be an object")
			}
			var cfg model.JWKSSyncConf
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, model.ValidationError("jwks_sync must be an object")
			}
			if err := cfg.Validate(); err != nil {
				return nil, model.ValidationError(err.Error())
			}
			item.JWKSSync = &cfg
		}
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return item, nil
}

// Delete removes the hint identified by ID or entity ID.
func (s *AuthorityHintsStore) Delete(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return err
	}
	for i := range s.hints {
		if s.hints[i].ID == item.ID {
			s.hints = append(s.hints[:i], s.hints[i+1:]...)
			break
		}
	}
	delete(s.byID, item.ID)
	return s.persist()
}

// String satisfies fmt.Stringer for logging.
func (s *AuthorityHintsStore) String() string {
	return fmt.Sprintf("authority_hints(%d)", len(s.hints))
}

var _ model.AuthorityHintsStore = (*AuthorityHintsStore)(nil)
