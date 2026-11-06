package backends

import (
	"strconv"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// publishedTrustMarkRow is the persisted shape of one published trust mark.
// IDs are content-addressed (hashID(trust_mark_type)) and never serialized.
type publishedTrustMarkRow struct {
	TrustMarkType      string                         `json:"trust_mark_type"`
	TrustMarkIssuer    string                         `json:"trust_mark_issuer,omitempty"`
	TrustMarkJWT       string                         `json:"trust_mark,omitempty"`
	Refresh            bool                           `json:"refresh"`
	MinLifetime        int                            `json:"min_lifetime,omitempty"`
	RefreshGracePeriod int                            `json:"refresh_grace_period,omitempty"`
	RefreshRateLimit   int                            `json:"refresh_rate_limit,omitempty"`
	SelfIssuanceSpec   *model.SelfIssuedTrustMarkSpec `json:"self_issuance_spec,omitempty"`
}

// PublishedTrustMarksStore implements model.PublishedTrustMarksStore on a JSON
// file.
type PublishedTrustMarksStore struct {
	*fileState
	rows  []publishedTrustMarkRow
	marks []model.PublishedTrustMark
	byID  map[uint]*model.PublishedTrustMark
}

func newPublishedTrustMarksStore(state *fileState) (*PublishedTrustMarksStore, error) {
	s := &PublishedTrustMarksStore{fileState: state}
	if err := rejectLegacyIDs(state.file, "published_trust_marks", "trust_marks"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(state.file, "published_trust_marks", "trust_marks"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	seen := make(map[uint]string, len(s.rows))
	for i := range s.rows {
		row := s.rows[i]
		id := hashID(row.TrustMarkType)
		if err := checkNoIDCollision("published_trust_marks", seen, id, row.TrustMarkType); err != nil {
			return nil, err
		}
		s.marks = append(s.marks, model.PublishedTrustMark{
			ID:                 id,
			TrustMarkType:      row.TrustMarkType,
			TrustMarkIssuer:    row.TrustMarkIssuer,
			TrustMarkJWT:       row.TrustMarkJWT,
			Refresh:            row.Refresh,
			MinLifetime:        row.MinLifetime,
			RefreshGracePeriod: row.RefreshGracePeriod,
			RefreshRateLimit:   row.RefreshRateLimit,
			SelfIssuanceSpec:   row.SelfIssuanceSpec,
		})
	}
	s.reindex()
	return s, nil
}

func (s *PublishedTrustMarksStore) reindex() {
	s.byID = make(map[uint]*model.PublishedTrustMark, len(s.marks))
	for i := range s.marks {
		s.byID[s.marks[i].ID] = &s.marks[i]
	}
}

// persist writes the in-memory marks back to the file (IDs derived, never
// stored). Caller must hold the lock.
func (s *PublishedTrustMarksStore) persist() error {
	rows := make([]publishedTrustMarkRow, 0, len(s.marks))
	for i := range s.marks {
		m := &s.marks[i]
		rows = append(rows, publishedTrustMarkRow{
			TrustMarkType:      m.TrustMarkType,
			TrustMarkIssuer:    m.TrustMarkIssuer,
			TrustMarkJWT:       m.TrustMarkJWT,
			Refresh:            m.Refresh,
			MinLifetime:        m.MinLifetime,
			RefreshGracePeriod: m.RefreshGracePeriod,
			RefreshRateLimit:   m.RefreshRateLimit,
			SelfIssuanceSpec:   m.SelfIssuanceSpec,
		})
	}
	return saveLocked(s.file, rows)
}

func (s *PublishedTrustMarksStore) findByIdent(ident string) (*model.PublishedTrustMark, error) {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		if m, ok := s.byID[uint(id)]; ok {
			return m, nil
		}
	}
	for i := range s.marks {
		if s.marks[i].TrustMarkType == ident {
			return &s.marks[i], nil
		}
	}
	return nil, model.NotFoundError("trust mark not found")
}

// List returns all published trust marks.
func (s *PublishedTrustMarksStore) List() ([]model.PublishedTrustMark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.PublishedTrustMark, len(s.marks))
	copy(out, s.marks)
	return out, nil
}

// Create adds a published trust mark; duplicate types are rejected.
func (s *PublishedTrustMarksStore) Create(add model.AddTrustMark) (*model.PublishedTrustMark, error) {
	if add.TrustMarkType == "" && add.TrustMark == "" {
		return nil, model.ValidationError("trust mark must have trust_mark_type or trust_mark")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.marks {
		if s.marks[i].TrustMarkType == add.TrustMarkType {
			return nil, model.AlreadyExistsError("trust mark already exists")
		}
	}
	item := model.PublishedTrustMark{
		ID:                 hashID(add.TrustMarkType),
		TrustMarkType:      add.TrustMarkType,
		TrustMarkIssuer:    add.TrustMarkIssuer,
		TrustMarkJWT:       add.TrustMark,
		Refresh:            add.Refresh,
		MinLifetime:        add.MinLifetime,
		RefreshGracePeriod: add.RefreshGracePeriod,
		RefreshRateLimit:   add.RefreshRateLimit,
		SelfIssuanceSpec:   add.SelfIssuanceSpec,
	}
	s.marks = append(s.marks, item)
	if err := s.persist(); err != nil {
		return nil, err
	}
	s.byID[item.ID] = &s.marks[len(s.marks)-1]
	return &s.marks[len(s.marks)-1], nil
}

// Get returns a published trust mark by numeric ID or trust_mark_type.
func (s *PublishedTrustMarksStore) Get(ident string) (*model.PublishedTrustMark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	out := *m
	return &out, nil
}

// Update replaces a published trust mark.
func (s *PublishedTrustMarksStore) Update(ident string, update model.AddTrustMark) (*model.PublishedTrustMark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	if update.TrustMarkType == "" {
		update.TrustMarkType = item.TrustMarkType
	}
	for i := range s.marks {
		if s.marks[i].TrustMarkType == update.TrustMarkType && s.marks[i].ID != item.ID {
			return nil, model.AlreadyExistsError("trust mark already exists")
		}
	}
	item.TrustMarkType = update.TrustMarkType
	item.TrustMarkIssuer = update.TrustMarkIssuer
	item.TrustMarkJWT = update.TrustMark
	item.Refresh = update.Refresh
	item.MinLifetime = update.MinLifetime
	item.RefreshGracePeriod = update.RefreshGracePeriod
	item.RefreshRateLimit = update.RefreshRateLimit
	item.SelfIssuanceSpec = update.SelfIssuanceSpec
	if err := s.persist(); err != nil {
		return nil, err
	}
	out := *item
	return &out, nil
}

// Patch partially updates a published trust mark (only non-nil fields).
func (s *PublishedTrustMarksStore) Patch(ident string, patch model.UpdateTrustMark) (*model.PublishedTrustMark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return nil, err
	}
	if patch.TrustMarkIssuer != nil {
		item.TrustMarkIssuer = *patch.TrustMarkIssuer
	}
	if patch.TrustMark != nil {
		item.TrustMarkJWT = *patch.TrustMark
	}
	if patch.Refresh != nil {
		item.Refresh = *patch.Refresh
	}
	if patch.MinLifetime != nil {
		item.MinLifetime = *patch.MinLifetime
	}
	if patch.RefreshGracePeriod != nil {
		item.RefreshGracePeriod = *patch.RefreshGracePeriod
	}
	if patch.RefreshRateLimit != nil {
		item.RefreshRateLimit = *patch.RefreshRateLimit
	}
	if patch.SelfIssuanceSpec != nil {
		item.SelfIssuanceSpec = patch.SelfIssuanceSpec
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	out := *item
	return &out, nil
}

// Delete removes a published trust mark.
func (s *PublishedTrustMarksStore) Delete(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findByIdent(ident)
	if err != nil {
		return err
	}
	for i := range s.marks {
		if s.marks[i].ID == item.ID {
			s.marks = append(s.marks[:i], s.marks[i+1:]...)
			break
		}
	}
	delete(s.byID, item.ID)
	return s.persist()
}

var _ model.PublishedTrustMarksStore = (*PublishedTrustMarksStore)(nil)
