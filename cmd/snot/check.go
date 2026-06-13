package main

import (
	"context"
	"fmt"
	"os"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
)

type CheckCmd struct{}

func (c *CheckCmd) Run(cli *CLI) error {
	ctx := context.Background()

	cfg, err := config.Load(ctx, cli.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	fmt.Printf("config        ok (hostname %s, knot owner %s, %d mapped users)\n", cfg.Hostname, cfg.OwnerDid, len(cfg.UserMap))

	store, err := forgejo.NewPostgres(ctx, cfg.DbDsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer store.Close()

	for did, user := range cfg.UserMap {
		repos, err := store.ListRepos(ctx, user)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		fmt.Printf("database      ok (%d public repos for %s = %s)\n", len(repos), user, did)
	}

	fi, err := os.Stat(cfg.RepoRoot)
	if err != nil {
		return fmt.Errorf("repo root %s: %w", cfg.RepoRoot, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("repo root %s: not a directory", cfg.RepoRoot)
	}
	fmt.Printf("repo root     ok (%s)\n", cfg.RepoRoot)

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("state dir %s: %w", cfg.StateDir, err)
	}
	fmt.Printf("state dir     ok (%s)\n", cfg.StateDir)

	return nil
}
