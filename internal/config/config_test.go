package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadEnv(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	return loadWith("", func() []string { return environ })
}

func TestLoadDefaultsFromEnv(t *testing.T) {
	c, err := loadEnv(t, map[string]string{
		"SNOT_HOSTNAME":  "knot.example.com",
		"SNOT_OWNER_DID": "did:plc:abc123",
		"SNOT_USER_MAP":  "did:plc:abc123=isabel,did:plc:def456=alice",
		"SNOT_DB_DSN":    "postgres:///forgejo",
		"SNOT_REPO_ROOT": "/var/lib/forgejo/repositories",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "0.0.0.0:5555" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.StateDir != "/var/lib/snot" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.UserMap["did:plc:abc123"] != "isabel" || c.UserMap["did:plc:def456"] != "alice" {
		t.Errorf("UserMap = %v", c.UserMap)
	}
}

func TestLoadFromTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `
hostname = "knot.example.com"
owner_did = "did:plc:abc123"
db_dsn = "postgres:///forgejo"
repo_root = "/repos"
dev = true
webhook_secret = "hunter2"

[users]
"did:plc:abc123" = "isabel"
"did:plc:def456" = "alice"
`
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := loadWith(path, func() []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "knot.example.com" || c.RepoRoot != "/repos" || !c.Dev {
		t.Errorf("config = %+v", c)
	}
	if c.PlcUrl != "https://plc.directory" {
		t.Errorf("default PlcUrl lost: %q", c.PlcUrl)
	}
	if c.UserMap["did:plc:abc123"] != "isabel" || c.UserMap["did:plc:def456"] != "alice" {
		t.Errorf("UserMap = %v", c.UserMap)
	}
	if c.WebhookSecret != "hunter2" {
		t.Errorf("WebhookSecret = %q", c.WebhookSecret)
	}
}

func TestEnvOverridesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `
hostname = "from-file.example.com"
owner_did = "did:plc:abc123"
db_dsn = "postgres:///filedsn"
repo_root = "/repos"

[users]
"did:plc:abc123" = "isabel"
`
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := loadWith(path, func() []string {
		return []string{
			"SNOT_HOSTNAME=from-env.example.com",
			"SNOT_DB_DSN=postgres:///envdsn",
			"SNOT_USER_MAP=did:plc:zzz=bob",
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "from-env.example.com" {
		t.Errorf("env should override hostname: %q", c.Hostname)
	}
	if c.DbDsn != "postgres:///envdsn" {
		t.Errorf("env should override db_dsn: %q", c.DbDsn)
	}
	// env user_map merges into the file's [users] table.
	if c.UserMap["did:plc:abc123"] != "isabel" || c.UserMap["did:plc:zzz"] != "bob" {
		t.Errorf("UserMap = %v", c.UserMap)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	if _, err := loadWith("", func() []string { return nil }); err == nil {
		t.Fatal("expected error for missing required config")
	}
}
