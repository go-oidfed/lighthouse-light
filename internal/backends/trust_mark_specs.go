package backends

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/go-oidfed/lighthouse/storage/model"
)

// trustMarkSpecFileRow is the persisted shape of one trust mark spec. IDs are
// content-addressed (hashID(trust_mark_type)) and never serialized.
type trustMarkSpecFileRow struct {
	TrustMarkType     string                   `json:"trust_mark_type"`
	Lifetime          uint                     `json:"lifetime,omitempty"`
	Ref               string                   `json:"ref,omitempty"`
	LogoURI           string                   `json:"logo_uri,omitempty"`
	DelegationJWT     string                   `json:"delegation_jwt,omitempty"`
	AdditionalClaims  map[string]any           `json:"additional_claims,omitempty"`
	Description       string                   `json:"description,omitempty"`
	EligibilityConfig *model.EligibilityConfig `json:"eligibility_config,omitempty"`
	CacheTTL          int                      `json:"cache_ttl,omitempty"`
}

// trustMarkState holds specs and subjects under one mutex, persisted to two
// files: trust_mark_specs.json (specs) and trust_mark_subjects.json
// (subjects).
type trustMarkState struct {
	mu          sync.RWMutex
	specFile    *fileState
	subjectFile *fileState
	rows        []trustMarkSpecFileRow
	specs       []trustMarkSpecRecord
	// subjects per spec ID (in-memory index; persisted via subjectFile rows)
	subjects map[uint][]*model.TrustMarkSubject
}

func newTrustMarkState(specFile, subjectFile *fileState) (*trustMarkState, error) {
	st := &trustMarkState{
		mu:          sync.RWMutex{},
		specFile:    specFile,
		subjectFile: subjectFile,
		subjects:    make(map[uint][]*model.TrustMarkSubject),
	}
	if err := rejectLegacyIDs(specFile.file, "trust_mark_specs", "specs"); err != nil {
		return nil, err
	}
	if err := rejectWrappedFormat(specFile.file, "trust_mark_specs", "specs"); err != nil {
		return nil, err
	}
	if err := specFile.load(&st.rows); err != nil {
		return nil, err
	}
	for i := range st.rows {
		row := st.rows[i]
		if err := validateEligibilityConfig("trust_mark_specs", row.TrustMarkType, row.EligibilityConfig); err != nil {
			return nil, err
		}
		st.specs = append(st.specs, trustMarkSpecRecord{
			ID:                hashID(row.TrustMarkType),
			TrustMarkType:     row.TrustMarkType,
			Lifetime:          row.Lifetime,
			Ref:               row.Ref,
			LogoURI:           row.LogoURI,
			DelegationJWT:     row.DelegationJWT,
			AdditionalClaims:  row.AdditionalClaims,
			Description:       row.Description,
			EligibilityConfig: row.EligibilityConfig,
			CacheTTL:          row.CacheTTL,
		})
	}
	if err := st.loadSubjects(); err != nil {
		return nil, err
	}
	return st, nil
}

// trustMarkSpecRecord is the in-memory spec row (with content-derived ID).
type trustMarkSpecRecord struct {
	ID                uint
	TrustMarkType     string
	Lifetime          uint
	Ref               string
	LogoURI           string
	DelegationJWT     string
	AdditionalClaims  map[string]any
	Description       string
	EligibilityConfig *model.EligibilityConfig
	CacheTTL          int
}

// toModel converts a spec record to a model.TrustMarkSpec.
func (r *trustMarkSpecRecord) toModel() model.TrustMarkSpec {
	return model.TrustMarkSpec{
		ID:                r.ID,
		TrustMarkType:     r.TrustMarkType,
		Lifetime:          r.Lifetime,
		Ref:               r.Ref,
		LogoURI:           r.LogoURI,
		DelegationJWT:     r.DelegationJWT,
		AdditionalClaims:  r.AdditionalClaims,
		Description:       r.Description,
		EligibilityConfig: r.EligibilityConfig,
		CacheTTL:          r.CacheTTL,
	}
}

func fromSpecModel(spec model.TrustMarkSpec) trustMarkSpecRecord {
	return trustMarkSpecRecord{
		ID:                spec.ID,
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
}

// trustMarkSubjectFileRow is the persisted shape of one trust mark subject in
// trust_mark_subjects.json (subjects are shared between the specs store and
// the trust marked entities store, mirroring the DB where both are subject
// rows). The trust_mark_type is the map key, not a row field.
type trustMarkSubjectFileRow struct {
	EntityID         string         `json:"entity_id"`
	Status           model.Status   `json:"status"`
	Description      string         `json:"description,omitempty"`
	AdditionalClaims map[string]any `json:"additional_claims,omitempty"`
}

// loadSubjects loads trust_mark_subjects.json (a mapping from trust_mark_type
// to subject rows), rejecting the legacy spec_id/id format and skipping
// subjects whose spec vanished.
func (st *trustMarkState) loadSubjects() error {
	content, err := os.ReadFile(st.subjectFile.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store trust_marks: failed to read %s: %w", st.subjectFile.file, err)
	}
	if len(content) == 0 {
		return nil
	}
	var raw map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return fmt.Errorf(
			"store trust_marks: %s: file uses the pre-v0.4 format (a flat array of subject rows); trust_mark_subjects.json is now a mapping from trust_mark_type to subject rows — re-provision (see docs/data-directory.md)",
			st.subjectFile.file,
		)
	}
	for _, rows := range raw {
		for _, row := range rows {
			if _, ok := row["spec_id"]; ok {
				return legacyErr(st.subjectFile.file)
			}
			if _, ok := row["id"]; ok {
				return legacyErr(st.subjectFile.file)
			}
		}
	}
	var byType map[string][]trustMarkSubjectFileRow
	if err := json.Unmarshal(content, &byType); err != nil {
		return fmt.Errorf("store trust_marks: failed to parse %s (fix or remove the file to reset): %w", st.subjectFile.file, err)
	}
	for typ, subs := range byType {
		spec := st.findSpecByIdent(typ)
		if spec == nil {
			continue
		}
		for i := range subs {
			sub := &subs[i]
			st.subjects[spec.ID] = append(st.subjects[spec.ID], &model.TrustMarkSubject{
				ID:               hashID(spec.TrustMarkType, sub.EntityID),
				TrustMarkSpecID:  spec.ID,
				EntityID:         sub.EntityID,
				Status:           sub.Status,
				Description:      sub.Description,
				AdditionalClaims: sub.AdditionalClaims,
			})
		}
	}
	return nil
}

// validateEligibilityConfig rejects unknown modes and custom modes without a
// checker (fail fast on hand-written data files).
func validateEligibilityConfig(store, trustMarkType string, cfg *model.EligibilityConfig) error {
	if cfg == nil {
		return nil
	}
	switch cfg.Mode {
	case model.EligibilityModeDBOnly, model.EligibilityModeCheckOnly,
		model.EligibilityModeDBOrCheck, model.EligibilityModeDBAndCheck:
		return nil
	case model.EligibilityModeCustom:
		if cfg.Checker == nil {
			return fmt.Errorf("store %s: trust mark spec %q: eligibility_config mode %q requires a checker", store, trustMarkType, model.EligibilityModeCustom)
		}
		return nil
	default:
		return fmt.Errorf("store %s: trust mark spec %q: eligibility_config.mode %q invalid (supported: db_only, check_only, db_or_check, db_and_check, custom)", store, trustMarkType, cfg.Mode)
	}
}

// materializeMissing writes initial state for both files when missing.
func (st *trustMarkState) materializeMissing() error {
	if err := materializeMissing(st.specFile, []trustMarkSpecFileRow{}); err != nil {
		return err
	}
	if err := materializeMissing(st.subjectFile, map[string][]trustMarkSubjectFileRow{}); err != nil {
		return err
	}
	return nil
}

// saveLocked persists both files.
func (st *trustMarkState) saveLocked() error {
	rows := make([]trustMarkSpecFileRow, 0, len(st.specs))
	for i := range st.specs {
		spec := &st.specs[i]
		rows = append(rows, trustMarkSpecFileRow{
			TrustMarkType:     spec.TrustMarkType,
			Lifetime:          spec.Lifetime,
			Ref:               spec.Ref,
			LogoURI:           spec.LogoURI,
			DelegationJWT:     spec.DelegationJWT,
			AdditionalClaims:  spec.AdditionalClaims,
			Description:       spec.Description,
			EligibilityConfig: spec.EligibilityConfig,
			CacheTTL:          spec.CacheTTL,
		})
	}
	if err := saveLocked(st.specFile.file, rows); err != nil {
		return err
	}
	subs := make(map[string][]trustMarkSubjectFileRow, len(st.specs))
	for i := range st.specs {
		spec := &st.specs[i]
		for _, sub := range st.subjects[spec.ID] {
			subs[spec.TrustMarkType] = append(subs[spec.TrustMarkType], trustMarkSubjectFileRow{
				EntityID:         sub.EntityID,
				Status:           sub.Status,
				Description:      sub.Description,
				AdditionalClaims: sub.AdditionalClaims,
			})
		}
	}
	return saveLocked(st.subjectFile.file, subs)
}

func (st *trustMarkState) findSpecByID(id uint) *trustMarkSpecRecord {
	for i := range st.specs {
		if st.specs[i].ID == id {
			return &st.specs[i]
		}
	}
	return nil
}

func (st *trustMarkState) findSpecByIdent(ident string) *trustMarkSpecRecord {
	if id, err := strconv.ParseUint(ident, 10, 64); err == nil {
		if spec := st.findSpecByID(uint(id)); spec != nil {
			return spec
		}
	}
	for i := range st.specs {
		if st.specs[i].TrustMarkType == ident {
			return &st.specs[i]
		}
	}
	return nil
}

func (st *trustMarkState) findSubjectByEntity(specID uint, entityID string) *model.TrustMarkSubject {
	for _, sub := range st.subjects[specID] {
		if sub.EntityID == entityID {
			return sub
		}
	}
	return nil
}
