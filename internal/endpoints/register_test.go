package endpoints

import (
	"path/filepath"
	"testing"

	"github.com/go-oidfed/lighthouse"
	lhstats "github.com/go-oidfed/lighthouse/api/stats"
	"github.com/go-oidfed/lighthouse/storage/model"
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/go-oidfed/lighthouse-light/internal/backends"
	"github.com/go-oidfed/lighthouse-light/internal/config"
)

// newLH builds a real lighthouse.LightHouse over the file backends with
// auto-generated keys in a temp keydir.
func newLH(t *testing.T, cfg *config.Config) (*lighthouse.LightHouse, model.Backends) {
	t.Helper()
	backs, err := backends.Build(cfg.DataDir, cfg)
	if err != nil {
		t.Fatalf("backends.Build: %v", err)
	}
	signing := lighthouse.SigningConf{
		KMS:              lighthouse.KMSFilesystem,
		PKBackend:        lighthouse.PKBackendFilesystem,
		AutoGenerateKeys: true,
	}
	signing.FileSystemBackend.KeyDir = filepath.Join(t.TempDir(), "keys")
	server := lighthouse.ServerConf{IPListen: "127.0.0.1", Port: 0}
	lh, err := lighthouse.NewLightHouse(
		server, cfg.EntityID, signing, backs,
		lighthouse.AdminAPIOptions{Enabled: false},
		lhstats.Config{},
	)
	if err != nil {
		t.Fatalf("NewLightHouse: %v", err)
	}
	repo := lighthouse.NewTrustAnchorRepo(backs.TrustAnchors)
	if err := repo.Load(); err != nil {
		t.Fatalf("TA repo load: %v", err)
	}
	lh.SetTrustAnchorRepo(repo)
	return lh, backs
}

func parseConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	c := config.DefaultConfig()
	if err := yamlv3.Unmarshal([]byte(yaml), c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c
}

const registerFullYAML = `
entity_id: https://ta.example.org
data_dir: DATA
signing:
  filesystem: { key_dir: KEYS }
  alg: ES512
endpoints:
  auth:
    trust_anchors: ["https://root.example.org"]
  fetch: { path: /fetch }
  list: { path: /subordinates }
  resolve: { path: /resolve, allowed_trust_anchors: ["https://root.example.org"] }
  trust_mark_status: { path: /trust_mark_status }
  trust_mark_list: { path: /trust_mark_list }
  trust_mark:
    path: /trust_mark
  historical_keys: { path: /historical_keys }
  enroll:
    path: /enroll
    checker: { type: none }
  enroll_request: { path: /enroll_request }
  trust_mark_request: { path: /trust_mark_request }
  entity_collection: { path: /collection }
  jwks_update: { path: /jwks_update }
  jwks_update_trigger: { path: /jwks_update_trigger }
`

// TestRegisterFullConfig registers every endpoint type on a real LightHouse.
func TestRegisterFullConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := parseConfig(t, replaceAll(registerFullYAML, "DATA", dir))
	cfg.Signing.FileSystemBackend.KeyDir = filepath.Join(dir, "keys")
	lh, backs := newLH(t, cfg)

	stops, err := Register(lh, cfg, backs)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()

	checks := map[model.FederationEndpointType]bool{
		model.EndpointTypeFetch:             true,
		model.EndpointTypeList:              true,
		model.EndpointTypeResolve:           true,
		model.EndpointTypeTrustMarkStatus:   true,
		model.EndpointTypeTrustMarkListing:  true,
		model.EndpointTypeTrustMark:         true,
		model.EndpointTypeTrustMarkRequest:  true,
		model.EndpointTypeHistoricalKeys:    true,
		model.EndpointTypeEnroll:            true,
		model.EndpointTypeEnrollRequest:     true,
		model.EndpointTypeEntityCollection:  true,
		model.EndpointTypeJwksUpdate:        true,
		model.EndpointTypeJwksUpdateTrigger: true,
	}
	for typ, want := range checks {
		if got := lh.HasEndpoint(typ); got != want {
			t.Errorf("HasEndpoint(%v) = %v; want %v", typ, got, want)
		}
	}
}

// TestRegisterSkipsUnconfigured verifies endpoints with neither path nor URL
// are skipped.
func TestRegisterSkipsUnconfigured(t *testing.T) {
	dir := t.TempDir()
	cfg := parseConfig(t, `
entity_id: https://ta.example.org
data_dir: `+dir+`
signing:
  filesystem: { key_dir: `+filepath.Join(dir, "keys")+` }
`)
	lh, backs := newLH(t, cfg)
	if _, err := Register(lh, cfg, backs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if lh.HasEndpoint(model.EndpointTypeFetch) {
		t.Fatalf("fetch should be skipped")
	}
	if lh.HasEndpoint(model.EndpointTypeResolve) {
		t.Fatalf("resolve should be skipped")
	}
}

// TestRegisterAuthFallback verifies the global endpoints.auth.trust_anchors
// fallback for an auth-enabled endpoint without its own auth_trust_anchors.
func TestRegisterAuthFallback(t *testing.T) {
	dir := t.TempDir()
	cfg := parseConfig(t, `
entity_id: https://ta.example.org
data_dir: `+dir+`
signing:
  filesystem: { key_dir: `+filepath.Join(dir, "keys")+` }
endpoints:
  auth:
    trust_anchors: ["https://root.example.org"]
  fetch:
    path: /fetch
    auth_enabled: true
`)
	lh, backs := newLH(t, cfg)
	if _, err := Register(lh, cfg, backs); err != nil {
		t.Fatalf("Register with auth fallback: %v", err)
	}
	if !lh.HasEndpoint(model.EndpointTypeFetch) {
		t.Fatalf("fetch should be registered")
	}
}

// TestRegisterAuthMissingTAsIsRejected exercises the validate-time check that
// an auth-enabled endpoint needs trust anchors.
func TestRegisterAuthMissingTAsIsRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := parseConfig(t, `
entity_id: https://ta.example.org
data_dir: `+dir+`
signing:
  filesystem: { key_dir: `+filepath.Join(dir, "keys")+` }
endpoints:
  fetch:
    path: /fetch
    auth_enabled: true
`)
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate should reject auth-enabled endpoint without trust anchors")
	}
}

func replaceAll(s, old, new string) string {
	for {
		idx := indexOf(s, old)
		if idx < 0 {
			return s
		}
		s = s[:idx] + new + s[idx+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
