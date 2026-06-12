package config

import (
	"context"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func load(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	var c Config
	err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &c,
		Lookuper: envconfig.MapLookuper(env),
	})
	return &c, err
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(t, map[string]string{
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
	if c.PlcUrl != "https://plc.directory" {
		t.Errorf("PlcUrl = %q", c.PlcUrl)
	}
	if c.UserMap["did:plc:abc123"] != "isabel" || c.UserMap["did:plc:def456"] != "alice" {
		t.Errorf("UserMap = %v", c.UserMap)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	if _, err := load(t, map[string]string{}); err == nil {
		t.Fatal("expected error for missing required vars")
	}
}
