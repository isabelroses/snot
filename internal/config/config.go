package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Hostname   string
	ListenAddr string

	// OwnerDid identifies the knot's administrator alone (sh.tangled.owner,
	// knot registration). Repo ownership is governed by UserMap.
	OwnerDid string

	// UserMap maps repo-owner DIDs to the Forgejo users whose repos they
	// expose, parsed from SNOT_USER_MAP="did:plc:abc=isabel,did:plc:def=alice".
	UserMap map[string]string

	DbDsn      string
	RepoRoot   string
	PushRemote string
	StateDir   string
	PlcUrl     string
	Dev        bool
}

// Load reads configuration from SNOT_* environment variables.
func Load(ctx context.Context) (*Config, error) {
	return loadWith(nil)
}

// loadWith builds the config from the given environ source (nil = os.Environ),
// applying defaults first and SNOT_* env vars on top.
func loadWith(environ func() []string) (*Config, error) {
	k := koanf.New(".")

	_ = k.Load(confmap.Provider(map[string]any{
		"listen_addr": "0.0.0.0:5555",
		"state_dir":   "/var/lib/snot",
		"plc_url":     "https://plc.directory",
		"dev":         "false",
	}, "."), nil)

	err := k.Load(env.Provider(".", env.Opt{
		Prefix:      "SNOT_",
		EnvironFunc: environ,
		TransformFunc: func(key, val string) (string, any) {
			return strings.ToLower(strings.TrimPrefix(key, "SNOT_")), val
		},
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("loading env: %w", err)
	}

	dev, _ := strconv.ParseBool(k.String("dev"))
	cfg := &Config{
		Hostname:   k.String("hostname"),
		ListenAddr: k.String("listen_addr"),
		OwnerDid:   k.String("owner_did"),
		UserMap:    parseUserMap(k.String("user_map")),
		DbDsn:      k.String("db_dsn"),
		RepoRoot:   k.String("repo_root"),
		PushRemote: k.String("push_remote"),
		StateDir:   k.String("state_dir"),
		PlcUrl:     k.String("plc_url"),
		Dev:        dev,
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseUserMap(s string) map[string]string {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		did, user, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(did)] = strings.TrimSpace(user)
	}
	return m
}

func (c *Config) validate() error {
	var missing []string
	if c.Hostname == "" {
		missing = append(missing, "SNOT_HOSTNAME")
	}
	if c.OwnerDid == "" {
		missing = append(missing, "SNOT_OWNER_DID")
	}
	if len(c.UserMap) == 0 {
		missing = append(missing, "SNOT_USER_MAP")
	}
	if c.DbDsn == "" {
		missing = append(missing, "SNOT_DB_DSN")
	}
	if c.RepoRoot == "" {
		missing = append(missing, "SNOT_REPO_ROOT")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
