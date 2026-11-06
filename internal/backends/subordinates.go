package backends

import (
	"fmt"
	"strconv"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lib/jwx"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// subordinateClaimRecord is the persisted shape of one subordinate claim. IDs
// are content-addressed (hashID(entity_id, claim)) and never serialized.
type subordinateClaimRecord struct {
	Claim string `json:"claim"`
	Value any    `json:"value"`
	Crit  bool   `json:"crit"`
}

// subordinateRecord is the persisted shape of one subordinate. It deliberately
// does not reuse model.ExtendedSubordinateInfo's JSON (its custom
// MarshalJSON flattens additional_claims into a map and would not round-trip).
// IDs are content-addressed (hashID(entity_id)) and never serialized.
type subordinateRecord struct {
	EntityID         string                          `json:"entity_id"`
	Description      string                          `json:"description,omitempty"`
	EntityTypes      []string                        `json:"registered_entity_types,omitempty"`
	Status           model.Status                    `json:"status"`
	JWKS             jwx.JWKS                        `json:"jwks"`
	Metadata         *oidfed.Metadata                `json:"metadata,omitempty"`
	MetadataPolicy   *oidfed.MetadataPolicies        `json:"metadata_policy,omitempty"`
	Constraints      *oidfed.ConstraintSpecification `json:"constraints,omitempty"`
	AdditionalClaims []subordinateClaimRecord        `json:"additional_claims,omitempty"`
	EnableJWKSUpdate bool                            `json:"enable_jwks_update,omitempty"`
	JWKSPollInterval int64                           `json:"jwks_poll_interval,omitempty"`
}

// toModel converts the record into a model.ExtendedSubordinateInfo.
func (r *subordinateRecord) toModel() model.ExtendedSubordinateInfo {
	id := hashID(r.EntityID)
	info := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{
			ID:               id,
			EntityID:         r.EntityID,
			Description:      r.Description,
			Status:           r.Status,
			EnableJWKSUpdate: r.EnableJWKSUpdate,
			JWKSPollInterval: r.JWKSPollInterval,
		},
		JWKS:           model.JWKS{Keys: r.JWKS},
		Metadata:       r.Metadata,
		MetadataPolicy: r.MetadataPolicy,
		Constraints:    r.Constraints,
	}
	for _, t := range r.EntityTypes {
		info.SubordinateEntityTypes = append(info.SubordinateEntityTypes, model.SubordinateEntityType{EntityType: t, SubordinateID: id})
	}
	info.SubordinateAdditionalClaims = make([]model.SubordinateAdditionalClaim, 0, len(r.AdditionalClaims))
	for _, c := range r.AdditionalClaims {
		info.SubordinateAdditionalClaims = append(info.SubordinateAdditionalClaims, model.SubordinateAdditionalClaim{
			ID: hashID(r.EntityID, c.Claim), SubordinateID: id, Claim: c.Claim, Value: c.Value, Crit: c.Crit,
		})
	}
	return info
}

// fromModel converts a model.ExtendedSubordinateInfo into a record.
func fromModel(info model.ExtendedSubordinateInfo) subordinateRecord {
	r := subordinateRecord{
		EntityID:         info.EntityID,
		Description:      info.Description,
		Status:           info.Status,
		JWKS:             info.JWKS.Keys,
		Metadata:         info.Metadata,
		MetadataPolicy:   info.MetadataPolicy,
		Constraints:      info.Constraints,
		EnableJWKSUpdate: info.EnableJWKSUpdate,
		JWKSPollInterval: info.JWKSPollInterval,
	}
	for _, t := range info.SubordinateEntityTypes {
		r.EntityTypes = append(r.EntityTypes, t.EntityType)
	}
	for _, c := range info.SubordinateAdditionalClaims {
		r.AdditionalClaims = append(r.AdditionalClaims, subordinateClaimRecord{Claim: c.Claim, Value: c.Value, Crit: c.Crit})
	}
	return r
}

// claimIDByName returns the deterministic claim ID for a claim name. Claim
// lookups by "ID" use hashID(entity_id, claim); an absent claim reports 0.
func (r *subordinateRecord) claimIDByName(name string) uint {
	for _, c := range r.AdditionalClaims {
		if c.Claim == name {
			return hashID(r.EntityID, name)
		}
	}
	return 0
}

// SubordinatesStore implements model.SubordinateStorageBackend on a JSON file.
// It holds a KV reference to apply the general (entity-level) claims/metadata/
// policy/constraints fallback exactly like the DB storage layer.
type SubordinatesStore struct {
	*fileState
	rows []subordinateRecord
	kv   model.KeyValueStore
}

func newSubordinatesStore(state *fileState, kv model.KeyValueStore) (*SubordinatesStore, error) {
	s := &SubordinatesStore{fileState: state, kv: kv}
	if err := rejectLegacyIDs(state.file, "subordinates", "subordinates"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(state.file, "subordinates", "subordinates"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	// Fail fast on hand-written data files: an active subordinate must have
	// keys (admin API parity).
	for i := range s.rows {
		rec := &s.rows[i]
		if rec.Status == model.StatusActive && !jwksHasKeys(&model.JWKS{Keys: rec.JWKS}) {
			return nil, fmt.Errorf("store subordinates: %s: status cannot be active without keys", rec.EntityID)
		}
	}
	return s, nil
}

func (s *SubordinatesStore) findIdxByEntityID(entityID string) int {
	for i := range s.rows {
		if s.rows[i].EntityID == entityID {
			return i
		}
	}
	return -1
}

func (s *SubordinatesStore) findIdxByDBID(id string) (int, error) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return -1, model.NotFoundErrorFmt("invalid subordinate ID: %s", id)
	}
	want := uint(n)
	for i := range s.rows {
		if hashID(s.rows[i].EntityID) == want {
			return i, nil
		}
	}
	return -1, nil
}

// applyGeneralFallbacks fills subordinate fields from the KV store when the
// subordinate-specific values are not set (mirror of the DB storage layer).
func (s *SubordinatesStore) applyGeneralFallbacks(info *model.ExtendedSubordinateInfo) error {
	if s.kv == nil {
		return nil
	}
	if info.MetadataPolicy == nil {
		if _, err := s.kv.GetAs(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyMetadataPolicy, &info.MetadataPolicy); err != nil {
			return fmt.Errorf("failed to get general metadata policy: %w", err)
		}
	}
	if info.Metadata == nil {
		if _, err := s.kv.GetAs(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyMetadata, &info.Metadata); err != nil {
			return fmt.Errorf("failed to get general metadata: %w", err)
		}
	}
	if info.Constraints == nil {
		if _, err := s.kv.GetAs(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyConstraints, &info.Constraints); err != nil {
			return fmt.Errorf("failed to get general constraints: %w", err)
		}
	}
	if len(info.SubordinateAdditionalClaims) == 0 {
		var generalClaims []model.SubordinateAdditionalClaim
		found, err := s.kv.GetAs(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyAdditionalClaims, &generalClaims)
		if err != nil {
			return fmt.Errorf("failed to get general additional claims: %w", err)
		}
		if found {
			info.SubordinateAdditionalClaims = generalClaims
		}
	}
	return nil
}

// Add stores a new subordinate; a duplicate entity ID is rejected.
func (s *SubordinatesStore) Add(info model.ExtendedSubordinateInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findIdxByEntityID(info.EntityID) != -1 {
		return model.AlreadyExistsErrorFmt("subordinate with entity_id %s already exists", info.EntityID)
	}
	rec := fromModel(info)
	s.rows = append(s.rows, rec)
	return saveLocked(s.file, s.rows)
}

// Update replaces a subordinate by entity ID (upsert semantics).
func (s *SubordinatesStore) Update(entityID string, info model.ExtendedSubordinateInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIdxByEntityID(entityID)
	rec := fromModel(info)
	if idx == -1 {
		s.rows = append(s.rows, rec)
	} else {
		s.rows[idx] = rec
	}
	return saveLocked(s.file, s.rows)
}

// Delete removes a subordinate by entity ID.
func (s *SubordinatesStore) Delete(entityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIdxByEntityID(entityID)
	if idx == -1 {
		return model.NotFoundError("subordinate not found")
	}
	s.rows = append(s.rows[:idx], s.rows[idx+1:]...)
	return saveLocked(s.file, s.rows)
}

// DeleteByDBID removes a subordinate by DB primary key.
func (s *SubordinatesStore) DeleteByDBID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(id)
	if err != nil {
		return err
	}
	s.rows = append(s.rows[:idx], s.rows[idx+1:]...)
	return saveLocked(s.file, s.rows)
}

// UpdateStatus updates the status by entity ID.
func (s *SubordinatesStore) UpdateStatus(entityID string, status model.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIdxByEntityID(entityID)
	if idx == -1 {
		return model.NotFoundError("subordinate not found")
	}
	s.rows[idx].Status = status
	return saveLocked(s.file, s.rows)
}

// UpdateStatusByDBID updates the status by DB primary key.
func (s *SubordinatesStore) UpdateStatusByDBID(id string, status model.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(id)
	if err != nil {
		return err
	}
	s.rows[idx].Status = status
	return saveLocked(s.file, s.rows)
}

// UpdateJWKSByDBID replaces the JWKS by DB primary key and returns the row.
func (s *SubordinatesStore) UpdateJWKSByDBID(id string, jwks model.JWKS) (*model.JWKS, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(id)
	if err != nil {
		return nil, err
	}
	s.rows[idx].JWKS = jwks.Keys
	if err := saveLocked(s.file, s.rows); err != nil {
		return nil, err
	}
	out := model.JWKS{Keys: s.rows[idx].JWKS}
	return &out, nil
}

// UpdateJWKSByEntityID replaces the JWKS by entity ID.
func (s *SubordinatesStore) UpdateJWKSByEntityID(entityID string, jwks model.JWKS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIdxByEntityID(entityID)
	if idx == -1 {
		return model.NotFoundError("subordinate not found")
	}
	s.rows[idx].JWKS = jwks.Keys
	return saveLocked(s.file, s.rows)
}

// Get retrieves a subordinate by entity ID, applying general fallbacks.
func (s *SubordinatesStore) Get(entityID string) (*model.ExtendedSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := s.findIdxByEntityID(entityID)
	if idx == -1 {
		return nil, nil
	}
	info := s.rows[idx].toModel()
	if err := s.applyGeneralFallbacks(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetByDBID retrieves a subordinate by DB primary key, applying fallbacks.
func (s *SubordinatesStore) GetByDBID(id string) (*model.ExtendedSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, err := s.findIdxByDBID(id)
	if err != nil {
		return nil, nil
	}
	if idx == -1 {
		return nil, nil
	}
	info := s.rows[idx].toModel()
	if err := s.applyGeneralFallbacks(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetByDBIDRaw returns the stored row without general fallbacks.
func (s *SubordinatesStore) GetByDBIDRaw(id string) (*model.ExtendedSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, err := s.findIdxByDBID(id)
	if err != nil {
		return nil, nil
	}
	if idx == -1 {
		return nil, nil
	}
	info := s.rows[idx].toModel()
	return &info, nil
}

// Load is a no-op: data is loaded at construction time.
func (s *SubordinatesStore) Load() error { return nil }

func jwksHasKeys(jwks *model.JWKS) bool {
	if jwks == nil {
		return false
	}
	return jwks.Keys.Set != nil && jwks.Keys.Len() > 0
}
