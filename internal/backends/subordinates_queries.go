package backends

import (
	"fmt"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// GetAll returns all subordinates as basic infos (with entity types).
func (s *SubordinatesStore) GetAll() ([]model.BasicSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.BasicSubordinateInfo, 0, len(s.rows))
	for i := range s.rows {
		info := s.rows[i].toModel()
		out = append(out, info.BasicSubordinateInfo)
	}
	return out, nil
}

// GetByStatus returns all subordinates with a specific status.
func (s *SubordinatesStore) GetByStatus(status model.Status) ([]model.BasicSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.BasicSubordinateInfo
	for i := range s.rows {
		if s.rows[i].Status == status {
			info := s.rows[i].toModel()
			out = append(out, info.BasicSubordinateInfo)
		}
	}
	return out, nil
}

// GetByEntityTypes returns subordinates whose entity types contain ALL given
// types.
func (s *SubordinatesStore) GetByEntityTypes(entityTypes []string) ([]model.BasicSubordinateInfo, error) {
	return s.byEntityTypes(nil, entityTypes, true)
}

// GetByAnyEntityType returns subordinates whose entity types contain ANY of
// the given types.
func (s *SubordinatesStore) GetByAnyEntityType(entityTypes []string) ([]model.BasicSubordinateInfo, error) {
	return s.byEntityTypes(nil, entityTypes, false)
}

// GetByStatusAndEntityTypes filters by status and ALL entity types.
func (s *SubordinatesStore) GetByStatusAndEntityTypes(status model.Status, entityTypes []string) ([]model.BasicSubordinateInfo, error) {
	return s.byEntityTypes(&status, entityTypes, true)
}

// GetByStatusAndAnyEntityType filters by status and ANY entity type.
func (s *SubordinatesStore) GetByStatusAndAnyEntityType(status model.Status, entityTypes []string) ([]model.BasicSubordinateInfo, error) {
	return s.byEntityTypes(&status, entityTypes, false)
}

func (s *SubordinatesStore) byEntityTypes(status *model.Status, entityTypes []string, requireAll bool) ([]model.BasicSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	has := func(rec *subordinateRecord, t string) bool {
		for _, e := range rec.EntityTypes {
			if e == t {
				return true
			}
		}
		return false
	}
	matches := func(rec *subordinateRecord) bool {
		if status != nil && rec.Status != *status {
			return false
		}
		if len(entityTypes) == 0 {
			return true
		}
		if requireAll {
			for _, t := range entityTypes {
				if !has(rec, t) {
					return false
				}
			}
			return true
		}
		for _, t := range entityTypes {
			if has(rec, t) {
				return true
			}
		}
		return false
	}
	var out []model.BasicSubordinateInfo
	for i := range s.rows {
		if matches(&s.rows[i]) {
			info := s.rows[i].toModel()
			out = append(out, info.BasicSubordinateInfo)
		}
	}
	return out, nil
}

// ListEnabledForJWKSRefresh returns all subordinates with
// EnableJWKSUpdate=true, with their full info and JWKS.
func (s *SubordinatesStore) ListEnabledForJWKSRefresh() ([]model.ExtendedSubordinateInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.ExtendedSubordinateInfo
	for i := range s.rows {
		if s.rows[i].EnableJWKSUpdate {
			out = append(out, s.rows[i].toModel())
		}
	}
	return out, nil
}

// ListAdditionalClaims returns the claims of a subordinate by DB ID.
func (s *SubordinatesStore) ListAdditionalClaims(subordinateDBID string) ([]model.SubordinateAdditionalClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return nil, err
	}
	info := s.rows[idx].toModel()
	return info.SubordinateAdditionalClaims, nil
}

// SetAdditionalClaims replaces all claims of a subordinate.
func (s *SubordinatesStore) SetAdditionalClaims(subordinateDBID string, claims []model.AddAdditionalClaim) ([]model.SubordinateAdditionalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return nil, err
	}
	rec := &s.rows[idx]
	rec.AdditionalClaims = nil
	result := make([]model.SubordinateAdditionalClaim, 0, len(claims))
	for _, c := range claims {
		rec.AdditionalClaims = append(rec.AdditionalClaims, subordinateClaimRecord{Claim: c.Claim, Value: c.Value, Crit: c.Crit})
		result = append(result, model.SubordinateAdditionalClaim{
			ID: hashID(rec.EntityID, c.Claim), SubordinateID: hashID(rec.EntityID), Claim: c.Claim, Value: c.Value, Crit: c.Crit,
		})
	}
	if err := saveLocked(s.file, s.rows); err != nil {
		return nil, err
	}
	return result, nil
}

// CreateAdditionalClaim adds one claim to a subordinate.
func (s *SubordinatesStore) CreateAdditionalClaim(subordinateDBID string, claim model.AddAdditionalClaim) (*model.SubordinateAdditionalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return nil, err
	}
	rec := &s.rows[idx]
	if rec.claimIDByName(claim.Claim) != 0 {
		return nil, model.AlreadyExistsErrorFmt("claim %q already exists for this subordinate", claim.Claim)
	}
	rec.AdditionalClaims = append(rec.AdditionalClaims, subordinateClaimRecord{Claim: claim.Claim, Value: claim.Value, Crit: claim.Crit})
	if err := saveLocked(s.file, s.rows); err != nil {
		return nil, err
	}
	return &model.SubordinateAdditionalClaim{
		ID: hashID(rec.EntityID, claim.Claim), SubordinateID: hashID(rec.EntityID), Claim: claim.Claim, Value: claim.Value, Crit: claim.Crit,
	}, nil
}

// GetAdditionalClaim returns one claim by ID for a subordinate.
func (s *SubordinatesStore) GetAdditionalClaim(subordinateDBID string, claimID string) (*model.SubordinateAdditionalClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return nil, err
	}
	rec := &s.rows[idx]
	var id uint
	if _, err := fmt.Sscanf(claimID, "%d", &id); err != nil {
		return nil, model.NotFoundError("additional claim not found")
	}
	for _, c := range rec.AdditionalClaims {
		if hashID(rec.EntityID, c.Claim) == id {
			return &model.SubordinateAdditionalClaim{
				ID: id, SubordinateID: hashID(rec.EntityID), Claim: c.Claim, Value: c.Value, Crit: c.Crit,
			}, nil
		}
	}
	return nil, model.NotFoundError("additional claim not found")
}

// UpdateAdditionalClaim updates one claim of a subordinate.
func (s *SubordinatesStore) UpdateAdditionalClaim(subordinateDBID string, claimID string, claim model.AddAdditionalClaim) (*model.SubordinateAdditionalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return nil, err
	}
	rec := &s.rows[idx]
	var id uint
	if _, err := fmt.Sscanf(claimID, "%d", &id); err != nil {
		return nil, model.NotFoundError("additional claim not found")
	}
	for i := range rec.AdditionalClaims {
		if hashID(rec.EntityID, rec.AdditionalClaims[i].Claim) == id {
			if claim.Claim != "" && claim.Claim != rec.AdditionalClaims[i].Claim {
				for _, c := range rec.AdditionalClaims {
					if c.Claim == claim.Claim {
						return nil, model.AlreadyExistsErrorFmt("claim %q already exists for this subordinate", claim.Claim)
					}
				}
				rec.AdditionalClaims[i].Claim = claim.Claim
			}
			rec.AdditionalClaims[i].Value = claim.Value
			rec.AdditionalClaims[i].Crit = claim.Crit
			if err := saveLocked(s.file, s.rows); err != nil {
				return nil, err
			}
			return &model.SubordinateAdditionalClaim{
				ID: hashID(rec.EntityID, rec.AdditionalClaims[i].Claim), SubordinateID: hashID(rec.EntityID),
				Claim: rec.AdditionalClaims[i].Claim, Value: rec.AdditionalClaims[i].Value, Crit: rec.AdditionalClaims[i].Crit,
			}, nil
		}
	}
	return nil, model.NotFoundError("additional claim not found")
}

// DeleteAdditionalClaim removes one claim of a subordinate.
func (s *SubordinatesStore) DeleteAdditionalClaim(subordinateDBID string, claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.findIdxByDBID(subordinateDBID)
	if err != nil {
		return err
	}
	rec := &s.rows[idx]
	var id uint
	if _, err := fmt.Sscanf(claimID, "%d", &id); err != nil {
		return model.NotFoundError("additional claim not found")
	}
	for i, c := range rec.AdditionalClaims {
		if hashID(rec.EntityID, c.Claim) == id {
			rec.AdditionalClaims = append(rec.AdditionalClaims[:i], rec.AdditionalClaims[i+1:]...)
			return saveLocked(s.file, s.rows)
		}
	}
	return model.NotFoundError("additional claim not found")
}
