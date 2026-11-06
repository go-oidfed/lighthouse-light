package backends

import (
	"strconv"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lib/jwx"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// trustMarkOwnerFileRow is the persisted owner of a trust mark type (inline
// on the type row; the DB's owner table is flattened here).
type trustMarkOwnerFileRow struct {
	EntityID    string   `json:"entity_id"`
	Description string   `json:"description,omitempty"`
	JWKS        jwx.JWKS `json:"jwks"`
}

// trustMarkIssuerFileRow is one persisted issuer attached to a type.
type trustMarkIssuerFileRow struct {
	Issuer      string `json:"issuer"`
	Description string `json:"description,omitempty"`
}

// trustMarkTypeFileRow is the persisted shape of one trust mark type with its
// owner and issuers inline. IDs are content-addressed and never serialized.
type trustMarkTypeFileRow struct {
	TrustMarkType string                   `json:"trust_mark_type"`
	Description   string                   `json:"description,omitempty"`
	Owner         *trustMarkOwnerFileRow   `json:"owner,omitempty"`
	Issuers       []trustMarkIssuerFileRow `json:"issuers,omitempty"`
}

// trustMarkTypeRecord is the in-memory type row with content-derived IDs and
// numeric links to the in-memory owner/issuer row slices (mirrors the DB join
// model internally; the file format is flat).
type trustMarkTypeRecord struct {
	ID            uint
	TrustMarkType string
	Description   string
	OwnerID       *uint
	IssuerIDs     []uint
}

// TrustMarkTypesStore implements model.TrustMarkTypesStore on a JSON file.
type TrustMarkTypesStore struct {
	*fileState
	rows    []trustMarkTypeFileRow
	types   []trustMarkTypeRecord
	owners  []model.TrustMarkOwner
	issuers []model.TrustMarkIssuer
}

func newTrustMarkTypesStore(state *fileState) (*TrustMarkTypesStore, error) {
	s := &TrustMarkTypesStore{fileState: state}
	if err := rejectLegacyIDs(state.file, "trust_mark_types", "types"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(state.file, "trust_mark_types", "types"); err != nil {
		return nil, err
	}
	if err := state.load(&s.rows); err != nil {
		return nil, err
	}
	seenType := make(map[uint]string, len(s.rows))
	for i := range s.rows {
		row := s.rows[i]
		typeID := hashID(row.TrustMarkType)
		if err := checkNoIDCollision("trust_mark_types", seenType, typeID, row.TrustMarkType); err != nil {
			return nil, err
		}
		rec := trustMarkTypeRecord{
			ID:            typeID,
			TrustMarkType: row.TrustMarkType,
			Description:   row.Description,
		}
		if row.Owner != nil {
			ownerID := hashID("owner", row.TrustMarkType, row.Owner.EntityID)
			s.owners = append(s.owners, model.TrustMarkOwner{
				ID:          ownerID,
				EntityID:    row.Owner.EntityID,
				Description: row.Owner.Description,
				JWKS:        model.JWKS{Keys: row.Owner.JWKS},
			})
			rec.OwnerID = &ownerID
		}
		seenIssuer := make(map[uint]string, len(row.Issuers))
		for _, iss := range row.Issuers {
			issuerID := hashID(rec.TrustMarkType, iss.Issuer)
			if err := checkNoIDCollision("trust_mark_types.issuers", seenIssuer, issuerID, iss.Issuer); err != nil {
				return nil, err
			}
			s.issuers = append(s.issuers, model.TrustMarkIssuer{
				ID:          issuerID,
				Issuer:      iss.Issuer,
				Description: iss.Description,
			})
			rec.IssuerIDs = append(rec.IssuerIDs, issuerID)
		}
		s.types = append(s.types, rec)
	}
	return s, nil
}

// persist writes the in-memory types back to the file (flat form; IDs are
// derived and never stored). Caller must hold the lock.
func (s *TrustMarkTypesStore) persist() error {
	rows := make([]trustMarkTypeFileRow, 0, len(s.types))
	for i := range s.types {
		t := &s.types[i]
		row := trustMarkTypeFileRow{
			TrustMarkType: t.TrustMarkType,
			Description:   t.Description,
		}
		if t.OwnerID != nil {
			if owner := s.findOwnerByID(*t.OwnerID); owner != nil {
				row.Owner = &trustMarkOwnerFileRow{
					EntityID:    owner.EntityID,
					Description: owner.Description,
					JWKS:        owner.JWKS.Keys,
				}
			}
		}
		for _, iid := range t.IssuerIDs {
			if iss := s.findIssuerByID(iid); iss != nil {
				row.Issuers = append(row.Issuers, trustMarkIssuerFileRow{
					Issuer:      iss.Issuer,
					Description: iss.Description,
				})
			}
		}
		rows = append(rows, row)
	}
	return saveLocked(s.file, rows)
}

func (s *TrustMarkTypesStore) findTypeByIdent(ident string) (*trustMarkTypeRecord, error) {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		for i := range s.types {
			if s.types[i].ID == uint(id) {
				return &s.types[i], nil
			}
		}
	}
	for i := range s.types {
		if s.types[i].TrustMarkType == ident {
			return &s.types[i], nil
		}
	}
	return nil, model.NotFoundError("trust mark type not found")
}

func (s *TrustMarkTypesStore) findOwnerByID(id uint) *model.TrustMarkOwner {
	for i := range s.owners {
		if s.owners[i].ID == id {
			return &s.owners[i]
		}
	}
	return nil
}

func (s *TrustMarkTypesStore) findIssuerByID(id uint) *model.TrustMarkIssuer {
	for i := range s.issuers {
		if s.issuers[i].ID == id {
			return &s.issuers[i]
		}
	}
	return nil
}

// assemble builds a model.TrustMarkType with resolved owner and issuers.
func (s *TrustMarkTypesStore) assemble(t *trustMarkTypeRecord) model.TrustMarkType {
	out := model.TrustMarkType{
		ID:            t.ID,
		TrustMarkType: t.TrustMarkType,
		Description:   t.Description,
	}
	if t.OwnerID != nil {
		if owner := s.findOwnerByID(*t.OwnerID); owner != nil {
			out.OwnerID = t.OwnerID
			out.Owner = owner
		}
	}
	for _, iid := range t.IssuerIDs {
		if iss := s.findIssuerByID(iid); iss != nil {
			out.Issuers = append(out.Issuers, *iss)
		}
	}
	return out
}

// List returns all trust mark types with their relations.
func (s *TrustMarkTypesStore) List() ([]model.TrustMarkType, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.TrustMarkType, 0, len(s.types))
	for i := range s.types {
		out = append(out, s.assemble(&s.types[i]))
	}
	return out, nil
}

// Create adds a new trust mark type; duplicate types are rejected. Optional
// owner and issuers are attached during creation.
func (s *TrustMarkTypesStore) Create(req model.AddTrustMarkType) (*model.TrustMarkType, error) {
	if req.TrustMarkType == "" {
		return nil, model.ValidationError("trust_mark_type is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.types {
		if s.types[i].TrustMarkType == req.TrustMarkType {
			return nil, model.AlreadyExistsError("trust mark type already exists")
		}
	}
	s.types = append(s.types, trustMarkTypeRecord{
		ID:            hashID(req.TrustMarkType),
		TrustMarkType: req.TrustMarkType,
		Description:   req.Description,
	})
	ident := req.TrustMarkType
	if req.TrustMarkOwner != nil {
		if _, err := s.createOwnerLocked(ident, *req.TrustMarkOwner); err != nil {
			return nil, err
		}
	}
	if len(req.TrustMarkIssuers) > 0 {
		if _, err := s.setIssuersLocked(ident, req.TrustMarkIssuers); err != nil {
			return nil, err
		}
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s.findTypeByIdentStable(ident)
}

// findTypeByIdentStable re-resolves after s.types mutation (pointers into
// s.types are invalidated by append).
func (s *TrustMarkTypesStore) findTypeByIdentStable(ident string) (*model.TrustMarkType, error) {
	rec, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	out := s.assemble(rec)
	return &out, nil
}

// Get returns a trust mark type by numeric ID or trust_mark_type.
func (s *TrustMarkTypesStore) Get(ident string) (*model.TrustMarkType, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	out := s.assemble(rec)
	return &out, nil
}

// Update replaces trust_mark_type/description (parity with the DB Update).
func (s *TrustMarkTypesStore) Update(ident string, req model.AddTrustMarkType) (*model.TrustMarkType, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	if req.TrustMarkType == "" {
		return nil, model.ValidationError("trust_mark_type is required")
	}
	for i := range s.types {
		if s.types[i].TrustMarkType == req.TrustMarkType && s.types[i].ID != item.ID {
			return nil, model.AlreadyExistsError("trust mark type already exists")
		}
	}
	item.TrustMarkType = req.TrustMarkType
	item.Description = req.Description
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s.findTypeByIdentStable(ident)
}

// Delete removes a type (owner/issuer links are dropped with it).
func (s *TrustMarkTypesStore) Delete(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return err
	}
	for i := range s.types {
		if s.types[i].ID == item.ID {
			s.types = append(s.types[:i], s.types[i+1:]...)
			break
		}
	}
	return s.persist()
}

// OwnersByType returns a map of trust_mark_type -> TrustMarkOwnerSpec for all
// types that have an owner.
func (s *TrustMarkTypesStore) OwnersByType() (oidfed.TrustMarkOwners, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(oidfed.TrustMarkOwners)
	for i := range s.types {
		t := &s.types[i]
		if t.OwnerID == nil {
			continue
		}
		owner := s.findOwnerByID(*t.OwnerID)
		if owner == nil {
			continue
		}
		out[t.TrustMarkType] = oidfed.TrustMarkOwnerSpec{
			ID:   owner.EntityID,
			JWKS: owner.JWKS.Keys,
		}
	}
	return out, nil
}

// IssuersByType returns a map of trust_mark_type -> []issuer entity IDs.
func (s *TrustMarkTypesStore) IssuersByType() (oidfed.AllowedTrustMarkIssuers, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(oidfed.AllowedTrustMarkIssuers)
	for i := range s.types {
		t := &s.types[i]
		for _, iid := range t.IssuerIDs {
			if iss := s.findIssuerByID(iid); iss != nil {
				out[t.TrustMarkType] = append(out[t.TrustMarkType], iss.Issuer)
			}
		}
	}
	return out, nil
}

// ListIssuers returns the issuers attached to a type.
func (s *TrustMarkTypesStore) ListIssuers(ident string) ([]model.TrustMarkIssuer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	return s.listIssuersLocked(item)
}

// resolveIssuerLocked finds or creates a global issuer, returning its ID.
func (s *TrustMarkTypesStore) resolveIssuerLocked(req model.AddTrustMarkIssuer) (uint, error) {
	if req.IssuerID != nil {
		if id, err := strconv.ParseUint(*req.IssuerID, 10, 64); err == nil {
			if iss := s.findIssuerByID(uint(id)); iss != nil {
				return iss.ID, nil
			}
		}
		return 0, model.NotFoundError("issuer not found")
	}
	if req.Issuer == "" {
		return 0, model.NotFoundError("issuer not specified")
	}
	for i := range s.issuers {
		if s.issuers[i].Issuer == req.Issuer {
			return s.issuers[i].ID, nil
		}
	}
	iss := model.TrustMarkIssuer{
		ID:          hashID(req.Issuer),
		Issuer:      req.Issuer,
		Description: req.Description,
	}
	s.issuers = append(s.issuers, iss)
	return iss.ID, nil
}

// SetIssuers replaces the issuer list of a type.
func (s *TrustMarkTypesStore) SetIssuers(ident string, in []model.AddTrustMarkIssuer) ([]model.TrustMarkIssuer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.setIssuersLocked(ident, in)
	if err != nil {
		return nil, err
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *TrustMarkTypesStore) setIssuersLocked(ident string, in []model.AddTrustMarkIssuer) ([]model.TrustMarkIssuer, error) {
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(in))
	for _, iss := range in {
		id, err := s.resolveIssuerLocked(iss)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	item.IssuerIDs = ids
	return s.listIssuersLocked(item)
}

// AddIssuer appends an issuer to a type.
func (s *TrustMarkTypesStore) AddIssuer(ident string, issuer model.AddTrustMarkIssuer) ([]model.TrustMarkIssuer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	id, err := s.resolveIssuerLocked(issuer)
	if err != nil {
		return nil, err
	}
	for _, existing := range item.IssuerIDs {
		if existing == id {
			return nil, model.AlreadyExistsError("issuer already attached to trust mark type")
		}
	}
	item.IssuerIDs = append(item.IssuerIDs, id)
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s.listIssuersLocked(item)
}

// DeleteIssuerByID removes an issuer from a type by issuer ID.
func (s *TrustMarkTypesStore) DeleteIssuerByID(ident string, issuerID uint) ([]model.TrustMarkIssuer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	for i, existing := range item.IssuerIDs {
		if existing == issuerID {
			item.IssuerIDs = append(item.IssuerIDs[:i], item.IssuerIDs[i+1:]...)
		}
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s.listIssuersLocked(item)
}

func (s *TrustMarkTypesStore) listIssuersLocked(item *trustMarkTypeRecord) ([]model.TrustMarkIssuer, error) {
	var out []model.TrustMarkIssuer
	for _, iid := range item.IssuerIDs {
		if iss := s.findIssuerByID(iid); iss != nil {
			out = append(out, *iss)
		}
	}
	return out, nil
}

// GetOwner returns the owner attached to a type.
func (s *TrustMarkTypesStore) GetOwner(ident string) (*model.TrustMarkOwner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	if item.OwnerID == nil {
		return nil, model.NotFoundError("trust mark owner not set")
	}
	owner := s.findOwnerByID(*item.OwnerID)
	if owner == nil {
		return nil, model.NotFoundError("trust mark owner not found")
	}
	out := *owner
	return &out, nil
}

// CreateOwner creates (or links) an owner for a type.
func (s *TrustMarkTypesStore) CreateOwner(ident string, req model.AddTrustMarkOwner) (*model.TrustMarkOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, err := s.createOwnerLocked(ident, req)
	if err != nil {
		return nil, err
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	out := *owner
	return &out, nil
}

func (s *TrustMarkTypesStore) createOwnerLocked(ident string, req model.AddTrustMarkOwner) (*model.TrustMarkOwner, error) {
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	var ownerID uint
	if req.OwnerID != nil {
		id, err := strconv.ParseUint(*req.OwnerID, 10, 64)
		if err != nil {
			return nil, model.NotFoundError("trust mark owner not found")
		}
		if s.findOwnerByID(uint(id)) == nil {
			return nil, model.NotFoundError("trust mark owner not found")
		}
		ownerID = uint(id)
	} else {
		if item.OwnerID != nil {
			if owner := s.findOwnerByID(*item.OwnerID); owner != nil {
				return owner, model.AlreadyExistsError("trust mark owner already exists")
			}
		}
		ownerID = hashID("owner", item.TrustMarkType, req.EntityID)
		s.owners = append(s.owners, model.TrustMarkOwner{
			ID:       ownerID,
			EntityID: req.EntityID,
			JWKS:     req.JWKS,
		})
	}
	item.OwnerID = &ownerID
	owner := s.findOwnerByID(ownerID)
	return owner, nil
}

// UpdateOwner replaces (or relinks) the owner of a type.
func (s *TrustMarkTypesStore) UpdateOwner(ident string, req model.AddTrustMarkOwner) (*model.TrustMarkOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return nil, err
	}
	if req.OwnerID != nil {
		id, err := strconv.ParseUint(*req.OwnerID, 10, 64)
		if err != nil {
			return nil, model.NotFoundError("trust mark owner not found")
		}
		if s.findOwnerByID(uint(id)) == nil {
			return nil, model.NotFoundError("trust mark owner not found")
		}
		item.OwnerID = new(uint)
		*item.OwnerID = uint(id)
	} else {
		if item.OwnerID == nil {
			return nil, model.NotFoundError("trust mark owner not set")
		}
		owner := s.findOwnerByID(*item.OwnerID)
		if owner == nil {
			return nil, model.NotFoundError("trust mark owner not found")
		}
		if req.EntityID != "" {
			owner.EntityID = req.EntityID
		}
		owner.JWKS = req.JWKS
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	owner := s.findOwnerByID(*item.OwnerID)
	out := *owner
	return &out, nil
}

// DeleteOwner detaches the owner from a type.
func (s *TrustMarkTypesStore) DeleteOwner(ident string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.findTypeByIdent(ident)
	if err != nil {
		return err
	}
	if item.OwnerID == nil {
		return nil
	}
	item.OwnerID = nil
	return s.persist()
}

var _ model.TrustMarkTypesStore = (*TrustMarkTypesStore)(nil)
