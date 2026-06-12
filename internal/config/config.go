package config

import (
	"context"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Hostname   string `env:"SNOT_HOSTNAME, required"`
	ListenAddr string `env:"SNOT_LISTEN_ADDR, default=0.0.0.0:5555"`

	// OwnerDid identifies the knot's administrator alone (sh.tangled.owner,
	// knot registration). Repo ownership is governed by UserMap.
	OwnerDid string `env:"SNOT_OWNER_DID, required"`

	// UserMap maps repo-owner DIDs to the Forgejo users whose repos they
	// expose, e.g. SNOT_USER_MAP="did:plc:abc=isabel,did:plc:def=alice".
	UserMap map[string]string `env:"SNOT_USER_MAP, required, separator=="`

	DbDsn      string `env:"SNOT_DB_DSN, required"`
	RepoRoot   string `env:"SNOT_REPO_ROOT, required"`
	PushRemote string `env:"SNOT_PUSH_REMOTE"`
	StateDir   string `env:"SNOT_STATE_DIR, default=/var/lib/snot"`
	PlcUrl     string `env:"SNOT_PLC_URL, default=https://plc.directory"`
	Dev        bool   `env:"SNOT_DEV, default=false"`
}

func Load(ctx context.Context) (*Config, error) {
	var c Config
	if err := envconfig.Process(ctx, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
