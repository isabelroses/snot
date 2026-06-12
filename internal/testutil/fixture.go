// internal/testutil/fixture.go
package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/isabelroses/snot/internal/forgejo"
)

// StubStore implements forgejo.Store from a fixed set of repos.
type StubStore struct {
	Repos map[string]*forgejo.Repo // key: "user/name" (lowercase)
	Langs map[int64]map[string]int64
	Keys  []forgejo.PublicKey
}

func (s *StubStore) ResolveRepo(_ context.Context, user, name string) (*forgejo.Repo, error) {
	r, ok := s.Repos[user+"/"+name]
	if !ok {
		return nil, forgejo.ErrNotFound
	}
	return r, nil
}

func (s *StubStore) Languages(_ context.Context, repoID int64) (map[string]int64, error) {
	return s.Langs[repoID], nil
}

func (s *StubStore) PublicKeys(_ context.Context, _ string) ([]forgejo.PublicKey, error) {
	return s.Keys, nil
}

func (s *StubStore) ListRepos(_ context.Context, _ string) ([]forgejo.Repo, error) {
	var repos []forgejo.Repo
	for _, r := range s.Repos {
		repos = append(repos, *r)
	}
	// Match the postgres implementation's ORDER BY lower_name.
	slices.SortFunc(repos, func(a, b forgejo.Repo) int {
		return strings.Compare(a.Name, b.Name)
	})
	return repos, nil
}

// FixtureRepo creates {root}/{owner}/{name}.git as a bare repo containing one
// commit (README.md on branch main) and returns the repo root directory.
func FixtureRepo(t *testing.T, owner, name string) (root string) {
	t.Helper()
	root = t.TempDir()
	work := filepath.Join(t.TempDir(), "work")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
			"GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	run(work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-m", "initial commit")

	bare := filepath.Join(root, owner, name+".git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	run(work, "clone", "--bare", ".", bare)
	return root
}

// StubRepo is a convenience forgejo.Repo for fixtures.
func StubRepo(id int64, owner, name string) *forgejo.Repo {
	return &forgejo.Repo{
		ID:            id,
		Name:          name,
		OwnerName:     owner,
		Description:   "a fixture repo",
		DefaultBranch: "main",
		CreatedAt:     time.Unix(1700000000, 0),
	}
}
