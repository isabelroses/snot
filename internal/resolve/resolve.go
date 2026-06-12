// Package resolve maps knot-protocol repo identifiers (ownerDid/name,
// ownerDid/rkey, or bare did:plc repo DIDs) to Forgejo repos on disk.
package resolve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/state"
)

type Resolver struct {
	Cfg   *config.Config
	Store forgejo.Store
	Rkeys *state.RkeyMap
	Dids  *state.RepoDids
	// MintDid mints a new repo DID (injected; tests use a fake).
	MintDid func(ctx context.Context) (did string, key []byte, err error)
}

// UserFor returns the Forgejo user whose repos the given DID exposes.
func (rs *Resolver) UserFor(did string) (string, bool) {
	user, ok := rs.Cfg.UserMap[did]
	return user, ok
}

// DidFor returns the DID mapped to a Forgejo user (the inverse of UserFor).
func (rs *Resolver) DidFor(user string) (string, bool) {
	for did, u := range rs.Cfg.UserMap {
		if strings.EqualFold(u, user) {
			return did, true
		}
	}
	return "", false
}

// Repo resolves an owner DID plus a repo name or record rkey.
func (rs *Resolver) Repo(ctx context.Context, ownerDid, nameOrRkey string) (*forgejo.Repo, string, error) {
	user, ok := rs.UserFor(ownerDid)
	if !ok {
		return nil, "", forgejo.ErrNotFound
	}
	name := nameOrRkey
	if mapped, ok := rs.Rkeys.RepoByRkey(nameOrRkey); ok {
		name = mapped
	}
	return rs.lookup(ctx, user, name)
}

// RepoFromParam accepts both forms the protocol uses: "ownerDid/name" and a
// bare repo DID (did:plc).
func (rs *Resolver) RepoFromParam(ctx context.Context, param string) (*forgejo.Repo, string, error) {
	if !strings.HasPrefix(param, "did:") {
		return nil, "", forgejo.ErrNotFound
	}
	if i := strings.Index(param, "/"); i >= 0 {
		return rs.Repo(ctx, param[:i], param[i+1:])
	}
	// Bare repo DID — look up in persisted mapping.
	if rs.Dids == nil {
		return nil, "", forgejo.ErrNotFound
	}
	info, ok := rs.Dids.Get(param)
	if !ok {
		return nil, "", forgejo.ErrNotFound
	}
	// Defense: require the owner DID is still mapped.
	if _, mapped := rs.DidFor(info.User); !mapped {
		return nil, "", forgejo.ErrNotFound
	}
	return rs.lookup(ctx, info.User, info.Repo)
}

// EnsureRepoDid returns the repo's did:plc, minting and persisting one on
// first use.
func (rs *Resolver) EnsureRepoDid(ctx context.Context, repo *forgejo.Repo) (string, error) {
	if rs.Dids != nil {
		if did, ok := rs.Dids.ByRepo(repo.OwnerName, repo.Name); ok {
			return did, nil
		}
	}
	did, key, err := rs.MintDid(ctx)
	if err != nil {
		return "", fmt.Errorf("minting repo DID: %w", err)
	}
	if rs.Dids != nil {
		if err := rs.Dids.Put(did, state.RepoDidInfo{User: repo.OwnerName, Repo: repo.Name, Key: key}); err != nil {
			return "", err
		}
	}
	return did, nil
}

func (rs *Resolver) lookup(ctx context.Context, user, name string) (*forgejo.Repo, string, error) {
	repo, err := rs.Store.ResolveRepo(ctx, user, name)
	if err != nil {
		return nil, "", err
	}
	path, err := securejoin.SecureJoin(rs.Cfg.RepoRoot, filepath.Join(repo.OwnerName, repo.Name+".git"))
	if err != nil {
		return nil, "", forgejo.ErrNotFound
	}
	if _, err := os.Stat(path); err != nil {
		return nil, "", forgejo.ErrNotFound
	}
	return repo, path, nil
}
