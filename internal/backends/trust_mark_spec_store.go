package backends

import (
	"encoding/json"
	"strconv"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// TrustMarkSpecsStore implements model.TrustMarkSpecStore on the shared
// trustMarkState. Specs persist to trust_mark_specs.json; subjects persist to
// trust_mark_subjects.json. It also holds a reference to the instances store
// so subject deletion / blocking revokes issued instances (DB parity).
type TrustMarkSpecsStore struct {
	state     *trustMarkState
	instances *TrustMarkInstancesStore
}

func newTrustMarkSpecsStore(state *trustMarkState) *TrustMarkSpecsStore {
	return &TrustMarkSpecsStore{state: state}
}

// SetInstances wires the instances store for subject-revocation on delete or
// status change (called once during Build).
func (s *TrustMarkSpecsStore) SetInstances(instances *TrustMarkInstancesStore) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	s.instances = instances
}

// List returns all specs.
func (s *TrustMarkSpecsStore) List() ([]model.TrustMarkSpec, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	out := make([]model.TrustMarkSpec, 0, len(s.state.specs))
	for i := range s.state.specs {
		out = append(out, s.state.specs[i].toModel())
	}
	return out, nil
}

// Create adds a new spec; duplicate types are rejected.
func (s *TrustMarkSpecsStore) Create(spec *model.AddTrustMarkSpec) (*model.TrustMarkSpec, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.findSpecByIdent(spec.TrustMarkType) != nil {
		return nil, model.AlreadyExistsError("trust mark spec already exists for this type")
	}
	if err := validateEligibilityConfig("trust_mark_specs", spec.TrustMarkType, spec.EligibilityConfig); err != nil {
		return nil, err
	}
	rec := trustMarkSpecRecord{
		ID:                hashID(spec.TrustMarkType),
		TrustMarkType:     spec.TrustMarkType,
		Lifetime:          spec.Lifetime,
		Ref:               spec.Ref,
		LogoURI:           spec.LogoURI,
		DelegationJWT:     spec.DelegationJWT,
		AdditionalClaims:  spec.AdditionalClaims,
		Description:       spec.Description,
		EligibilityConfig: spec.EligibilityConfig,
		CacheTTL:          spec.CacheTTL,
	}
	s.state.specs = append(s.state.specs, rec)
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := rec.toModel()
	return &out, nil
}

// Get returns a spec by numeric ID or trust_mark_type.
func (s *TrustMarkSpecsStore) Get(ident string) (*model.TrustMarkSpec, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	rec := s.state.findSpecByIdent(ident)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	out := rec.toModel()
	return &out, nil
}

// GetByType returns a spec by trust_mark_type.
func (s *TrustMarkSpecsStore) GetByType(trustMarkType string) (*model.TrustMarkSpec, error) {
	return s.Get(trustMarkType)
}

// Update replaces a spec (full replacement).
func (s *TrustMarkSpecsStore) Update(ident string, spec *model.AddTrustMarkSpec) (*model.TrustMarkSpec, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(ident)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	for i := range s.state.specs {
		if s.state.specs[i].TrustMarkType == spec.TrustMarkType && s.state.specs[i].ID != rec.ID {
			return nil, model.AlreadyExistsError("trust mark spec already exists for this type")
		}
	}
	if err := validateEligibilityConfig("trust_mark_specs", spec.TrustMarkType, spec.EligibilityConfig); err != nil {
		return nil, err
	}
	rec.TrustMarkType = spec.TrustMarkType
	rec.Lifetime = spec.Lifetime
	rec.Ref = spec.Ref
	rec.LogoURI = spec.LogoURI
	rec.DelegationJWT = spec.DelegationJWT
	rec.AdditionalClaims = spec.AdditionalClaims
	rec.Description = spec.Description
	rec.EligibilityConfig = spec.EligibilityConfig
	rec.CacheTTL = spec.CacheTTL
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := rec.toModel()
	return &out, nil
}

// Patch partially updates a spec.
func (s *TrustMarkSpecsStore) Patch(ident string, updates map[string]any) (*model.TrustMarkSpec, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(ident)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	for key, value := range updates {
		switch key {
		case "additional_claims":
			if value == nil {
				rec.AdditionalClaims = nil
			} else {
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, model.ValidationError("additional_claims must be an object")
				}
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					return nil, model.ValidationError("additional_claims must be an object")
				}
				rec.AdditionalClaims = m
			}
		case "eligibility_config":
			if value == nil {
				rec.EligibilityConfig = nil
			} else {
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, model.ValidationError("eligibility_config must be an object")
				}
				var cfg model.EligibilityConfig
				if err := json.Unmarshal(raw, &cfg); err != nil {
					return nil, model.ValidationError("eligibility_config must be an object")
				}
				if err := validateEligibilityConfig("trust_mark_specs", rec.TrustMarkType, &cfg); err != nil {
					return nil, err
				}
				rec.EligibilityConfig = &cfg
			}
		case "trust_mark_type":
			t, ok := value.(string)
			if !ok || t == "" {
				return nil, model.ValidationError("trust_mark_type must be a non-empty string")
			}
			for i := range s.state.specs {
				if s.state.specs[i].TrustMarkType == t && s.state.specs[i].ID != rec.ID {
					return nil, model.AlreadyExistsError("trust mark spec already exists for this type")
				}
			}
			rec.TrustMarkType = t
		case "lifetime":
			if n, ok := toUint(value); ok {
				rec.Lifetime = n
			}
		case "cache_ttl":
			if n, ok := toInt(value); ok {
				rec.CacheTTL = n
			}
		case "ref":
			if str, ok := value.(string); ok {
				rec.Ref = str
			}
		case "logo_uri":
			if str, ok := value.(string); ok {
				rec.LogoURI = str
			}
		case "delegation_jwt":
			if str, ok := value.(string); ok {
				rec.DelegationJWT = str
			}
		case "description":
			if str, ok := value.(string); ok {
				rec.Description = str
			}
		}
	}
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := rec.toModel()
	return &out, nil
}

// Delete removes a spec.
func (s *TrustMarkSpecsStore) Delete(ident string) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(ident)
	if rec == nil {
		return model.NotFoundError("trust mark spec not found")
	}
	for i := range s.state.specs {
		if s.state.specs[i].ID == rec.ID {
			s.state.specs = append(s.state.specs[:i], s.state.specs[i+1:]...)
			break
		}
	}
	delete(s.state.subjects, rec.ID)
	return s.state.saveLocked()
}

// ListSubjects returns all subjects of a spec, optionally filtered by status.
func (s *TrustMarkSpecsStore) ListSubjects(specIdent string, status *model.Status) ([]model.TrustMarkSubject, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	var out []model.TrustMarkSubject
	for _, sub := range s.state.subjects[rec.ID] {
		copied := *sub
		if status == nil || copied.Status == *status {
			out = append(out, copied)
		}
	}
	return out, nil
}

// CreateSubject adds a subject to a spec; duplicates are rejected.
func (s *TrustMarkSpecsStore) CreateSubject(specIdent string, subject *model.AddTrustMarkSubject) (*model.TrustMarkSubject, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	if s.state.findSubjectByEntity(rec.ID, subject.EntityID) != nil {
		return nil, model.AlreadyExistsError("subject already exists for this trust mark spec")
	}
	sub := &model.TrustMarkSubject{
		ID:               hashID(rec.TrustMarkType, subject.EntityID),
		TrustMarkSpecID:  rec.ID,
		EntityID:         subject.EntityID,
		Status:           subject.Status,
		Description:      subject.Description,
		AdditionalClaims: subject.AdditionalClaims,
	}
	s.state.subjects[rec.ID] = append(s.state.subjects[rec.ID], sub)
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := *sub
	return &out, nil
}

// GetSubject returns a subject by numeric ID or entity ID within a spec.
func (s *TrustMarkSpecsStore) GetSubject(specIdent, subjectIdent string) (*model.TrustMarkSubject, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	if id, err := strconv.ParseUint(subjectIdent, 10, 64); err == nil {
		for _, sub := range s.state.subjects[rec.ID] {
			if sub.ID == uint(id) {
				out := *sub
				return &out, nil
			}
		}
	}
	for _, sub := range s.state.subjects[rec.ID] {
		if sub.EntityID == subjectIdent {
			out := *sub
			return &out, nil
		}
	}
	return nil, model.NotFoundError("trust mark subject not found")
}

// UpdateSubject replaces a subject by ID or entity ID.
func (s *TrustMarkSpecsStore) UpdateSubject(specIdent, subjectIdent string, subject *model.AddTrustMarkSubject) (*model.TrustMarkSubject, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	existing := s.findSubjectLocked(rec.ID, subjectIdent)
	if existing == nil {
		return nil, model.NotFoundError("trust mark subject not found")
	}
	targetEntity := subject.EntityID
	if targetEntity == "" {
		targetEntity = existing.EntityID
	}
	for _, sub := range s.state.subjects[rec.ID] {
		if sub.EntityID == targetEntity && sub.ID != existing.ID {
			return nil, model.AlreadyExistsError("subject already exists for this trust mark spec")
		}
	}
	existing.EntityID = targetEntity
	existing.Status = subject.Status
	existing.Description = subject.Description
	existing.AdditionalClaims = subject.AdditionalClaims
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := *existing
	return &out, nil
}

// DeleteSubject removes a subject and revokes its issued instances.
func (s *TrustMarkSpecsStore) DeleteSubject(specIdent, subjectIdent string) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return model.NotFoundError("trust mark spec not found")
	}
	existing := s.findSubjectLocked(rec.ID, subjectIdent)
	if existing == nil {
		return model.NotFoundError("trust mark subject not found")
	}
	s.revokeInstancesLocked(existing)
	subs := s.state.subjects[rec.ID]
	for i, sub := range subs {
		if sub.ID == existing.ID {
			s.state.subjects[rec.ID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	return s.state.saveLocked()
}

// ChangeSubjectStatus changes a subject's status, revoking instances when the
// new status is blocked or inactive.
func (s *TrustMarkSpecsStore) ChangeSubjectStatus(specIdent, subjectIdent string, status model.Status) (*model.TrustMarkSubject, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	rec := s.state.findSpecByIdent(specIdent)
	if rec == nil {
		return nil, model.NotFoundError("trust mark spec not found")
	}
	existing := s.findSubjectLocked(rec.ID, subjectIdent)
	if existing == nil {
		return nil, model.NotFoundError("trust mark subject not found")
	}
	existing.Status = status
	if status == model.StatusBlocked || status == model.StatusInactive {
		s.revokeInstancesLocked(existing)
	}
	if err := s.state.saveLocked(); err != nil {
		return nil, err
	}
	out := *existing
	return &out, nil
}

// findSubjectLocked resolves a subject by numeric ID or entity ID within a
// spec. Caller must hold the state lock.
func (s *TrustMarkSpecsStore) findSubjectLocked(specID uint, subjectIdent string) *model.TrustMarkSubject {
	if id, err := strconv.ParseUint(subjectIdent, 10, 64); err == nil {
		for _, sub := range s.state.subjects[specID] {
			if sub.ID == uint(id) {
				return sub
			}
		}
	}
	return s.state.findSubjectByEntity(specID, subjectIdent)
}

// revokeInstancesLocked revokes all active instances issued for a subject.
// Caller must hold the state lock. The instances store has its own lock and
// never calls back into the spec state, so ordering state.mu → instances.mu
// is deadlock-free.
func (s *TrustMarkSpecsStore) revokeInstancesLocked(subject *model.TrustMarkSubject) {
	if s.instances == nil {
		return
	}
	_, _ = s.instances.RevokeBySubjectID(subject.ID)
}

func toUint(v any) (uint, bool) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0, false
		}
		return uint(n), true
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint(n), true
	default:
		return 0, false
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
