// forgejo/forgejo.go
package forgejo

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned for absent, private, or wrong-owner repos alike,
// so callers cannot distinguish private repos from missing ones.
var ErrNotFound = errors.New("forgejo: not found")

type Repo struct {
	ID            int64
	Name          string // lower_name
	OwnerName     string // lowercased owner name
	Description   string
	DefaultBranch string
	CreatedAt     time.Time
}

type PublicKey struct {
	Key       string
	CreatedAt time.Time
}

type Store interface {
	// ResolveRepo returns the repo iff it exists, is public, and belongs to user.
	ResolveRepo(ctx context.Context, user, name string) (*Repo, error)
	Languages(ctx context.Context, repoID int64) (map[string]int64, error)
	PublicKeys(ctx context.Context, user string) ([]PublicKey, error)
	// ListRepos returns all public repos belonging to user, ordered by name.
	ListRepos(ctx context.Context, user string) ([]Repo, error)
}
