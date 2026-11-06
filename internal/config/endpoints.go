package config

import (
	"github.com/go-oidfed/lighthouse-light/internal/migration"
	"github.com/zachmann/go-utils/duration"
)

// EndpointsConf holds the configuration for all federation endpoints. It
// mirrors migration.EndpointsConf and adds the current lighthouse additions:
// jwks_update / jwks_update_trigger and the entity collection's
// disable_delegated_collection flag. Trust mark specs live in
// trust_mark_specs.json (data dir), not here.
type EndpointsConf struct {
	Fetch             FetchEndpointConf            `yaml:"fetch"`
	List              migration.SimpleEndpointConf `yaml:"list"`
	Resolve           ResolveEndpointConf          `yaml:"resolve"`
	TrustMarkStatus   migration.SimpleEndpointConf `yaml:"trust_mark_status"`
	TrustMarkList     migration.SimpleEndpointConf `yaml:"trust_mark_list"`
	TrustMark         TrustMarkEndpointConf        `yaml:"trust_mark"`
	HistoricalKeys    migration.SimpleEndpointConf `yaml:"historical_keys"`
	Enroll            migration.EnrollEndpointConf `yaml:"enroll"`
	EnrollRequest     migration.SimpleEndpointConf `yaml:"enroll_request"`
	TrustMarkRequest  migration.SimpleEndpointConf `yaml:"trust_mark_request"`
	EntityCollection  CollectionEndpointConf       `yaml:"entity_collection"`
	JWKSUpdate        migration.SimpleEndpointConf `yaml:"jwks_update"`
	JWKSUpdateTrigger migration.SimpleEndpointConf `yaml:"jwks_update_trigger"`
	Auth              AuthConf                     `yaml:"auth"`
}

// AuthConf holds global endpoint authentication defaults. Trust anchors are
// referenced by entity ID only; their definitions live in trust_anchors.json
// (checked at boot by backends.CheckReferencedTrustAnchors).
type AuthConf struct {
	AllRequireAuth bool     `yaml:"all_require_auth"`
	TrustAnchors   []string `yaml:"trust_anchors"`
}

// FetchEndpointConf is the fetch endpoint configuration.
type FetchEndpointConf struct {
	Path              string                  `yaml:"path"`
	URL               string                  `yaml:"url"`
	AuthEnabled       bool                    `yaml:"auth_enabled"`
	AuthTrustAnchors  []string                `yaml:"auth_trust_anchors"`
	StatementLifetime duration.DurationOption `yaml:"statement_lifetime"`
}

// ResolveEndpointConf is the resolve endpoint configuration.
type ResolveEndpointConf struct {
	Path                                   string                  `yaml:"path"`
	URL                                    string                  `yaml:"url"`
	AuthEnabled                            bool                    `yaml:"auth_enabled"`
	AuthTrustAnchors                       []string                `yaml:"auth_trust_anchors"`
	AllowedTrustAnchors                    []string                `yaml:"allowed_trust_anchors"`
	UseEntityCollectionAllowedTrustAnchors bool                    `yaml:"use_entity_collection_allowed_trust_anchors"`
	GracePeriod                            duration.DurationOption `yaml:"grace_period"`
	TimeElapsedGraceFactor                 float64                 `yaml:"time_elapsed_grace_factor"`
	ProactiveResolver                      ProactiveResolverConf   `yaml:"proactive_resolver"`
}

// ProactiveResolverConf is the proactive resolver sub-configuration.
type ProactiveResolverConf struct {
	Enabled          bool `yaml:"enabled"`
	ConcurrencyLimit int  `yaml:"concurrency_limit"`
	QueueSize        int  `yaml:"queue_size"`
	ResponseStorage  struct {
		Dir       string `yaml:"dir"`
		StoreJSON bool   `yaml:"store_json"`
		StoreJWT  bool   `yaml:"store_jwt"`
	} `yaml:"response_storage"`
}

// CollectionEndpointConf is the entity collection endpoint configuration. It
// reuses migration.CollectionEndpointConf and adds
// disable_delegated_collection (light addition, maps to
// model.CollectionDBConfigJSON.DisableDelegatedCollection).
type CollectionEndpointConf struct {
	migration.CollectionEndpointConf `yaml:",inline"`
	// DisableDelegatedCollection disables the subordinate collection endpoints
	// advertised in the entity collection metadata.
	DisableDelegatedCollection bool `yaml:"disable_delegated_collection"`
}

// TrustMarkEndpointConf is the trust mark endpoint configuration: the route
// and auth settings only. Trust mark specs (lifetime, eligibility, cache_ttl)
// are provisioned in trust_mark_specs.json (data dir).
type TrustMarkEndpointConf struct {
	Path             string   `yaml:"path"`
	URL              string   `yaml:"url"`
	AuthEnabled      bool     `yaml:"auth_enabled"`
	AuthTrustAnchors []string `yaml:"auth_trust_anchors"`
}
