package main

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/state"
)

type ReposCmd struct{}

func (c *ReposCmd) Run(cli *CLI) error {
	ctx := context.Background()

	cfg, err := config.Load(ctx, cli.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	store, err := forgejo.NewPostgres(ctx, cfg.DbDsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer store.Close()

	dids, err := state.LoadRepoDids(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("state (repodids): %w", err)
	}

	userDids := slices.Sorted(maps.Keys(cfg.UserMap))
	for _, ownerDid := range userDids {
		repos, err := store.ListRepos(ctx, cfg.UserMap[ownerDid])
		if err != nil {
			return err
		}
		for _, r := range repos {
			did, ok := dids.ByRepo(r.OwnerName, r.Name)
			if !ok {
				did = "(not yet published)"
			}
			fmt.Printf("%-30s %s\n", r.OwnerName+"/"+r.Name, did)
		}
	}
	return nil
}
