package backends

import (
	"sort"
	"strconv"
	"sync"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// additionalClaimValueRow is the in-memory value of one entity configuration
// additional claim: {"<claim>": {"value": ..., "crit": ...}}.
type additionalClaimValueRow struct {
	Value any  `json:"value"`
	Crit  bool `json:"crit"`
}

// AdditionalClaimsStore implements model.AdditionalClaimsStore fully in
// memory. It is seeded at boot from
// federation_data.extra_entity_configuration_claims and there is no
// additional_claims.json file: Create/Set/Update/Delete mutations are lost on
// restart (no light caller mutates them at runtime; the config is the single
// source of truth). IDs are content-addressed (hashID(claim)).
type AdditionalClaimsStore struct {
	mu     sync.RWMutex
	rows   map[string]additionalClaimValueRow
	claims []model.EntityConfigurationAdditionalClaim
	byID   map[uint]*model.EntityConfigurationAdditionalClaim
}

// newAdditionalClaimsStore seeds the in-memory claim map from the config rows
// (order preserved; collisions are impossible with distinct claim names but
// checked for parity with the other stores).
func newAdditionalClaimsStore(claims []model.AddAdditionalClaim) (*AdditionalClaimsStore, error) {
	s := &AdditionalClaimsStore{rows: make(map[string]additionalClaimValueRow, len(claims))}
	for _, c := range claims {
		s.rows[c.Claim] = additionalClaimValueRow{Value: c.Value, Crit: c.Crit}
	}
	// Deterministic order for List: sorted by claim name.
	names := make([]string, 0, len(s.rows))
	for name := range s.rows {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[uint]string, len(names))
	for _, name := range names {
		row := s.rows[name]
		id := hashID(name)
		if err := checkNoIDCollision("additional_claims", seen, id, name); err != nil {
			return nil, err
		}
		s.claims = append(s.claims, model.EntityConfigurationAdditionalClaim{
			ID:    id,
			Claim: name,
			Value: row.Value,
			Crit:  row.Crit,
		})
	}
	s.reindex()
	return s, nil
}

func (s *AdditionalClaimsStore) reindex() {
	s.byID = make(map[uint]*model.EntityConfigurationAdditionalClaim, len(s.claims))
	for i := range s.claims {
		s.byID[s.claims[i].ID] = &s.claims[i]
	}
}

// List returns all additional claims.
func (s *AdditionalClaimsStore) List() ([]model.EntityConfigurationAdditionalClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.EntityConfigurationAdditionalClaim, len(s.claims))
	copy(out, s.claims)
	return out, nil
}

// Set replaces the complete set of additional claims.
func (s *AdditionalClaimsStore) Set(items []model.AddAdditionalClaim) ([]model.EntityConfigurationAdditionalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = make(map[string]additionalClaimValueRow, len(items))
	for _, it := range items {
		s.rows[it.Claim] = additionalClaimValueRow{Value: it.Value, Crit: it.Crit}
	}
	s.claims = s.claims[:0]
	for name, row := range s.rows {
		s.claims = append(s.claims, model.EntityConfigurationAdditionalClaim{
			ID:    hashID(name),
			Claim: name,
			Value: row.Value,
			Crit:  row.Crit,
		})
	}
	sort.Slice(s.claims, func(i, j int) bool { return s.claims[i].Claim < s.claims[j].Claim })
	s.reindex()
	out := make([]model.EntityConfigurationAdditionalClaim, len(s.claims))
	copy(out, s.claims)
	return out, nil
}

// Create inserts a claim; duplicates are rejected.
func (s *AdditionalClaimsStore) Create(item model.AddAdditionalClaim) (*model.EntityConfigurationAdditionalClaim, error) {
	if item.Claim == "" {
		return nil, model.ValidationError("claim is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[item.Claim]; exists {
		return nil, model.AlreadyExistsError("additional claim already exists")
	}
	s.rows[item.Claim] = additionalClaimValueRow{Value: item.Value, Crit: item.Crit}
	row := model.EntityConfigurationAdditionalClaim{
		ID:    hashID(item.Claim),
		Claim: item.Claim,
		Value: item.Value,
		Crit:  item.Crit,
	}
	s.claims = append(s.claims, row)
	sort.Slice(s.claims, func(i, j int) bool { return s.claims[i].Claim < s.claims[j].Claim })
	s.reindex()
	return &s.claims[sort.Search(len(s.claims), func(i int) bool { return s.claims[i].Claim >= item.Claim })], nil
}

func (s *AdditionalClaimsStore) findByIdent(ident string) (*model.EntityConfigurationAdditionalClaim, error) {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		if c, ok := s.byID[uint(id)]; ok {
			return c, nil
		}
	}
	for i := range s.claims {
		if s.claims[i].Claim == ident {
			return &s.claims[i], nil
		}
	}
	return nil, model.NotFoundError("additional claim not found")
}

// Get returns a claim by numeric ID or claim name.
func (s *AdditionalClaimsStore) Get(ident string) (*model.EntityConfigurationAdditionalClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	out := *c
	return &out, nil
}

// Update updates value/crit for a claim identified by ID or claim name.
func (s *AdditionalClaimsStore) Update(ident string, item model.AddAdditionalClaim) (*model.EntityConfigurationAdditionalClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	if item.Claim != "" && item.Claim != row.Claim {
		if _, exists := s.rows[item.Claim]; exists {
			return nil, model.AlreadyExistsError("additional claim already exists")
		}
		delete(s.rows, row.Claim)
		row.Claim = item.Claim
		row.ID = hashID(item.Claim)
	}
	row.Value = item.Value
	row.Crit = item.Crit
	s.rows[row.Claim] = additionalClaimValueRow{Value: row.Value, Crit: row.Crit}
	sort.Slice(s.claims, func(i, j int) bool { return s.claims[i].Claim < s.claims[j].Claim })
	s.reindex()
	out := *row
	return &out, nil
}

// Delete removes a claim by ID or claim name.
func (s *AdditionalClaimsStore) Delete(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.findByIdent(ident)
	if err != nil {
		return err
	}
	delete(s.rows, row.Claim)
	s.claims = s.claims[:0]
	for name, r := range s.rows {
		s.claims = append(s.claims, model.EntityConfigurationAdditionalClaim{
			ID:    hashID(name),
			Claim: name,
			Value: r.Value,
			Crit:  r.Crit,
		})
	}
	sort.Slice(s.claims, func(i, j int) bool { return s.claims[i].Claim < s.claims[j].Claim })
	s.reindex()
	return nil
}

var _ model.AdditionalClaimsStore = (*AdditionalClaimsStore)(nil)
