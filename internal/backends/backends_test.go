package backends

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gorm.io/datatypes"

	zapduration "github.com/zachmann/go-utils/duration"

	oidfed "github.com/go-oidfed/lib"
	"github.com/go-oidfed/lib/jwx"

	"github.com/go-oidfed/lighthouse-light/internal/config"
	"github.com/go-oidfed/lighthouse/storage/model"
)

// newTestBackends builds a store set in a fresh temp dir (no config).
func newTestBackends(t *testing.T) model.Backends {
	t.Helper()
	dir := t.TempDir()
	backs, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return backs
}

// newTestBackendsConfig builds a store set with a config (config-owned KV).
func newTestBackendsConfig(t *testing.T, mutate func(*config.Config)) model.Backends {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.EntityID = "https://ta.example.org"
	cfg.Signing.FileSystemBackend.KeyDir = filepath.Join(dir, "keys")
	if mutate != nil {
		mutate(cfg)
	}
	backs, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return backs
}

func TestKVStore(t *testing.T) {
	backs := newTestBackends(t)
	kv := backs.KV

	if err := kv.Set("scope-a", "key-a", datatypes.JSON(`{"x":1}`)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := kv.Get("scope-a", "key-a")
	if err != nil || string(got) != `{"x":1}` {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if missing, err := kv.Get("missing", "key"); err != nil || missing != nil {
		t.Fatalf("Get missing = %q, %v; want nil,nil", missing, err)
	}
	if err := kv.SetAny("scope-a", "num", 42); err != nil {
		t.Fatalf("SetAny: %v", err)
	}
	var n int
	found, err := kv.GetAs("scope-a", "num", &n)
	if err != nil || !found || n != 42 {
		t.Fatalf("GetAs = %v, %d, %v; want true,42,nil", found, n, err)
	}
	var s string
	if found, _ := kv.GetAs("nope", "x", &s); found {
		t.Fatalf("GetAs missing should be false")
	}
	if err := kv.Delete("scope-a", "key-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if missing, _ := kv.Get("scope-a", "key-a"); missing != nil {
		t.Fatalf("Delete did not remove entry")
	}
	if err := kv.Delete("scope-a", "key-a"); err != nil { // idempotent
		t.Fatalf("Delete idempotent: %v", err)
	}
}

// TestConfigOwnedKVNotPersisted verifies the config-owned KV overlay: values
// are served from the config via the in-memory KV store, no kv.json file is
// ever created, and absent config values are absent from the store.
func TestConfigOwnedKVNotPersisted(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.EntityID = "https://ta.example.org"
	cfg.Signing.FileSystemBackend.KeyDir = filepath.Join(dir, "keys")
	cfg.Signing.Alg = "ES256"
	cfg.FederationData.ConfigurationLifetime = zapduration.DurationOption(3600 * time.Second)

	backs, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, err := backs.KV.Get("signing", "alg"); err != nil || got == nil {
		t.Fatalf("signing/alg not served from config: %v, %v", got, err)
	}
	if got, err := backs.KV.Get("entity_configuration", "lifetime"); err != nil || got == nil {
		t.Fatalf("entity_configuration/lifetime not served from config: %v, %v", got, err)
	}

	// The KV store is in-memory: no kv.json file may exist.
	if _, err := os.Stat(filepath.Join(dir, "kv.json")); !os.IsNotExist(err) {
		t.Fatalf("kv.json must not exist (KV is in-memory), stat err: %v", err)
	}

	// Rebuild WITHOUT the config: the values must be gone (nothing persisted).
	backs2, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build 2: %v", err)
	}
	for _, key := range []string{"alg", "lifetime"} {
		if got, _ := backs2.KV.Get("signing", key); got != nil {
			t.Fatalf("signing/%s survived a config-less rebuild: %q", key, got)
		}
	}
}
func TestJTIStore(t *testing.T) {
	backs := newTestBackends(t)
	jti := backs.JTI

	if err := jti.Store("jti-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	exists, err := jti.Exists("jti-1")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true", exists, err)
	}
	// Expired JTI: Exists must report false and reap it.
	if err := jti.Store("jti-expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("Store expired: %v", err)
	}
	exists, err = jti.Exists("jti-expired")
	if err != nil || exists {
		t.Fatalf("Exists expired = %v, %v; want false", exists, err)
	}
	if err := jti.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

func TestSubordinateCRUD(t *testing.T) {
	backs := newTestBackends(t)
	subs := backs.Subordinates

	info := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{
			EntityID:    "https://sub.example.org",
			Description: "desc",
			Status:      model.StatusActive,
		},
		JWKS: testJWKS(),
	}
	if err := subs.Add(info); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := subs.Get("https://sub.example.org")
	if err != nil || got == nil {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if got.Description != "desc" || got.Status != model.StatusActive {
		t.Fatalf("unexpected row: %+v", got)
	}
	if err := subs.UpdateStatus("https://sub.example.org", model.StatusBlocked); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if got, _ := subs.Get("https://sub.example.org"); got.Status != model.StatusBlocked {
		t.Fatalf("status after update = %v", got.Status)
	}
	if err := subs.UpdateJWKSByEntityID("https://sub.example.org", testJWKS()); err != nil {
		t.Fatalf("UpdateJWKSByEntityID: %v", err)
	}
	if got, _ := subs.Get("https://sub.example.org"); got.JWKS.Keys.Len() == 0 {
		t.Fatalf("jwks not updated")
	}
	// Duplicate Add must error.
	if err := subs.Add(info); err == nil {
		t.Fatalf("Add duplicate should error")
	}
	// Unknown subordinate: Get returns nil, nil.
	if got, err := subs.Get("https://nope.example.org"); err != nil || got != nil {
		t.Fatalf("Get unknown = %v, %v; want nil,nil", got, err)
	}
	// GetByDBID resolves via the content-addressed ID.
	byID, err := subs.GetByDBID(itoa(int(hashID("https://sub.example.org"))))
	if err != nil || byID == nil || byID.EntityID != "https://sub.example.org" {
		t.Fatalf("GetByDBID = %v, %v", byID, err)
	}
}

func testJWKS() model.JWKS {
	var jwks model.JWKS
	raw := `{"keys":[{"kty":"EC","crv":"P-256","kid":"k1","x":"FHEJbLPz7CZTXpOA5QLjD1iQuobK_4rT6uqmfHAWm2g","y":"grLHVQa9f0bj0TZ8_BREg88n6_6rQUTRN4c4PON0CzE"}]}`
	if err := json.Unmarshal([]byte(raw), &jwks.Keys); err != nil {
		panic(err)
	}
	return model.NewJWKS(jwks.Keys)
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	backs1, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build 1: %v", err)
	}

	// Mutate several stores. The KV store is deliberately NOT mutated: it is
	// in-memory only, so nothing it holds may survive a rebuild.
	if err := backs1.Subordinates.Add(model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://sub.example.org", Status: model.StatusActive},
		JWKS:                 testJWKS(),
	}); err != nil {
		t.Fatalf("Sub Add: %v", err)
	}
	if _, err := backs1.TrustMarkSpecs.Create(&model.AddTrustMarkSpec{
		TrustMarkType: "https://example.org/tm",
		Lifetime:      3600,
	}); err != nil {
		t.Fatalf("Spec Create: %v", err)
	}
	if err := backs1.TrustMarks.Approve("https://example.org/tm", "https://sub.example.org"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := backs1.TrustMarkInstances.Create(&model.IssuedTrustMarkInstance{
		JTI: "jti-1", ExpiresAt: int(time.Now().Add(time.Hour).Unix()),
		TrustMarkType: "https://example.org/tm", Subject: "https://sub.example.org",
	}); err != nil {
		t.Fatalf("Instance Create: %v", err)
	}

	// Rebuild from the same dir: every file-backed mutation must survive; the
	// in-memory KV must come back empty.
	backs2, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build 2: %v", err)
	}
	if got, _ := backs2.KV.Get("scope", "key"); got != nil {
		t.Fatalf("KV entry must not survive a rebuild (KV is in-memory): %q", got)
	}
	if sub, _ := backs2.Subordinates.Get("https://sub.example.org"); sub == nil {
		t.Fatalf("subordinate lost after rebuild")
	}
	if ok, _ := backs2.TrustMarks.HasTrustMark("https://example.org/tm", "https://sub.example.org"); !ok {
		t.Fatalf("trust mark lost after rebuild")
	}
	if _, err := backs2.TrustMarkInstances.GetByJTI("jti-1"); err != nil {
		t.Fatalf("instance lost after rebuild")
	}
}

// TestNoIDsInFiles verifies the load-bearing requirement: the data files
// contain no numeric ids and no next_* counters — IDs are derived.
func TestNoIDsInFiles(t *testing.T) {
	dir := t.TempDir()
	backs, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Create rows in every ID-bearing store.
	jwks := testJWKS()
	if _, err := backs.AuthorityHints.Create(model.AddAuthorityHint{EntityID: "https://ta.example.org"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.TrustAnchors.Create(model.AddTrustAnchor{EntityID: "https://root.example.org", JWKS: &jwks}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.TrustMarkTypes.Create(model.AddTrustMarkType{
		TrustMarkType:    "https://example.org/tm",
		TrustMarkIssuers: []model.AddTrustMarkIssuer{{Issuer: "https://tmi.example.org"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.PublishedTrustMarks.Create(model.AddTrustMark{TrustMarkType: "https://example.org/tm", TrustMark: "jwt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.AdditionalClaims.Create(model.AddAdditionalClaim{Claim: "c1", Value: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.TrustMarkSpecs.Create(&model.AddTrustMarkSpec{TrustMarkType: "https://example.org/tm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := backs.TrustMarkSpecs.CreateSubject("https://example.org/tm", &model.AddTrustMarkSubject{EntityID: "https://sub.example.org", Status: model.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if err := backs.Subordinates.Add(model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://sub.example.org", Status: model.StatusActive},
		JWKS:                 jwks,
	}); err != nil {
		t.Fatal(err)
	}

	for _, name := range StoreFiles {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, forbidden := range []string{`"next_`, `"next_id"`, `"id":`, `"spec_id"`, `"owner_id"`, `"issuer_id"`, `"claim_id"`} {
			if containsStr(string(content), forbidden) {
				t.Errorf("%s contains forbidden key %s: %s", name, forbidden, content)
			}
		}
	}
}

func TestLegacyFormatRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(dir, nil); err != nil {
		t.Fatalf("Build: %v", err)
	}
	old := `{"next_id":2,"subordinates":[{"id":1,"entity_id":"https://sub.example.org","status":"active","jwks":{}}]}`
	if err := os.WriteFile(filepath.Join(dir, "subordinates.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Build(dir, nil)
	if err == nil {
		t.Fatalf("legacy-format file should be rejected")
	}
	if !containsStr(err.Error(), "legacy numeric-ID format") {
		t.Fatalf("error should explain the legacy format, got: %v", err)
	}
}

func TestCorruptFileIsFatal(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(dir, nil); err != nil {
		t.Fatalf("Build: %v", err)
	}
	corrupt := filepath.Join(dir, "subordinates.json")
	if err := os.WriteFile(corrupt, []byte("{{{{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Build(dir, nil)
	if err == nil {
		t.Fatalf("Build with corrupt file should fail")
	}
	if !containsStr(err.Error(), "subordinates.json") {
		t.Fatalf("error should name the corrupt file, got: %v", err)
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestGeneralMetadataConfigOverlay(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yamlCfg := `entity_id: https://ta.example.org
data_dir: %s
logging: { level: info, format: json }
signing:
  filesystem: { key_dir: keys }
federation_data:
  metadata:
    openid_provider:
      organization_name: "General Org"
endpoints:
  fetch: { path: /fetch }
`
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(yamlCfg, dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.FederationData.SubordinateMetadata == nil ||
		cfg.FederationData.SubordinateMetadata.OpenIDProvider == nil ||
		cfg.FederationData.SubordinateMetadata.OpenIDProvider.OrganizationName != "General Org" {
		t.Fatalf("federation_data.metadata not mapped from YAML: %+v", cfg.FederationData.SubordinateMetadata)
	}

	backs, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Subordinate without own metadata falls back to the general value.
	noMeta := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://plain.example.org", Status: model.StatusActive},
		JWKS:                 testJWKS(),
	}
	if err := backs.Subordinates.Add(noMeta); err != nil {
		t.Fatalf("Add noMeta: %v", err)
	}
	got, _ := backs.Subordinates.Get("https://plain.example.org")
	if got.Metadata == nil || got.Metadata.OpenIDProvider == nil ||
		got.Metadata.OpenIDProvider.OrganizationName != "General Org" {
		t.Fatalf("general metadata fallback not applied: %+v", got.Metadata)
	}

	// Subordinate with its own metadata keeps it.
	withOwn := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://own.example.org", Status: model.StatusActive},
		JWKS:                 testJWKS(),
		Metadata:             &oidfed.Metadata{OpenIDProvider: &oidfed.OpenIDProviderMetadata{OrganizationName: "Own Org"}},
	}
	if err := backs.Subordinates.Add(withOwn); err != nil {
		t.Fatalf("Add withOwn: %v", err)
	}
	own, _ := backs.Subordinates.Get("https://own.example.org")
	if own.Metadata == nil || own.Metadata.OpenIDProvider == nil ||
		own.Metadata.OpenIDProvider.OrganizationName != "Own Org" {
		t.Fatalf("own metadata replaced by fallback: %+v", own.Metadata)
	}

	// Config-owned value must not be persisted: kv.json does not exist.
	if _, err := os.Stat(filepath.Join(dir, "kv.json")); !os.IsNotExist(err) {
		t.Fatalf("kv.json must not exist (KV is in-memory), stat err: %v", err)
	}
}

func TestGeneralClaimsFallback(t *testing.T) {
	// General claims are config-owned (federation_data.subordinate_
	// additional_claims): seed them via config and verify the fallback
	// behavior at read time.
	backs := newTestBackendsConfig(t, func(cfg *config.Config) {
		cfg.FederationData.SubordinateAdditionalClaims = []config.SubordinateAdditionalClaimConf{
			{Claim: "general_1", Value: "gval", Crit: false},
		}
	})

	// Subordinate with own claims keeps them.
	withOwn := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://own.example.org", Status: model.StatusActive},
		JWKS:                 testJWKS(),
		SubordinateAdditionalClaims: []model.SubordinateAdditionalClaim{
			{Claim: "own_1", Value: "oval", Crit: true},
		},
	}
	if err := backs.Subordinates.Add(withOwn); err != nil {
		t.Fatalf("Add withOwn: %v", err)
	}
	// Subordinate with no claims gets the general fallback.
	noClaims := model.ExtendedSubordinateInfo{
		BasicSubordinateInfo: model.BasicSubordinateInfo{EntityID: "https://plain.example.org", Status: model.StatusActive},
		JWKS:                 testJWKS(),
	}
	if err := backs.Subordinates.Add(noClaims); err != nil {
		t.Fatalf("Add noClaims: %v", err)
	}

	gotOwn, _ := backs.Subordinates.Get("https://own.example.org")
	if len(gotOwn.SubordinateAdditionalClaims) != 1 || gotOwn.SubordinateAdditionalClaims[0].Claim != "own_1" {
		t.Fatalf("own claims replaced by fallback: %+v", gotOwn.SubordinateAdditionalClaims)
	}
	gotPlain, _ := backs.Subordinates.Get("https://plain.example.org")
	if len(gotPlain.SubordinateAdditionalClaims) != 1 || gotPlain.SubordinateAdditionalClaims[0].Claim != "general_1" {
		t.Fatalf("general fallback not applied: %+v", gotPlain.SubordinateAdditionalClaims)
	}

	// Raw get must return exactly the stored row (no fallback).
	plainInfo, _ := backs.Subordinates.Get("https://plain.example.org")
	raw, err := backs.Subordinates.GetByDBIDRaw(itoa(int(plainInfo.ID)))
	if err != nil || raw == nil {
		t.Fatalf("GetByDBIDRaw: %v, %v", raw, err)
	}
	if len(raw.SubordinateAdditionalClaims) != 0 {
		t.Fatalf("GetByDBIDRaw should not apply fallback: %+v", raw.SubordinateAdditionalClaims)
	}
}

func TestAuthorityHintCRUD(t *testing.T) {
	backs := newTestBackends(t)
	ah := backs.AuthorityHints

	hint, err := ah.Create(model.AddAuthorityHint{EntityID: "https://ta.example.org", Description: "d", JWKSSync: &model.JWKSSyncConf{Mode: model.JWKSSyncPush, JWTLifetime: 60}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if hint.ID != hashID("https://ta.example.org") {
		t.Fatalf("hint ID should be content-addressed")
	}
	if _, err := ah.Create(model.AddAuthorityHint{EntityID: "https://ta.example.org"}); err == nil {
		t.Fatalf("duplicate hint should error")
	}
	// Get by entity ID and by content-addressed ID.
	if got, err := ah.Get("https://ta.example.org"); err != nil || got == nil {
		t.Fatalf("Get by entityID: %v, %v", got, err)
	}
	if got, err := ah.Get(itoa(int(hashID("https://ta.example.org")))); err != nil || got == nil {
		t.Fatalf("Get by ID: %v, %v", got, err)
	}
	// Patch jwks_sync.
	patched, err := ah.Patch("https://ta.example.org", map[string]any{"jwks_sync": map[string]any{"mode": "trigger", "timeout": 30}})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if patched.JWKSSync == nil || patched.JWKSSync.Mode != model.JWKSSyncTrigger {
		t.Fatalf("patch failed: %+v", patched.JWKSSync)
	}
	list, err := ah.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := ah.Delete("https://ta.example.org"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := ah.List(); len(list) != 0 {
		t.Fatalf("List after delete = %d", len(list))
	}
}

func TestTrustAnchorStore(t *testing.T) {
	backs := newTestBackends(t)
	ta := backs.TrustAnchors

	req := model.AddTrustAnchor{EntityID: "https://root.example.org"}
	jwks := testJWKS()
	req.JWKS = &jwks
	created, err := ta.Create(req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.JWKS.Keys.Len() == 0 {
		t.Fatalf("jwks not stored")
	}
	if _, err := ta.Create(req); err == nil {
		t.Fatalf("duplicate TA should error")
	}
	if gotJWKS, err := ta.GetJWKS("https://root.example.org"); err != nil || gotJWKS == nil {
		t.Fatalf("GetJWKS: %v, %v", gotJWKS, err)
	}
}

// TestTrustAnchorEnableJWKSUpdate verifies that trust_anchors.json rows may
// carry enable_jwks_update (automatic JWKS refresh; supported via the file
// store implementing oidfed.JWKStorage) and that the flag and refreshed keys
// survive persist/reload.
func TestTrustAnchorEnableJWKSUpdate(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(dir, nil); err != nil {
		t.Fatalf("Build: %v", err)
	}
	row := `[{"entity_id":"https://root.example.org","enable_jwks_update":true,"key_poll_interval":300}]`
	if err := os.WriteFile(filepath.Join(dir, "trust_anchors.json"), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}

	backs, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build with enable_jwks_update: %v", err)
	}
	anchors, err := backs.TrustAnchors.List()
	if err != nil || len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d (err %v)", len(anchors), err)
	}
	if !anchors[0].EnableJWKSUpdate || anchors[0].KeyPollInterval != 300 {
		t.Fatalf("flag/interval not loaded: %+v", anchors[0])
	}

	// The store must satisfy oidfed.JWKStorage (the refresher's storage).
	var store oidfed.JWKStorage = backs.TrustAnchors.(*TrustAnchorsStore)
	jwks := jwx.NewJWKS()
	if err := json.Unmarshal(
		[]byte(`{"keys":[{"kty":"EC","crv":"P-256","kid":"fresh","x":"AAAA","y":"BBBB"}]}`),
		&jwks,
	); err != nil {
		t.Fatalf("parse jwks: %v", err)
	}
	if err := store.UpdateJWKS("https://root.example.org", jwks); err != nil {
		t.Fatalf("UpdateJWKS: %v", err)
	}
	if err := store.RegisterEntityJWKSFile("https://root.example.org", "/nope"); err != nil {
		t.Fatalf("RegisterEntityJWKSFile: %v", err)
	}
	got, err := store.GetJWKS("https://root.example.org")
	if err != nil || got == nil || got.Len() != 1 {
		t.Fatalf("GetJWKS: got %v (err %v)", got, err)
	}

	// Rebuild: refreshed keys and the flag persist in the file.
	backs2, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	anchors2, _ := backs2.TrustAnchors.List()
	if !anchors2[0].EnableJWKSUpdate || anchors2[0].JWKS.Keys.Len() != 1 {
		t.Fatalf("flag/keys not persisted: %+v", anchors2[0])
	}
}

func TestTrustMarkTypesStore(t *testing.T) {
	backs := newTestBackends(t)
	tm := backs.TrustMarkTypes

	typ, err := tm.Create(model.AddTrustMarkType{
		TrustMarkType:    "https://example.org/tm",
		TrustMarkIssuers: []model.AddTrustMarkIssuer{{Issuer: "https://tmi.example.org"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if typ.ID != hashID("https://example.org/tm") {
		t.Fatalf("type ID should be content-addressed")
	}
	issuers, err := tm.IssuersByType()
	if err != nil || len(issuers["https://example.org/tm"]) != 1 || issuers["https://example.org/tm"][0] != "https://tmi.example.org" {
		t.Fatalf("IssuersByType = %+v, %v", issuers, err)
	}
	// Owner round trip.
	owner, err := tm.CreateOwner("https://example.org/tm", model.AddTrustMarkOwner{EntityID: "https://owner.example.org", JWKS: testJWKS()})
	if err != nil {
		t.Fatalf("CreateOwner: %v", err)
	}
	if owner.EntityID != "https://owner.example.org" {
		t.Fatalf("owner wrong: %+v", owner)
	}
	owners, err := tm.OwnersByType()
	if err != nil || owners["https://example.org/tm"].ID != "https://owner.example.org" {
		t.Fatalf("OwnersByType = %+v, %v", owners, err)
	}
	// Replace issuers via SetIssuers.
	if _, err := tm.SetIssuers("https://example.org/tm", []model.AddTrustMarkIssuer{{Issuer: "https://tmi2.example.org"}}); err != nil {
		t.Fatalf("SetIssuers: %v", err)
	}
	if list, _ := tm.ListIssuers("https://example.org/tm"); len(list) != 1 || list[0].Issuer != "https://tmi2.example.org" {
		t.Fatalf("issuers not replaced: %+v", list)
	}
}

func TestTrustMarkSpecsAndSubjects(t *testing.T) {
	backs := newTestBackends(t)
	specs := backs.TrustMarkSpecs

	spec, err := specs.Create(&model.AddTrustMarkSpec{
		TrustMarkType:     "https://example.org/tm",
		Lifetime:          3600,
		CacheTTL:          60,
		EligibilityConfig: &model.EligibilityConfig{Mode: model.EligibilityModeDBOnly, CheckCacheTTL: 30},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if spec.EligibilityConfig == nil || spec.EligibilityConfig.CheckCacheTTL != 30 {
		t.Fatalf("eligibility config not persisted: %+v", spec.EligibilityConfig)
	}
	if _, err := specs.Create(&model.AddTrustMarkSpec{TrustMarkType: "https://example.org/tm"}); err == nil {
		t.Fatalf("duplicate spec should error")
	}
	sub, err := specs.CreateSubject("https://example.org/tm", &model.AddTrustMarkSubject{
		EntityID: "https://sub.example.org", Status: model.StatusActive,
	})
	if err != nil {
		t.Fatalf("CreateSubject: %v", err)
	}
	if sub.ID != hashID("https://example.org/tm", "https://sub.example.org") {
		t.Fatalf("subject ID should be content-addressed")
	}
	if _, err := specs.CreateSubject("https://example.org/tm", &model.AddTrustMarkSubject{EntityID: "https://sub.example.org", Status: model.StatusActive}); err == nil {
		t.Fatalf("duplicate subject should error")
	}
	// Subject ID must be resolvable by the instances store — and STABLE across
	// a rebuild (persisted instances keep linking to subjects).
	if id, err := backs.TrustMarkInstances.FindSubjectID("https://example.org/tm", "https://sub.example.org"); err != nil || id != sub.ID {
		t.Fatalf("FindSubjectID = %d, %v; want %d", id, err, sub.ID)
	}
	// ChangeSubjectStatus should leave the row updated.
	changed, err := specs.ChangeSubjectStatus("https://example.org/tm", "https://sub.example.org", model.StatusBlocked)
	if err != nil || changed.Status != model.StatusBlocked {
		t.Fatalf("ChangeSubjectStatus = %+v, %v", changed, err)
	}
}

// TestSubjectIDStableAcrossRebuild ensures hash-derived IDs survive a reload.
func TestSubjectIDStableAcrossRebuild(t *testing.T) {
	dir := t.TempDir()
	backs1, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := backs1.TrustMarkSpecs.Create(&model.AddTrustMarkSpec{TrustMarkType: "t", Lifetime: 3600}); err != nil {
		t.Fatal(err)
	}
	sub, err := backs1.TrustMarkSpecs.CreateSubject("t", &model.AddTrustMarkSubject{EntityID: "https://sub.example.org", Status: model.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	backs2, err := Build(dir, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	id2, err := backs2.TrustMarkInstances.FindSubjectID("t", "https://sub.example.org")
	if err != nil || id2 != sub.ID {
		t.Fatalf("subject ID changed across rebuild: %d != %d (%v)", id2, sub.ID, err)
	}
	gotSub, err := backs2.TrustMarkSpecs.GetSubject("t", "https://sub.example.org")
	if err != nil || gotSub.ID != sub.ID {
		t.Fatalf("GetSubject ID changed: %+v, %v", gotSub, err)
	}
}

func TestPublishedTrustMarksStore(t *testing.T) {
	backs := newTestBackends(t)
	pm := backs.PublishedTrustMarks

	if _, err := pm.Create(model.AddTrustMark{
		TrustMarkType: "https://example.org/tm",
		TrustMark:     "jwt.here",
		Refresh:       true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pm.Create(model.AddTrustMark{TrustMarkType: "https://example.org/tm"}); err == nil {
		t.Fatalf("duplicate should error")
	}
	got, err := pm.Get("https://example.org/tm")
	if err != nil || got.TrustMarkJWT != "jwt.here" || !got.Refresh {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	updated, err := pm.Update("https://example.org/tm", model.AddTrustMark{
		TrustMarkType:    "https://example.org/tm",
		TrustMark:        "jwt2",
		SelfIssuanceSpec: &model.SelfIssuedTrustMarkSpec{Lifetime: 100, Ref: "r1"},
	})
	if err != nil || updated.TrustMarkJWT != "jwt2" || updated.SelfIssuanceSpec == nil {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	if err := pm.Delete("https://example.org/tm"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := pm.List(); len(list) != 0 {
		t.Fatalf("List after delete = %d", len(list))
	}
}

func TestAdditionalClaimsStore(t *testing.T) {
	backs := newTestBackends(t)
	ac := backs.AdditionalClaims

	row, err := ac.Create(model.AddAdditionalClaim{Claim: "c1", Value: "v1", Crit: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if row.ID != hashID("c1") {
		t.Fatalf("claim ID should be content-addressed")
	}
	if _, err := ac.Create(model.AddAdditionalClaim{Claim: "c1"}); err == nil {
		t.Fatalf("duplicate should error")
	}
	if got, err := ac.Get("c1"); err != nil || got.Value != "v1" {
		t.Fatalf("Get by claim = %+v, %v", got, err)
	}
	updated, err := ac.Update("c1", model.AddAdditionalClaim{Claim: "c1", Value: "v2", Crit: false})
	if err != nil || updated.Value != "v2" {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	if err := ac.Delete("c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

}

func TestExtraEntityConfigurationClaimsFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.EntityID = "https://ta.example.org"
	cfg.Signing.FileSystemBackend.KeyDir = filepath.Join(dir, "keys")
	cfg.FederationData.ExtraEntityConfigurationClaims = []config.SubordinateAdditionalClaimConf{
		{Claim: "c1", Value: "v1", Crit: true},
		{Claim: "c2", Value: 42},
	}
	backs, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rows, err := backs.AdditionalClaims.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("List = %d rows; want 2", len(rows))
	}
	if rows[0].Claim != "c1" || rows[0].Value != "v1" || !rows[0].Crit {
		t.Fatalf("c1 row wrong: %+v", rows[0])
	}
	if rows[1].Claim != "c2" || rows[1].Value != 42 {
		t.Fatalf("c2 row wrong: %+v", rows[1])
	}

	// The store is in-memory: no additional_claims.json file may exist.
	if _, err := os.Stat(filepath.Join(dir, "additional_claims.json")); !os.IsNotExist(err) {
		t.Fatalf("additional_claims.json must not exist (config-seeded store), stat err: %v", err)
	}
}

func TestTrustMarkInstancesStoreLifecycle(t *testing.T) {
	backs := newTestBackends(t)
	inst := backs.TrustMarkInstances

	// A spec + subject so FindSubjectID resolves correctly.
	if _, err := backs.TrustMarkSpecs.Create(&model.AddTrustMarkSpec{TrustMarkType: "t", Lifetime: 3600}); err != nil {
		t.Fatalf("spec create: %v", err)
	}
	sub, err := backs.TrustMarkSpecs.CreateSubject("t", &model.AddTrustMarkSubject{EntityID: "s1", Status: model.StatusActive})
	if err != nil {
		t.Fatalf("subject create: %v", err)
	}

	now := int(time.Now().Unix())
	if err := inst.Create(&model.IssuedTrustMarkInstance{JTI: "a", ExpiresAt: now + 3600, TrustMarkType: "t", Subject: "s1", TrustMarkSubjectID: sub.ID}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := inst.Create(&model.IssuedTrustMarkInstance{JTI: "b", ExpiresAt: now - 10, TrustMarkType: "t", Subject: "s2"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := inst.Create(&model.IssuedTrustMarkInstance{JTI: "c", ExpiresAt: now + 100, Revoked: true, TrustMarkType: "t", Subject: "s3"}); err != nil {
		t.Fatalf("create c: %v", err)
	}
	active, err := inst.ListActiveSubjects("t")
	if err != nil || !reflect.DeepEqual(active, []string{"s1"}) {
		t.Fatalf("ListActiveSubjects = %v, %v", active, err)
	}
	if ok, _ := inst.HasActiveInstance("t", "s1"); !ok {
		t.Fatalf("s1 should have active instance")
	}
	if ok, _ := inst.HasActiveInstance("t", "s2"); ok {
		t.Fatalf("s2 should be expired")
	}
	st, err := inst.GetStatus("c")
	if err != nil || st != model.TrustMarkStatusRevoked {
		t.Fatalf("GetStatus(c) = %v, %v", st, err)
	}
	if _, err := inst.GetStatus("nope"); err == nil {
		t.Fatalf("GetStatus unknown should error")
	}
	n, err := inst.DeleteExpired(0)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired = %d, %v; want 1", n, err)
	}
	// Revoking by subject ID must revoke "a" (the only non-revoked instance
	// belonging to the subject).
	n, err = inst.RevokeBySubjectID(sub.ID)
	if err != nil || n != 1 {
		t.Fatalf("RevokeBySubjectID = %d, %v; want 1", n, err)
	}
	if st, _ := inst.GetStatus("a"); st != model.TrustMarkStatusRevoked {
		t.Fatalf("a should be revoked after RevokeBySubjectID, got %v", st)
	}
}

// TestTrustMarkInstancesStoreRejectsAliasedStrings guards against the
// zero-copy request-binding corruption: fiber binds query/body strings as
// views into a reused request buffer, so an instance row must not retain
// those views. Corrupting the source bytes after Create must not change what
// the store returns (nor what it later persists).
func TestTrustMarkInstancesStoreRejectsAliasedStrings(t *testing.T) {
	backs := newTestBackends(t)
	inst := backs.TrustMarkInstances

	// Simulate a request buffer: a []byte that the "next request" rewrites.
	// The zero-copy view below aliases buf exactly like fiber's
	// utils.UnsafeString does for query/body values.
	buf := []byte("http://localhost:7674")
	subj := unsafe.String(&buf[0], len(buf))
	typ := "https://example.org/trustmark"

	if err := inst.Create(&model.IssuedTrustMarkInstance{
		JTI: "aliased-1", TrustMarkType: typ, Subject: subj, ExpiresAt: int(time.Now().Unix()) + 3600,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// "Next request" reuses the buffer with a different subject.
	copy(buf, "http://localhost:7673")

	active, err := inst.ListActiveSubjects(typ)
	if err != nil || !reflect.DeepEqual(active, []string{"http://localhost:7674"}) {
		t.Fatalf("ListActiveSubjects after buffer reuse = %v, %v; want the original subject", active, err)
	}
	got, err := inst.GetByJTI("aliased-1")
	if err != nil || got.Subject != "http://localhost:7674" {
		t.Fatalf("GetByJTI after buffer reuse = %+v, %v; want the original subject", got, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
