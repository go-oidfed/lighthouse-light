// lighthouse-light is a config-driven, no-DB deployment of the lighthouse
// federation entity: it imports the lighthouse module and runs it with
// filesystem persistence (JSON files in a data dir) instead of SQLite, no
// admin API, and KMS fixed to the filesystem. See docs/ for full reference.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lighthouse"
	lhstats "github.com/go-oidfed/lighthouse/api/stats"
	"github.com/go-oidfed/lighthouse/storage/model"

	"github.com/go-oidfed/lighthouse-light/internal/backends"
	"github.com/go-oidfed/lighthouse-light/internal/config"
	"github.com/go-oidfed/lighthouse-light/internal/endpoints"
)

// VERSION is the lighthouse-light release version.
const VERSION = "0.1.0"

func main() {
	log.Info().Str("version", VERSION).Msg("lighthouse-light starting")

	var configFile string
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}
	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Logging: level + format from config.
	if lvl, err := zerolog.ParseLevel(cfg.Logging.Level); err == nil {
		zerolog.SetGlobalLevel(lvl)
	} else {
		log.Warn().Str("level", cfg.Logging.Level).Msg("invalid logging.level, using info")
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	if cfg.Logging.Format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	} else {
		log.Logger = log.Output(os.Stdout) // JSON
	}

	log.Info().Str("file", configFile).Msg("Loaded Config")

	// File-backed stores. All persisted state lives under cfg.DataDir; the
	// config-owned general settings (and entity-configuration extra claims)
	// are applied to the in-memory KV/additional-claims stores at boot.
	backs, err := backends.Build(cfg.DataDir, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize storage")
	}

	// Fail fast when an endpoint references a trust anchor that is not defined
	// in trust_anchors.json (the data file is the single source of truth).
	if err := backends.CheckReferencedTrustAnchors(cfg, backs); err != nil {
		log.Fatal().Err(err).Msg("invalid trust anchor references")
	}

	// KMS and public-key backend are fixed to the filesystem (user decision):
	// signing keys persist in the configured key dir; private keys never
	// appear in data files.
	signingConf := lighthouse.SigningConf{
		KMS:              lighthouse.KMSFilesystem,
		PKBackend:        lighthouse.PKBackendFilesystem,
		AutoGenerateKeys: cfg.Signing.AutoGenerateKeys,
		FileSystemBackend: struct {
			KeyFile string `yaml:"key_file" envconfig:"KEY_FILE"`
			KeyDir  string `yaml:"key_dir" envconfig:"KEY_DIR"`
		}{
			KeyFile: cfg.Resolve(cfg.Signing.FileSystemBackend.KeyFile),
			KeyDir:  cfg.Resolve(cfg.Signing.FileSystemBackend.KeyDir),
		},
	}

	serverConf := cfg.Server
	serverConf.AccessLogWriter = os.Stdout // admin TLS is rejected in validation

	// Admin API disabled; stats disabled (no DB/stats storage).
	lh, err := lighthouse.NewLightHouse(
		serverConf,
		cfg.EntityID,
		signingConf,
		backs,
		lighthouse.AdminAPIOptions{Enabled: false},
		lhstats.Config{},
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize lighthouse")
	}

	setupTrustMarkIssuer(lh, backs)

	// No banner art (the plan: log JSON/console lines only).
	lh.LogoBanner = false
	lh.VersionBanner = false

	log.Info().Msg("Initialized Entity")

	if err := setupTrustAnchorRepo(lh, backs); err != nil {
		log.Fatal().Err(err).Msg("failed to setup trust anchor repository")
	}

	// Automatic trust-anchor JWKS refresh: anchors with
	// enable_jwks_update=true in trust_anchors.json have their keys polled
	// from their entity configuration and persisted back into the file. Our
	// file store implements oidfed.JWKStorage, so no database is involved.
	startTAJWKSRefresher(lh, backs)

	stops, err := endpoints.Register(lh, cfg, backs)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to register endpoints")
	}

	log.Info().Msg("Added Endpoints")
	log.Info().
		Str("entity_id", cfg.EntityID).
		Int("port", cfg.Server.Port).
		Str("data_dir", cfg.DataDir).
		Msg("lighthouse-light started")

	// Graceful shutdown on SIGINT/SIGTERM: stop endpoint background services,
	// then the lighthouse server. Start blocks until the server stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			for _, s := range stops {
				s()
			}
			if err := lh.Stop(); err != nil {
				log.Warn().Err(err).Msg("error during shutdown")
			}
			close(done)
		case <-done:
		}
	}()

	lh.Start()
	<-done
	log.Info().Msg("lighthouse-light stopped")
}

// setupTrustMarkIssuer configures the trust mark spec provider backed by the
// file stores (mirrors lighthouse main).
func setupTrustMarkIssuer(lh *lighthouse.LightHouse, backs model.Backends) {
	if backs.TrustMarkSpecs != nil {
		dbProvider := lighthouse.NewDBTrustMarkSpecProvider(backs.TrustMarkSpecs)
		lh.TrustMarkIssuer.SetProvider(dbProvider)
		log.Info().Msg("Configured TrustMarkSpecProvider from files")
	}
}

// setupTrustAnchorRepo builds the trust anchor repository from the file store.
func setupTrustAnchorRepo(lh *lighthouse.LightHouse, backs model.Backends) error {
	if backs.TrustAnchors == nil {
		log.Warn().Msg("Trust anchor storage not available; skipping TA repository setup")
		return nil
	}
	repo := lighthouse.NewTrustAnchorRepo(backs.TrustAnchors)
	if err := repo.Load(); err != nil {
		return fmt.Errorf("failed to load trust anchor repository: %w", err)
	}
	log.Info().Int("count", repo.Count()).Msg("Loaded trust anchor repository")
	lh.SetTrustAnchorRepo(repo)
	return nil
}

// startTAJWKSRefresher starts the automatic trust-anchor JWKS refresher for
// anchors with enable_jwks_update=true. The refresher polls each anchor's
// entity configuration on its key_poll_interval and stores refreshed keys in
// trust_anchors.json via the file store (which implements oidfed.JWKStorage);
// the in-memory repo sees the updates live (shared TrustAnchor pointers).
// Stopped automatically by lh.Stop().
func startTAJWKSRefresher(lh *lighthouse.LightHouse, backs model.Backends) {
	repo := lh.TrustAnchorRepo()
	if repo == nil || len(repo.AllWithJWKSUpdate()) == 0 {
		log.Debug().Msg("No trust anchors with enable_jwks_update=true; skipping TA JWKS refresher")
		return
	}
	jwkStorage, ok := backs.TrustAnchors.(oidfed.JWKStorage)
	if !ok {
		log.Warn().Msg("Trust anchor store does not implement JWK storage; TA JWKS refresher disabled")
		return
	}
	refresher, err := lighthouse.SetupTAJWKSRefresher(repo, jwkStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start TA JWKS refresher")
	}
	lh.SetTAJWKSRefresher(refresher)
}
