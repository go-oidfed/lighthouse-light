// Package endpoints registers the configured federation endpoints on a
// lighthouse.LightHouse, mirroring how lighthouse's config2db + endpoint
// registry load endpoints from the database.
package endpoints

import (
	"fmt"
	"strings"
	"time"

	oidfed "github.com/go-oidfed/lib"

	"github.com/go-oidfed/lighthouse"
	"github.com/go-oidfed/lighthouse-light/internal/config"
	"github.com/go-oidfed/lighthouse-light/internal/migration"
	"github.com/go-oidfed/lighthouse/storage/model"
)

// Register registers every configured endpoint on lh. Endpoints whose path
// and URL are both empty are skipped (lighthouse parity). Errors wrap the
// endpoint type. Background services started for endpoints (proactive
// resolver) return stop functions the caller must invoke on shutdown (config
// restart for Step 6 wiring).
func Register(lh *lighthouse.LightHouse, cfg *config.Config, backs model.Backends) ([]func(), error) {
	var stops []func()
	globalTAIDs := cfg.Endpoints.Auth.TrustAnchors
	authEnabled := func(flag bool) bool { return flag || cfg.Endpoints.Auth.AllRequireAuth }
	authTAs := func(epTAs []string) []string {
		if len(epTAs) > 0 {
			return epTAs
		}
		return globalTAIDs
	}
	endpointConf := func(path, url string, flag bool, epTAs []string) lighthouse.EndpointConf {
		conf := lighthouse.EndpointConf{
			Path:        path,
			URL:         url,
			AuthEnabled: authEnabled(flag),
		}
		if conf.AuthEnabled {
			conf.AuthTrustAnchors = authTAs(epTAs)
		} else {
			conf.AuthTrustAnchors = epTAs
		}
		return conf
	}
	skip := func(path, url string) bool { return path == "" && url == "" }

	// fetch
	if !skip(cfg.Endpoints.Fetch.Path, cfg.Endpoints.Fetch.URL) {
		if err := lh.AddFetchEndpoint(
			endpointConf(cfg.Endpoints.Fetch.Path, cfg.Endpoints.Fetch.URL, cfg.Endpoints.Fetch.AuthEnabled, cfg.Endpoints.Fetch.AuthTrustAnchors),
			backs.Subordinates,
		); err != nil {
			return stops, fmt.Errorf("endpoints.fetch: %w", err)
		}
	}

	// list
	if !skip(cfg.Endpoints.List.Path, cfg.Endpoints.List.URL) {
		if err := lh.AddSubordinateListingEndpoint(
			endpointConf(cfg.Endpoints.List.Path, cfg.Endpoints.List.URL, cfg.Endpoints.List.AuthEnabled, cfg.Endpoints.List.AuthTrustAnchors),
			backs.Subordinates, backs.TrustMarks,
		); err != nil {
			return stops, fmt.Errorf("endpoints.list: %w", err)
		}
	}

	// resolve
	if !skip(cfg.Endpoints.Resolve.Path, cfg.Endpoints.Resolve.URL) {
		allowedTAs := cfg.Endpoints.Resolve.AllowedTrustAnchors
		if cfg.Endpoints.Resolve.UseEntityCollectionAllowedTrustAnchors {
			// static config: mirror the DB lookup against the collection's
			// configured TAs.
			if len(cfg.Endpoints.EntityCollection.AllowedTrustAnchors) > 0 {
				allowedTAs = cfg.Endpoints.EntityCollection.AllowedTrustAnchors
			}
		}
		if cfg.Endpoints.Resolve.GracePeriod.Duration() > 0 {
			oidfed.ResolverCacheGracePeriod = cfg.Endpoints.Resolve.GracePeriod.Duration()
		}
		if cfg.Endpoints.Resolve.TimeElapsedGraceFactor > 0 {
			oidfed.ResolverCacheLifetimeElapsedGraceFactor = cfg.Endpoints.Resolve.TimeElapsedGraceFactor
		}
		var proactive *oidfed.ProactiveResolver
		if cfg.Endpoints.Resolve.ProactiveResolver.Enabled {
			proactive = &oidfed.ProactiveResolver{
				EntityID: lh.FederationEntity.EntityID(),
				Store: oidfed.ResolveStore{
					BaseDir:   cfg.Resolve(cfg.Endpoints.Resolve.ProactiveResolver.ResponseStorage.Dir),
					StoreJWT:  cfg.Endpoints.Resolve.ProactiveResolver.ResponseStorage.StoreJWT,
					StoreJSON: cfg.Endpoints.Resolve.ProactiveResolver.ResponseStorage.StoreJSON,
				},
				Signer:      lh.ResolveResponseSigner(),
				RefreshLead: cfg.Endpoints.Resolve.GracePeriod.Duration(),
				Concurrency: cfg.Endpoints.Resolve.ProactiveResolver.ConcurrencyLimit,
				QueueSize:   cfg.Endpoints.Resolve.ProactiveResolver.QueueSize,
			}
			proactive.Start()
			stops = append(stops, proactive.Stop)
		}
		if err := lh.AddResolveEndpoint(
			endpointConf(cfg.Endpoints.Resolve.Path, cfg.Endpoints.Resolve.URL, cfg.Endpoints.Resolve.AuthEnabled, cfg.Endpoints.Resolve.AuthTrustAnchors),
			allowedTAs, proactive,
		); err != nil {
			return stops, fmt.Errorf("endpoints.resolve: %w", err)
		}
	}

	// trust_mark_status
	if !skip(cfg.Endpoints.TrustMarkStatus.Path, cfg.Endpoints.TrustMarkStatus.URL) {
		if err := lh.AddTrustMarkStatusEndpoint(
			endpointConf(cfg.Endpoints.TrustMarkStatus.Path, cfg.Endpoints.TrustMarkStatus.URL, cfg.Endpoints.TrustMarkStatus.AuthEnabled, cfg.Endpoints.TrustMarkStatus.AuthTrustAnchors),
			lighthouse.TrustMarkStatusConfig{InstanceStore: backs.TrustMarkInstances},
		); err != nil {
			return stops, fmt.Errorf("endpoints.trust_mark_status: %w", err)
		}
	}

	// trust_mark_list
	if !skip(cfg.Endpoints.TrustMarkList.Path, cfg.Endpoints.TrustMarkList.URL) {
		if err := lh.AddTrustMarkedEntitiesListingEndpoint(
			endpointConf(cfg.Endpoints.TrustMarkList.Path, cfg.Endpoints.TrustMarkList.URL, cfg.Endpoints.TrustMarkList.AuthEnabled, cfg.Endpoints.TrustMarkList.AuthTrustAnchors),
			backs.TrustMarkInstances,
		); err != nil {
			return stops, fmt.Errorf("endpoints.trust_mark_list: %w", err)
		}
	}

	// trust_mark
	if !skip(cfg.Endpoints.TrustMark.Path, cfg.Endpoints.TrustMark.URL) {
		eligibilityCache := lighthouse.NewEligibilityCache()
		_ = eligibilityCache.StartCleanupRoutine(5 * time.Minute)
		issuedCache := lighthouse.NewIssuedTrustMarkCache()
		_ = issuedCache.StartCleanupRoutine(5 * time.Minute)
		if err := lh.AddTrustMarkEndpointWithConfig(
			endpointConf(cfg.Endpoints.TrustMark.Path, cfg.Endpoints.TrustMark.URL, cfg.Endpoints.TrustMark.AuthEnabled, cfg.Endpoints.TrustMark.AuthTrustAnchors),
			lighthouse.TrustMarkEndpointConfig{
				Store:                backs.TrustMarks,
				SpecStore:            backs.TrustMarkSpecs,
				InstanceStore:        backs.TrustMarkInstances,
				Cache:                eligibilityCache,
				IssuedTrustMarkCache: issuedCache,
			},
		); err != nil {
			return stops, fmt.Errorf("endpoints.trust_mark: %w", err)
		}
	}

	// trust_mark_request
	if !skip(cfg.Endpoints.TrustMarkRequest.Path, cfg.Endpoints.TrustMarkRequest.URL) {
		if err := lh.AddTrustMarkRequestEndpoint(
			endpointConf(cfg.Endpoints.TrustMarkRequest.Path, cfg.Endpoints.TrustMarkRequest.URL, cfg.Endpoints.TrustMarkRequest.AuthEnabled, cfg.Endpoints.TrustMarkRequest.AuthTrustAnchors),
			backs.TrustMarks,
		); err != nil {
			return stops, fmt.Errorf("endpoints.trust_mark_request: %w", err)
		}
	}

	// historical_keys
	if !skip(cfg.Endpoints.HistoricalKeys.Path, cfg.Endpoints.HistoricalKeys.URL) {
		if err := lh.AddHistoricalKeysEndpoint(
			endpointConf(cfg.Endpoints.HistoricalKeys.Path, cfg.Endpoints.HistoricalKeys.URL, cfg.Endpoints.HistoricalKeys.AuthEnabled, cfg.Endpoints.HistoricalKeys.AuthTrustAnchors),
		); err != nil {
			return stops, fmt.Errorf("endpoints.historical_keys: %w", err)
		}
	}

	// enroll
	if !skip(cfg.Endpoints.Enroll.Path, cfg.Endpoints.Enroll.URL) {
		var checker lighthouse.EntityChecker
		ct, cc := migration.ConvertCheckerConfig(&cfg.Endpoints.Enroll.Checker)
		if ct != "" {
			c, err := lighthouse.EntityCheckerFromJSONConfig(ct, cc)
			if err != nil {
				return stops, fmt.Errorf("endpoints.enroll: failed to create entity checker: %w", err)
			}
			checker = c
		}
		if err := lh.AddEnrollEndpoint(
			endpointConf(cfg.Endpoints.Enroll.Path, cfg.Endpoints.Enroll.URL, cfg.Endpoints.Enroll.AuthEnabled, cfg.Endpoints.Enroll.AuthTrustAnchors),
			backs.Subordinates, checker,
		); err != nil {
			return stops, fmt.Errorf("endpoints.enroll: %w", err)
		}
	}

	// enroll_request
	if !skip(cfg.Endpoints.EnrollRequest.Path, cfg.Endpoints.EnrollRequest.URL) {
		if err := lh.AddEnrollRequestEndpoint(
			endpointConf(cfg.Endpoints.EnrollRequest.Path, cfg.Endpoints.EnrollRequest.URL, cfg.Endpoints.EnrollRequest.AuthEnabled, cfg.Endpoints.EnrollRequest.AuthTrustAnchors),
			backs.Subordinates,
		); err != nil {
			return stops, fmt.Errorf("endpoints.enroll_request: %w", err)
		}
	}

	// entity_collection
	if !skip(cfg.Endpoints.EntityCollection.Path, cfg.Endpoints.EntityCollection.URL) {
		var collector oidfed.EntityCollector = &oidfed.SimpleEntityCollector{
			DisableSubordinateCollectionEndpoints: cfg.Endpoints.EntityCollection.DisableDelegatedCollection,
		}
		if cfg.Endpoints.EntityCollection.Interval.Duration() > 0 {
			pec := &oidfed.PeriodicEntityCollector{
				Collector:    collector,
				TrustAnchors: cfg.Endpoints.EntityCollection.AllowedTrustAnchors,
				Interval:     cfg.Endpoints.EntityCollection.Interval.Duration(),
				Concurrency:  cfg.Endpoints.EntityCollection.ConcurrencyLimit,
			}
			if cfg.Endpoints.EntityCollection.PaginationLimit > 0 {
				pec.SortEntitiesComparisonFunc = func(a, b *oidfed.CollectedEntity) int {
					return strings.Compare(a.EntityID, b.EntityID)
				}
				pec.PagingLimit = cfg.Endpoints.EntityCollection.PaginationLimit
			}
			collector = pec
		}
		if err := lh.AddEntityCollectionEndpoint(
			endpointConf(cfg.Endpoints.EntityCollection.Path, cfg.Endpoints.EntityCollection.URL, cfg.Endpoints.EntityCollection.AuthEnabled, cfg.Endpoints.EntityCollection.AuthTrustAnchors),
			collector, cfg.Endpoints.EntityCollection.AllowedTrustAnchors,
			cfg.Endpoints.EntityCollection.PaginationLimit > 0,
		); err != nil {
			return stops, fmt.Errorf("endpoints.entity_collection: %w", err)
		}
	}

	// jwks_update (light addition; works with the file-backed subordinate store)
	if !skip(cfg.Endpoints.JWKSUpdate.Path, cfg.Endpoints.JWKSUpdate.URL) {
		if err := lh.AddJWKSUpdateEndpoint(
			endpointConf(cfg.Endpoints.JWKSUpdate.Path, cfg.Endpoints.JWKSUpdate.URL, cfg.Endpoints.JWKSUpdate.AuthEnabled, cfg.Endpoints.JWKSUpdate.AuthTrustAnchors),
			backs.Subordinates,
		); err != nil {
			return stops, fmt.Errorf("endpoints.jwks_update: %w", err)
		}
	}

	// jwks_update_trigger (light addition)
	if !skip(cfg.Endpoints.JWKSUpdateTrigger.Path, cfg.Endpoints.JWKSUpdateTrigger.URL) {
		if err := lh.AddJWKSUpdateTriggerEndpoint(
			endpointConf(cfg.Endpoints.JWKSUpdateTrigger.Path, cfg.Endpoints.JWKSUpdateTrigger.URL, cfg.Endpoints.JWKSUpdateTrigger.AuthEnabled, cfg.Endpoints.JWKSUpdateTrigger.AuthTrustAnchors),
			backs.Subordinates,
		); err != nil {
			return stops, fmt.Errorf("endpoints.jwks_update_trigger: %w", err)
		}
	}

	return stops, nil
}
