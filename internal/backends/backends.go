// Package backends implements the lighthouse storage model interfaces on top
// of plain JSON files in a data directory. Every store is a JSON file; all
// mutations are written atomically (tmp file + rename). lighthouse-light is
// single-process: one instance per data dir, no cross-process locking.
//
// The data files are the single source of truth for federation content
// (subordinates, trust anchors, authority hints, trust mark specs/subjects,
// published trust marks, issuers/owners, instances, JTI). The config file
// carries operational settings and the general (entity-level) settings; those
// are surfaced to lighthouse through the in-memory KV store (and the
// config-seeded entity configuration additional claims) and are never written
// to disk. IDs are content-addressed (hashID), so the files contain no
// numeric ids.
package backends

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lib/jwx/keymanagement/kms"
	"github.com/rs/zerolog/log"

	"github.com/go-oidfed/lighthouse-light/internal/config"
	"github.com/go-oidfed/lighthouse-light/internal/migration"
	"github.com/go-oidfed/lighthouse/storage"
	"github.com/go-oidfed/lighthouse/storage/model"
)

// StoreFiles is the canonical list of data-dir files, one per store. The
// trust_mark_specs and trust_mark_subjects stores share the spec/subject
// state: the specs file holds the spec rows, the subjects file holds the
// subject rows.
var StoreFiles = []string{
	"authority_hints.json",
	"trust_mark_types.json",
	"trust_anchors.json",
	"subordinates.json",
	"trust_mark_subjects.json",
	"trust_mark_specs.json",
	"trust_mark_instances.json",
	"published_trust_marks.json",
	"jti.json",
}

// fileState is the shared persistence plumbing of every file-backed store.
type fileState struct {
	mu   sync.RWMutex
	file string
}

// newFileState creates a fileState for the given data-dir file.
func newFileState(dataDir, name string) *fileState {
	return &fileState{file: filepath.Join(dataDir, name)}
}

// load reads the file into out. A missing file yields a zero store (nil, nil).
// A parse error is fatal: the file is never silently wiped.
func (s *fileState) load(out any) error {
	content, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store %s: failed to read %s: %w", filepath.Base(s.file), s.file, err)
	}
	if len(content) == 0 {
		return nil
	}
	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf(
			"store %s: failed to parse %s (fix or remove the file to reset): %w",
			filepath.Base(s.file), s.file, err,
		)
	}
	return nil
}

// save writes v to the file atomically (tmp + rename).
func (s *fileState) save(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return saveLocked(s.file, v)
}

func saveLocked(file string, v any) error {
	content, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("store %s: failed to marshal: %w", filepath.Base(file), err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("store %s: failed to write %s: %w", filepath.Base(file), tmp, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("store %s: failed to rename %s: %w", filepath.Base(file), tmp, err)
	}
	return nil
}

// materializeMissing writes v to the file when it does not exist yet, so the
// full set of store files is present after the first boot (scriptable
// provisioning: operators can pre-write these files before first start).
// Existing files are never touched.
func materializeMissing(fs *fileState, v any) error {
	if _, err := os.Stat(fs.file); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("store %s: failed to stat %s: %w", filepath.Base(fs.file), fs.file, err)
	}
	return saveLocked(fs.file, v)
}

// Build constructs all file-backed stores under dataDir and returns the
// model.Backends struct. cfg (optional) supplies the config-owned KV values,
// which are applied as a read-only overlay (see applyConfigValues). Stores
// are constructed in dependency order: KV first (the subordinates store holds
// a KV reference for the general-claims fallback). Any load error aborts
// (missing files are fine, corrupt or legacy-format files are not — see
// fileState.load and rejectLegacyIDs). The DB-backed stores are left nil.
func Build(dataDir string, cfg *config.Config) (model.Backends, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return model.Backends{}, fmt.Errorf("backends: failed to create data dir %q: %w", dataDir, err)
	}

	kv := newKVStore()
	if err := applyConfigValues(kv, cfg); err != nil {
		return model.Backends{}, err
	}

	authorityHints, err := newAuthorityHintsStore(newFileState(dataDir, "authority_hints.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(authorityHints.fileState, []authorityHintRow{}); err != nil {
		return model.Backends{}, err
	}

	trustMarkTypes, err := newTrustMarkTypesStore(newFileState(dataDir, "trust_mark_types.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(trustMarkTypes.fileState, []trustMarkTypeFileRow{}); err != nil {
		return model.Backends{}, err
	}

	trustAnchors, err := newTrustAnchorsStore(newFileState(dataDir, "trust_anchors.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(trustAnchors.fileState, []trustAnchorRow{}); err != nil {
		return model.Backends{}, err
	}

	// Specs and subjects share one state object; the specs store persists
	// specs, the subjects store persists the subjects.
	shared, err := newTrustMarkState(
		newFileState(dataDir, "trust_mark_specs.json"),
		newFileState(dataDir, "trust_mark_subjects.json"),
	)
	if err != nil {
		return model.Backends{}, err
	}
	if err := shared.materializeMissing(); err != nil {
		return model.Backends{}, err
	}

	subordinates, err := newSubordinatesStore(
		newFileState(dataDir, "subordinates.json"), kv,
	)
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(subordinates.fileState, []subordinateRecord{}); err != nil {
		return model.Backends{}, err
	}

	instances, err := newTrustMarkInstancesStore(newFileState(dataDir, "trust_mark_instances.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(instances.fileState, []model.IssuedTrustMarkInstance{}); err != nil {
		return model.Backends{}, err
	}
	instances.SetState(shared)
	specsStore := newTrustMarkSpecsStore(shared)
	specsStore.SetInstances(instances)

	published, err := newPublishedTrustMarksStore(newFileState(dataDir, "published_trust_marks.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(published.fileState, []publishedTrustMarkRow{}); err != nil {
		return model.Backends{}, err
	}

	additionalClaims, err := newAdditionalClaimsStore(additionalClaimsFromConfig(cfg))
	if err != nil {
		return model.Backends{}, err
	}

	jti, err := newJTIStore(newFileState(dataDir, "jti.json"))
	if err != nil {
		return model.Backends{}, err
	}
	if err := materializeMissing(jti.fileState, jti.rows); err != nil {
		return model.Backends{}, err
	}

	return model.Backends{
		Subordinates:        subordinates,
		TrustMarks:          newTrustMarksStore(shared),
		TrustMarkSpecs:      specsStore,
		TrustMarkInstances:  instances,
		AuthorityHints:      authorityHints,
		TrustMarkTypes:      trustMarkTypes,
		AdditionalClaims:    additionalClaims,
		PublishedTrustMarks: published,
		TrustAnchors:        trustAnchors,
		KV:                  kv,
		JTI:                 jti,
		// DB, Users, Stats, SubordinateEvents, PKStorages, Transaction: nil
	}, nil
}

// generalClaimRecord is the persisted/KV shape of general (entity-level)
// subordinate additional claims (mirrors the admin API payload struct minus
// the numeric id).
type generalClaimRecord struct {
	Claim string `json:"claim"`
	Value any    `json:"value"`
	Crit  bool   `json:"crit"`
}

// applyConfigValues overlays the config-owned settings onto the KV store. The
// KV store is in-memory: the config file is the single source of truth for
// these values and nothing is ever written to disk, so removing a key from
// the config removes it from the effective KV state on the next boot. The
// exact storage helpers lighthouse uses are called so the encodings match the
// DB-format expectations of the consumers.
func applyConfigValues(kv *KVStore, cfg *config.Config) error {
	if kv == nil || cfg == nil {
		return nil
	}

	// 1. signing.alg
	if cfg.Signing.Alg != "" {
		if err := storage.SetSigningAlg(kv, storage.SigningAlgWithNbf{SigningAlg: cfg.Signing.Alg}); err != nil {
			return fmt.Errorf("config signing.alg: %w", err)
		}
	}

	// 2. signing.rsa_key_len
	if cfg.Signing.RSAKeyLen > 0 {
		if err := storage.SetRSAKeyLen(kv, cfg.Signing.RSAKeyLen); err != nil {
			return fmt.Errorf("config signing.rsa_key_len: %w", err)
		}
	}

	// 3. signing.key_rotation (legacy automatic_key_rollover folded in, same
	// as config2db migrateKeyRotation).
	var rotationConfig kms.KeyRotationConfig
	hasConfig := false
	if cfg.Signing.KeyRotation.Interval.Duration() > 0 {
		rotationConfig.Enabled = cfg.Signing.KeyRotation.Enabled
		rotationConfig.Interval = cfg.Signing.KeyRotation.Interval
		rotationConfig.Overlap = cfg.Signing.KeyRotation.Overlap
		rotationConfig.KeyAnnouncementLeadTime = cfg.Signing.KeyRotation.KeyAnnouncementLeadTime
		rotationConfig.KeyAnnouncementLeadTimeECMultiplier = cfg.Signing.KeyRotation.KeyAnnouncementLeadTimeECMultiplier
		hasConfig = true
	} else if cfg.Signing.AutomaticKeyRollover.Interval.Duration() > 0 {
		rotationConfig.Enabled = cfg.Signing.AutomaticKeyRollover.Enabled
		rotationConfig.Interval = cfg.Signing.AutomaticKeyRollover.Interval
		hasConfig = true
	}
	if hasConfig {
		if err := storage.SetKeyRotation(kv, rotationConfig); err != nil {
			return fmt.Errorf("config signing.key_rotation: %w", err)
		}
	}

	// 4. federation_data.metadata (general, entity-level subordinate metadata)
	if cfg.FederationData.SubordinateMetadata != nil {
		if err := kv.SetAny(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyMetadata,
			&cfg.FederationData.SubordinateMetadata.Metadata); err != nil {
			return fmt.Errorf("config federation_data.metadata: %w", err)
		}
	}

	// 5. federation_data.constraints (general, entity-level)
	if cfg.FederationData.Constraints != nil {
		if err := storage.SetConstraints(kv, cfg.FederationData.Constraints); err != nil {
			return fmt.Errorf("config federation_data.constraints: %w", err)
		}
	}

	// 6. federation_data.metadata_policy_crit (general critical operators)
	if len(cfg.FederationData.MetadataPolicyCrit) > 0 {
		if err := storage.SetMetadataPolicyCrit(kv, cfg.FederationData.MetadataPolicyCrit); err != nil {
			return fmt.Errorf("config federation_data.metadata_policy_crit: %w", err)
		}
	}

	// 7. federation_data.metadata_policy_file (general/entity-level policies).
	// The config is authoritative; a missing file is skipped with a warning
	// (a fresh install without a policy file must boot).
	if cfg.FederationData.MetadataPolicyFile != "" {
		path := cfg.Resolve(cfg.FederationData.MetadataPolicyFile)
		content, err := os.ReadFile(path)
		if err != nil {
			log.Warn().Err(err).Msgf("config: skipping federation_data.metadata_policy_file %q: not readable", path)
		} else {
			var metadataPolicy oidfed.MetadataPolicies
			if err := json.Unmarshal(content, &metadataPolicy); err != nil {
				return fmt.Errorf("config federation_data.metadata_policy_file: failed to parse %q: %w", path, err)
			}
			if err := kv.SetAny(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyMetadataPolicy, &metadataPolicy); err != nil {
				return fmt.Errorf("config federation_data.metadata_policy_file: %w", err)
			}
		}
	}

	// 8. federation_data.configuration_lifetime
	if cfg.FederationData.ConfigurationLifetime.Duration() > 0 {
		if err := storage.SetEntityConfigurationLifetime(kv, cfg.FederationData.ConfigurationLifetime.Duration()); err != nil {
			return fmt.Errorf("config federation_data.configuration_lifetime: %w", err)
		}
	}

	// 9. endpoints.fetch.statement_lifetime
	if cfg.Endpoints.Fetch.StatementLifetime.Duration() > 0 {
		seconds := int(cfg.Endpoints.Fetch.StatementLifetime.Duration().Seconds())
		if err := kv.SetAny(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyLifetime, seconds); err != nil {
			return fmt.Errorf("config endpoints.fetch.statement_lifetime: %w", err)
		}
	}

	// 10. federation_data.federation_entity_metadata
	if metadata := cfg.FederationData.Metadata.ToOIDFedMetadata(); metadata != nil {
		if err := storage.SetMetadata(kv, metadata); err != nil {
			return fmt.Errorf("config federation_data.federation_entity_metadata: %w", err)
		}
	}

	// 11. federation_data.subordinate_additional_claims (general fallback
	// claims; shape mirrors the admin API storage minus the numeric id).
	claims := make([]generalClaimRecord, 0, len(cfg.FederationData.SubordinateAdditionalClaims))
	for _, c := range cfg.FederationData.SubordinateAdditionalClaims {
		claims = append(claims, generalClaimRecord{Claim: c.Claim, Value: c.Value, Crit: c.Crit})
	}
	if err := kv.SetAny(model.KeyValueScopeSubordinateStatement, model.KeyValueKeyAdditionalClaims, claims); err != nil {
		return fmt.Errorf("config federation_data.subordinate_additional_claims: %w", err)
	}

	return nil
}

// additionalClaimsFromConfig converts the config's
// federation_data.extra_entity_configuration_claims (list of
// {claim, value, crit}) into the model rows seeding the in-memory entity
// configuration additional claims store. Order is preserved.
func additionalClaimsFromConfig(cfg *config.Config) []model.AddAdditionalClaim {
	if cfg == nil {
		return nil
	}
	in := cfg.FederationData.ExtraEntityConfigurationClaims
	out := make([]model.AddAdditionalClaim, 0, len(in))
	for _, c := range in {
		out = append(out, model.AddAdditionalClaim{Claim: c.Claim, Value: c.Value, Crit: c.Crit})
	}
	return out
}

// CheckReferencedTrustAnchors fails fast when an endpoint references a trust
// anchor entity ID that is not defined in trust_anchors.json. Trust anchors
// themselves live only in the data file; config references must resolve at
// boot (auth/allowed-lists would otherwise fail obscurely at request time).
func CheckReferencedTrustAnchors(cfg *config.Config, backs model.Backends) error {
	if backs.TrustAnchors == nil {
		return nil
	}
	list, err := backs.TrustAnchors.List()
	if err != nil {
		return fmt.Errorf("trust_anchors: list: %w", err)
	}
	defined := make(map[string]bool, len(list))
	for i := range list {
		defined[list[i].EntityID] = true
	}
	missing := func(location string, ids []string) error {
		for _, id := range ids {
			if !defined[id] {
				return fmt.Errorf("%s: trust anchor %q is not defined in trust_anchors.json", location, id)
			}
		}
		return nil
	}
	refs := map[string][]string{
		"endpoints.auth.trust_anchors":                      cfg.Endpoints.Auth.TrustAnchors,
		"endpoints.fetch.auth_trust_anchors":                cfg.Endpoints.Fetch.AuthTrustAnchors,
		"endpoints.list.auth_trust_anchors":                 cfg.Endpoints.List.AuthTrustAnchors,
		"endpoints.resolve.auth_trust_anchors":              cfg.Endpoints.Resolve.AuthTrustAnchors,
		"endpoints.resolve.allowed_trust_anchors":           cfg.Endpoints.Resolve.AllowedTrustAnchors,
		"endpoints.trust_mark_status.auth_trust_anchors":    cfg.Endpoints.TrustMarkStatus.AuthTrustAnchors,
		"endpoints.trust_mark_list.auth_trust_anchors":      cfg.Endpoints.TrustMarkList.AuthTrustAnchors,
		"endpoints.trust_mark.auth_trust_anchors":           cfg.Endpoints.TrustMark.AuthTrustAnchors,
		"endpoints.historical_keys.auth_trust_anchors":      cfg.Endpoints.HistoricalKeys.AuthTrustAnchors,
		"endpoints.enroll.auth_trust_anchors":               cfg.Endpoints.Enroll.AuthTrustAnchors,
		"endpoints.enroll_request.auth_trust_anchors":       cfg.Endpoints.EnrollRequest.AuthTrustAnchors,
		"endpoints.trust_mark_request.auth_trust_anchors":   cfg.Endpoints.TrustMarkRequest.AuthTrustAnchors,
		"endpoints.entity_collection.auth_trust_anchors":    cfg.Endpoints.EntityCollection.AuthTrustAnchors,
		"endpoints.entity_collection.allowed_trust_anchors": cfg.Endpoints.EntityCollection.AllowedTrustAnchors,
		"endpoints.jwks_update.auth_trust_anchors":          cfg.Endpoints.JWKSUpdate.AuthTrustAnchors,
		"endpoints.jwks_update_trigger.auth_trust_anchors":  cfg.Endpoints.JWKSUpdateTrigger.AuthTrustAnchors,
	}
	for location, ids := range refs {
		if err := missing(location, ids); err != nil {
			return err
		}
	}
	if cfg.Endpoints.Enroll.Checker.Kind != 0 {
		if err := missing("endpoints.enroll.checker.trust_anchors",
			migration.ExtractCheckerTrustAnchorIDs(&cfg.Endpoints.Enroll.Checker)); err != nil {
			return err
		}
	}
	return nil
}
