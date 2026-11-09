package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lighthouse"
	"github.com/go-oidfed/lighthouse-light/internal/migration"
	"github.com/zachmann/go-utils/duration"
	"gopkg.in/yaml.v3"
)

// GeneralMetadata is the general (entity-level) subordinate metadata. The
// value is persisted as oidfed.Metadata, whose entity-type keys (e.g.
// openid_provider) are JSON-tagged only; yaml.v3 does not honor those tags, so
// the YAML mapping is decoded generically and re-marshaled through JSON to
// populate the typed Metadata fields.
type GeneralMetadata struct {
	oidfed.Metadata
}

// UnmarshalYAML decodes the metadata YAML mapping into the embedded
// oidfed.Metadata via a JSON round-trip (YAML keys match the JSON tags).
func (m *GeneralMetadata) UnmarshalYAML(node *yaml.Node) error {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &m.Metadata)
}

// Config is the lighthouse-light configuration. The config file carries
// operational settings (server, signing, endpoints) and the general
// (entity-level) federation settings; all federation CONTENT (subordinates,
// trust anchors, authority hints, trust mark specs/subjects, published trust
// marks, issuers/owners, extra entity claims) lives exclusively in the data
// dir files — see docs/data-directory.md. The config is never merged into the
// data files as upserts: removing a key from the config removes it from the
// effective state on the next boot.
//
// NOTE: Signing has no kms / pk_backend / pkcs11 fields: lighthouse-light
// hardcodes KMS=filesystem and PKBackend=filesystem (user decision).
type Config struct {
	EntityID       string                `yaml:"entity_id"` // required
	DataDir        string                `yaml:"data_dir"`  // default "/data"; ALL persisted state lives here
	Logging        LoggingConf           `yaml:"logging"`
	Server         lighthouse.ServerConf `yaml:"server"` // incl. native TLS (server.tls)
	Signing        SigningConf           `yaml:"signing"`
	FederationData FederationDataConf    `yaml:"federation_data"`
	Endpoints      EndpointsConf         `yaml:"endpoints"`

	baseDir string // config file dir; NOT serialized
}

// LoggingConf controls the application log output.
type LoggingConf struct {
	Level  string `yaml:"level"`  // default "info"
	Format string `yaml:"format"` // "json" (default) or "console"
}

// SigningConf has NO kms/pk_backend/pkcs11 fields — fixed to filesystem in
// code (user decision). KV-managed alg/rotation values are included so the
// seed can write them exactly like config2db does.
type SigningConf struct {
	AutoGenerateKeys  bool `yaml:"auto_generate_keys"` // default true
	FileSystemBackend struct {
		KeyFile string `yaml:"key_file"`
		KeyDir  string `yaml:"key_dir"`
	} `yaml:"filesystem"`
	Alg                  string                   `yaml:"alg"`         // → KV signing.alg (default ES512 via lazy KV)
	RSAKeyLen            int                      `yaml:"rsa_key_len"` // → KV signing.rsa_key_len (default 2048)
	KeyRotation          KeyRotationConf          `yaml:"key_rotation"`
	AutomaticKeyRollover AutomaticKeyRolloverConf `yaml:"automatic_key_rollover"`
}

// KeyRotationConf mirrors migration.SigningConf.KeyRotation exactly; the seed
// writes the same KV values config2db writes.
type KeyRotationConf struct {
	Enabled                             bool                    `yaml:"enabled"`
	Interval                            duration.DurationOption `yaml:"interval"`
	Overlap                             duration.DurationOption `yaml:"overlap"`
	KeyAnnouncementLeadTime             duration.DurationOption `yaml:"key_announcement_lead_time"`
	KeyAnnouncementLeadTimeECMultiplier float64                 `yaml:"key_announcement_lead_time_ec_multiplier"`
}

// AutomaticKeyRolloverConf mirrors migration.SigningConf.AutomaticKeyRollover.
type AutomaticKeyRolloverConf struct {
	Enabled  bool                    `yaml:"enabled"`
	Interval duration.DurationOption `yaml:"interval"`
}

// FederationDataConf holds the general (entity-level) federation settings.
// These are applied to the in-memory KV store as a config overlay at boot and
// are never persisted to disk; removing a section removes its effect.
// Content collections (authority hints, trust anchors, trust marks, issuers,
// owners) live in the data dir files, not here. Entity-configuration extra
// claims are seeded from config into the in-memory additional claims store
// (see ExtraEntityConfigurationClaims).
type FederationDataConf struct {
	SubordinateMetadata            *GeneralMetadata                 `yaml:"metadata"` // general (entity-level) subordinate metadata
	Constraints                    *oidfed.ConstraintSpecification  `yaml:"constraints"`
	MetadataPolicyCrit             []oidfed.PolicyOperatorName      `yaml:"metadata_policy_crit"`
	MetadataPolicyFile             string                           `yaml:"metadata_policy_file"` // JSON oidfed.MetadataPolicies; general (entity) level
	ConfigurationLifetime          duration.DurationOption          `yaml:"configuration_lifetime"`
	Metadata                       migration.FederationMetadataConf `yaml:"federation_entity_metadata"`
	SubordinateAdditionalClaims    []SubordinateAdditionalClaimConf `yaml:"subordinate_additional_claims"`     // general fallback claims
	ExtraEntityConfigurationClaims []SubordinateAdditionalClaimConf `yaml:"extra_entity_configuration_claims"` // entity-configuration extra claims
}

// SubordinateAdditionalClaimConf is one general (entity-level) subordinate
// additional claim, applied as fallback to all subordinates without their own
// claims.
type SubordinateAdditionalClaimConf struct {
	Claim string `yaml:"claim"`
	Value any    `yaml:"value"`
	Crit  bool   `yaml:"crit"`
}

// Defaults.
const (
	DefaultDataDir           = "/data"
	DefaultLogLevel          = "info"
	DefaultLogFormat         = "json"
	DefaultIPListen          = "0.0.0.0"
	DefaultPort              = 7672
	DefaultForwardedIPHeader = "X-Forwarded-For"
)

// DefaultConfig returns a Config populated with the lighthouse-light defaults.
func DefaultConfig() *Config {
	return &Config{
		DataDir: DefaultDataDir,
		Logging: LoggingConf{
			Level:  DefaultLogLevel,
			Format: DefaultLogFormat,
		},
		Server: lighthouse.ServerConf{
			IPListen:          DefaultIPListen,
			Port:              DefaultPort,
			ForwardedIPHeader: DefaultForwardedIPHeader,
		},
		Signing: SigningConf{
			AutoGenerateKeys: true,
		},
	}
}

// possibleConfigLocations mirrors lighthouse cmd/lighthouse/config/config.go.
var possibleConfigLocations = []string{
	".",
	"config",
	"/config",
	"/lighthouse/config",
	"/lighthouse",
	"/data/config",
	"/data",
	"/etc/lighthouse",
}

// Load resolves the config file (explicit arg > $LH_CONFIG_FILE >
// discovery), applies defaults storage and validates it.
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("LH_CONFIG_FILE")
	}

	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}

	c := DefaultConfig()
	if resolvedPath != "" {
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			return nil, err
		}
		// KnownFields(true) fails fast on unknown keys: content sections
		// that moved to the data files (subordinates, authority_hints,
		// trust_anchors, trust_mark_*...) error instead of silently
		// being ignored.
		dec := yaml.NewDecoder(strings.NewReader(string(content)))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil {
			return nil, fmt.Errorf("failed to parse config file %q: %w", resolvedPath, err)
		}
	}

	abs, err := filepath.Abs(resolvedPath)
	if err != nil {
		return nil, err
	}
	c.baseDir = filepath.Dir(abs)

	// server.tls.cert/key are paths; resolve them against the config dir like
	// the other relative path options. (lighthouse.ServerConf.TLS has an
	// unexported type, but its fields are exported and settable directly.)
	if c.Server.TLS.Cert != "" {
		c.Server.TLS.Cert = c.Resolve(c.Server.TLS.Cert)
	}
	if c.Server.TLS.Key != "" {
		c.Server.TLS.Key = c.Resolve(c.Server.TLS.Key)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func resolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	for _, dir := range possibleConfigLocations {
		candidate := filepath.Join(dir, "config.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no config file found (use the first CLI arg, $LH_CONFIG_FILE, or one of: %s)", strings.Join(possibleConfigLocations, ", "))
}

// Resolve resolves a possibly-relative path against the config file's
// directory. Absolute paths are returned unchanged.
func (c *Config) Resolve(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.baseDir, p)
}

// BaseDir returns the config file directory used for relative-path resolution.
func (c *Config) BaseDir() string { return c.baseDir }

// Validate checks the config for errors. Any error names the offending field.
func (c *Config) Validate() error {
	if c.EntityID == "" {
		return fmt.Errorf("entity_id is required")
	}
	if c.Signing.FileSystemBackend.KeyFile == "" && c.Signing.FileSystemBackend.KeyDir == "" {
		return fmt.Errorf("signing.filesystem: key_file or key_dir must be set")
	}
	if c.Server.TLS.Enabled {
		if c.Server.TLS.Cert == "" || c.Server.TLS.Key == "" {
			return fmt.Errorf("server.tls: cert and key are required when enabled")
		}
	}
	if c.Server.AdminTLS.Enabled {
		return fmt.Errorf("server.admin_tls is not supported (there is no admin API)")
	}

	// Key rotation with an interval below the entity-configuration lifetime is
	// a configuration error: rotated-out keys expire while ECs/statements
	// signed with them are still valid, and (with the default announcement
	// lead time) the rotation loop degenerates into generating keys in a tight
	// loop, unboundedly growing the key dir and the published JWKS.
	for name, interval := range map[string]duration.DurationOption{
		"signing.key_rotation.interval":           c.Signing.KeyRotation.Interval,
		"signing.automatic_key_rollover.interval": c.Signing.AutomaticKeyRollover.Interval,
	} {
		d := interval.Duration()
		if d <= 0 {
			continue // rotation disabled
		}
		lifetime := c.FederationData.ConfigurationLifetime.Duration()
		if lifetime > 0 && d < lifetime {
			return fmt.Errorf(
				"%s (%s) is smaller than federation_data.configuration_lifetime (%s): "+
					"rotated keys expire while entity configurations and statements signed with them are still valid "+
					"(see docs/signing.md)",
				name, d, lifetime,
			)
		}
	}

	// Endpoint auth pre-check: an auth-enabled endpoint needs auth trust
	// anchors (per-endpoint or global). The anchors themselves must exist in
	// trust_anchors.json; that is checked at boot by
	// backends.CheckReferencedTrustAnchors.
	globalTAs := c.Endpoints.Auth.TrustAnchors
	checkAuth := func(name string, epAuthTAs []string, epAuthEnabled bool) error {
		enabled := epAuthEnabled || c.Endpoints.Auth.AllRequireAuth
		if !enabled {
			return nil
		}
		if len(epAuthTAs) == 0 && len(globalTAs) == 0 {
			return fmt.Errorf("endpoint %s: auth_enabled requires auth_trust_anchors or endpoints.auth.trust_anchors", name)
		}
		return nil
	}
	for name, ep := range map[string]struct {
		authTAs  []string
		authFlag bool
	}{
		"fetch":               {c.Endpoints.Fetch.AuthTrustAnchors, c.Endpoints.Fetch.AuthEnabled},
		"list":                {c.Endpoints.List.AuthTrustAnchors, c.Endpoints.List.AuthEnabled},
		"resolve":             {c.Endpoints.Resolve.AuthTrustAnchors, c.Endpoints.Resolve.AuthEnabled},
		"trust_mark_status":   {c.Endpoints.TrustMarkStatus.AuthTrustAnchors, c.Endpoints.TrustMarkStatus.AuthEnabled},
		"trust_mark_list":     {c.Endpoints.TrustMarkList.AuthTrustAnchors, c.Endpoints.TrustMarkList.AuthEnabled},
		"trust_mark":          {c.Endpoints.TrustMark.AuthTrustAnchors, c.Endpoints.TrustMark.AuthEnabled},
		"historical_keys":     {c.Endpoints.HistoricalKeys.AuthTrustAnchors, c.Endpoints.HistoricalKeys.AuthEnabled},
		"enroll":              {c.Endpoints.Enroll.AuthTrustAnchors, c.Endpoints.Enroll.AuthEnabled},
		"enroll_request":      {c.Endpoints.EnrollRequest.AuthTrustAnchors, c.Endpoints.EnrollRequest.AuthEnabled},
		"trust_mark_request":  {c.Endpoints.TrustMarkRequest.AuthTrustAnchors, c.Endpoints.TrustMarkRequest.AuthEnabled},
		"entity_collection":   {c.Endpoints.EntityCollection.AuthTrustAnchors, c.Endpoints.EntityCollection.AuthEnabled},
		"jwks_update":         {c.Endpoints.JWKSUpdate.AuthTrustAnchors, c.Endpoints.JWKSUpdate.AuthEnabled},
		"jwks_update_trigger": {c.Endpoints.JWKSUpdateTrigger.AuthTrustAnchors, c.Endpoints.JWKSUpdateTrigger.AuthEnabled},
	} {
		if err := checkAuth(name, ep.authTAs, ep.authFlag); err != nil {
			return err
		}
	}

	return nil
}
