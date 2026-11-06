package backends

import (
	"strconv"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lib/jwx"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// trustAnchorRow is the persisted shape of one trust anchor. IDs are
// content-addressed (hashID(entity_id)) and never serialized.
type trustAnchorRow struct {
	EntityID         string   `json:"entity_id"`
	EnableJWKSUpdate bool     `json:"enable_jwks_update,omitempty"`
	KeyPollInterval  int64    `json:"key_poll_interval,omitempty"` // seconds; 0 = derive from EC expiration
	JWKS             jwx.JWKS `json:"jwks"`
}

// TrustAnchorsStore implements model.TrustAnchorStore on a JSON file.
type TrustAnchorsStore struct {
	*fileState
	rows    []trustAnchorRow
	anchors []model.TrustAnchor
}

func newTrustAnchorsStore(state *fileState) (*TrustAnchorsStore, error) {
	s := &TrustAnchorsStore{fileState: state}
	if err := rejectLegacyIDs(state.file, "trust_anchors", "anchors"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(state.file, "trust_anchors", "anchors"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	seen := make(map[uint]string, len(s.rows))
	for i := range s.rows {
		row := s.rows[i]
		id := hashID(row.EntityID)
		if err := checkNoIDCollision("trust_anchors", seen, id, row.EntityID); err != nil {
			return nil, err
		}
		s.anchors = append(s.anchors, model.TrustAnchor{
			ID:               id,
			EntityID:         row.EntityID,
			EnableJWKSUpdate: row.EnableJWKSUpdate,
			KeyPollInterval:  row.KeyPollInterval,
			JWKS:             model.JWKS{Keys: row.JWKS},
		})
	}
	return s, nil
}

// persist writes the in-memory anchors back to the file (IDs derived, not
// stored). Caller must hold the lock.
func (s *TrustAnchorsStore) persist() error {
	rows := make([]trustAnchorRow, 0, len(s.anchors))
	for i := range s.anchors {
		a := &s.anchors[i]
		rows = append(rows, trustAnchorRow{
			EntityID:         a.EntityID,
			EnableJWKSUpdate: a.EnableJWKSUpdate,
			KeyPollInterval:  a.KeyPollInterval,
			JWKS:             a.JWKS.Keys,
		})
	}
	return saveLocked(s.file, rows)
}

// List returns all trust anchors.
func (s *TrustAnchorsStore) List() ([]model.TrustAnchor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.TrustAnchor, len(s.anchors))
	copy(out, s.anchors)
	return out, nil
}

func (s *TrustAnchorsStore) findByEntityID(entityID string) (*model.TrustAnchor, error) {
	for i := range s.anchors {
		if s.anchors[i].EntityID == entityID {
			return &s.anchors[i], nil
		}
	}
	return nil, model.NotFoundError("trust anchor not found")
}

// Get returns a trust anchor by numeric ID or entity_id.
func (s *TrustAnchorsStore) Get(ident string) (*model.TrustAnchor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		for i := range s.anchors {
			if s.anchors[i].ID == uint(id) {
				return &s.anchors[i], nil
			}
		}
		return nil, model.NotFoundError("trust anchor not found")
	}
	return s.findByEntityID(ident)
}

// Create adds a new trust anchor; duplicates are rejected.
func (s *TrustAnchorsStore) Create(req model.AddTrustAnchor) (*model.TrustAnchor, error) {
	if req.EntityID == "" {
		return nil, model.ValidationError("entity_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.anchors {
		if s.anchors[i].EntityID == req.EntityID {
			return nil, model.AlreadyExistsError("trust anchor already exists")
		}
	}
	item := model.TrustAnchor{
		ID:               hashID(req.EntityID),
		EntityID:         req.EntityID,
		EnableJWKSUpdate: req.EnableJWKSUpdate,
		KeyPollInterval:  req.KeyPollInterval,
	}
	if req.JWKS != nil && req.JWKS.Keys.Set != nil && req.JWKS.Keys.Len() > 0 {
		item.JWKS = *req.JWKS
	}
	s.anchors = append(s.anchors, item)
	if err := s.persist(); err != nil {
		return nil, err
	}
	return &s.anchors[len(s.anchors)-1], nil
}

// Update replaces a trust anchor resolved by ID or entity_id.
func (s *TrustAnchorsStore) Update(ident string, req model.AddTrustAnchor) (*model.TrustAnchor, error) {
	if req.EntityID == "" {
		return nil, model.ValidationError("entity_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.resolveLocked(ident)
	if err != nil {
		return nil, err
	}
	for i := range s.anchors {
		if s.anchors[i].EntityID == req.EntityID && s.anchors[i].ID != item.ID {
			return nil, model.AlreadyExistsError("trust anchor already exists")
		}
	}
	item.EntityID = req.EntityID
	item.EnableJWKSUpdate = req.EnableJWKSUpdate
	item.KeyPollInterval = req.KeyPollInterval
	if req.JWKS != nil && req.JWKS.Keys.Set != nil && req.JWKS.Keys.Len() > 0 {
		item.JWKS = *req.JWKS
	} else if req.JWKS != nil {
		item.JWKS = model.JWKS{}
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *TrustAnchorsStore) resolveLocked(ident string) (*model.TrustAnchor, error) {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		for i := range s.anchors {
			if s.anchors[i].ID == uint(id) {
				return &s.anchors[i], nil
			}
		}
		return nil, model.NotFoundError("trust anchor not found")
	}
	return s.findByEntityID(ident)
}

// Delete removes a trust anchor by numeric ID or entity_id.
func (s *TrustAnchorsStore) Delete(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.resolveLocked(ident)
	if err != nil {
		return err
	}
	for i := range s.anchors {
		if s.anchors[i].ID == item.ID {
			s.anchors = append(s.anchors[:i], s.anchors[i+1:]...)
			break
		}
	}
	return s.persist()
}

// GetJWKS returns the stored JWKS for an entity, or nil if none is stored.
func (s *TrustAnchorsStore) GetJWKS(entityID string) (*jwx.JWKS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ta, err := s.findByEntityID(entityID)
	if err != nil {
		if _, ok := err.(model.NotFoundError); ok {
			return nil, nil
		}
		return nil, err
	}
	if ta.JWKS.Keys.Set == nil {
		return nil, nil
	}
	keys := ta.JWKS.Keys
	return &keys, nil
}

// UpdateJWKS stores/replaces the JWKS for an entity. The entity must exist.
func (s *TrustAnchorsStore) UpdateJWKS(entityID string, jwks jwx.JWKS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ta, err := s.findByEntityID(entityID)
	if err != nil {
		return err
	}
	ta.JWKS = model.JWKS{Keys: jwks}
	return s.persist()
}

// RegisterEntityJWKSFile implements oidfed.JWKStorage. The store keeps every
// anchor's JWKS inline in trust_anchors.json; there is no per-entity JWKS
// file, so registering one is a no-op.
func (s *TrustAnchorsStore) RegisterEntityJWKSFile(_, _ string) error {
	return nil
}

// The store serves as the JWKS storage for lighthouse's TA JWKS refresher
// (lighthouse.SetupTAJWKSRefresher): refresh results persist into
// trust_anchors.json rows and are visible to the in-memory repo (shared
// model.TrustAnchor pointers).
var _ oidfed.JWKStorage = (*TrustAnchorsStore)(nil)

var _ model.TrustAnchorStore = (*TrustAnchorsStore)(nil)
