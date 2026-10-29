package config

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Hostname   string
	ListenAddr string

	// OwnerDid identifies the knot's administrator alone (sh.tangled.owner,
	// knot registration). Repo ownership is governed by UserMap.
	OwnerDid string

	// UserMap maps repo-owner DIDs to the Forgejo users whose repos they
	// expose. In TOML this is the [users] table; SNOT_USER_MAP
	// ("did:plc:abc=isabel,did:plc:def=alice") overrides/extends it.
	UserMap map[string]string

	DbDsn         string
	RepoRoot      string
	PushRemote    string
	WebhookSecret string // SNOT_WEBHOOK_SECRET / webhook_secret; verifies Forgejo push webhooks
	StateDir      string
	PlcUrl        string
	Dev           bool
}

// Load reads configuration from the TOML file at path (optional; skipped if
// absent), with SNOT_* environment variables overriding individual keys.
func Load(ctx context.Context, path string) (*Config, error) {
	return loadWith(path, nil)
}

// loadWith layers defaults < TOML file < SNOT_* env. environ defaults to
// os.Environ when nil.
func loadWith(path string, environ func() []string) (*Config, error) {
	k := koanf.New(".")

	_ = k.Load(confmap.Provider(map[string]any{
		"listen_addr": "0.0.0.0:5555",
		"state_dir":   "/var/lib/snot",
		"plc_url":     "https://plc.directory",
		"dev":         "false",
	}, "."), nil)

	if path != "" {
		if err := k.Load(file.Provider(path), toml.Parser()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("loading config %s: %w", path, err)
		}
	}

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

	users := k.StringMap("users")
	if users == nil {
		users = map[string]string{}
	}
	envUsers, err := parseUserMap(k.String("user_map"))
	if err != nil {
		return nil, err
	}
	for did, user := range envUsers {
		users[did] = user
	}

	dev, _ := strconv.ParseBool(k.String("dev"))
	cfg := &Config{
		Hostname:      k.String("hostname"),
		ListenAddr:    k.String("listen_addr"),
		OwnerDid:      k.String("owner_did"),
		UserMap:       users,
		DbDsn:         k.String("db_dsn"),
		RepoRoot:      k.String("repo_root"),
		PushRemote:    k.String("push_remote"),
		WebhookSecret: k.String("webhook_secret"),
		StateDir:      k.String("state_dir"),
		PlcUrl:        k.String("plc_url"),
		Dev:           dev,
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseUserMap parses the SNOT_USER_MAP override form
// "did:plc:abc=isabel,did:plc:def=alice". A malformed entry is an error
// rather than skipped, since a dropped mapping silently unpublishes repos.
func parseUserMap(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		did, user, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("SNOT_USER_MAP entry %q is not did=user", pair)
		}
		m[strings.TrimSpace(did)] = strings.TrimSpace(user)
	}
	return m, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.Hostname == "" {
		missing = append(missing, "hostname / SNOT_HOSTNAME")
	}
	if c.OwnerDid == "" {
		missing = append(missing, "owner_did / SNOT_OWNER_DID")
	}
	if len(c.UserMap) == 0 {
		missing = append(missing, "[users] / SNOT_USER_MAP")
	}
	if c.DbDsn == "" {
		missing = append(missing, "db_dsn / SNOT_DB_DSN")
	}
	if c.RepoRoot == "" {
		missing = append(missing, "repo_root / SNOT_REPO_ROOT")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}

	// hostname is interpolated into URLs and the did:web service-auth audience
	if strings.ContainsAny(c.Hostname, "/?#@ ") || strings.Contains(c.Hostname, "://") {
		return fmt.Errorf("hostname %q must be a bare host[:port], not a URL", c.Hostname)
	}
	if _, err := syntax.ParseDID(c.OwnerDid); err != nil {
		return fmt.Errorf("owner_did: %w", err)
	}
	for did, user := range c.UserMap {
		if _, err := syntax.ParseDID(did); err != nil {
			return fmt.Errorf("users: %w", err)
		}
		if user == "" {
			return fmt.Errorf("users: %s maps to an empty Forgejo user", did)
		}
	}
	if u, err := url.Parse(c.PlcUrl); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("plc_url %q must be an http(s) URL", c.PlcUrl)
	}
	return nil
}
