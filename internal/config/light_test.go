package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestServerTLSParsed verifies that server.tls parses from YAML (the field
// lives in lighthouse.ServerConf, whose TLS member has an unexported type)
// and that cert/key paths resolve against the config file's directory.
func TestServerTLSParsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tls"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `entity_id: https://ta.example.org
data_dir: /data
signing:
  filesystem: { key_dir: /data/keys }
server:
  ip_listen: 127.0.0.1
  tls:
    enabled: true
    redirect_http: true
    cert: tls/cert.pem
    key: tls/key.pem
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tls := c.Server.TLS // unexported type; fields are exported
	if !tls.Enabled {
		t.Fatal("server.tls.enabled not parsed")
	}
	if !tls.RedirectHTTP {
		t.Fatal("server.tls.redirect_http not parsed")
	}
	if want := filepath.Join(dir, "tls", "cert.pem"); tls.Cert != want {
		t.Errorf("cert not resolved against config dir: got %q want %q", tls.Cert, want)
	}
	if want := filepath.Join(dir, "tls", "key.pem"); tls.Key != want {
		t.Errorf("key not resolved against config dir: got %q want %q", tls.Key, want)
	}
}

func TestExtraEntityConfigurationClaimsParsed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `entity_id: https://ta.example.org
data_dir: /data
signing:
  filesystem: { key_dir: /data/keys }
federation_data:
  extra_entity_configuration_claims:
    - claim: x
      value: y
      crit: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	claims := c.FederationData.ExtraEntityConfigurationClaims
	if len(claims) != 1 {
		t.Fatalf("extra_entity_configuration_claims = %d rows; want 1", len(claims))
	}
	if claims[0].Claim != "x" || claims[0].Value != "y" || !claims[0].Crit {
		t.Fatalf("claim row wrong: %+v", claims[0])
	}
}

func TestServerTLSValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(t *testing.T, extra string) error {
		t.Helper()
		cfgPath := filepath.Join(dir, "config.yaml")
		content := `entity_id: https://ta.example.org
data_dir: /data
signing:
  filesystem: { key_dir: /data/keys }
server:
` + extra
		if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(cfgPath)
		return err
	}

	if err := write(t, "  tls: { enabled: true }\n"); err == nil || !strings.Contains(err.Error(), "cert and key are required") {
		t.Errorf("tls without cert/key must fail, got %v", err)
	}
	if err := write(t, "  admin_tls: { enabled: true }\n"); err == nil || !strings.Contains(err.Error(), "admin_tls is not supported") {
		t.Errorf("admin_tls must fail, got %v", err)
	}
}
