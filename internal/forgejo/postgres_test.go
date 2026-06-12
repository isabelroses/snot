// forgejo/postgres_test.go
package forgejo

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Run against a real Forgejo database:
//
//	TEST_DATABASE_URL=postgres://...  TEST_FORGEJO_USER=isabel TEST_FORGEJO_REPO=somerepo go test ./forgejo/
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	user := os.Getenv("TEST_FORGEJO_USER")
	repoName := os.Getenv("TEST_FORGEJO_REPO")

	ctx := context.Background()
	p, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	repo, err := p.ResolveRepo(ctx, user, repoName)
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if repo.Name == "" || repo.OwnerName == "" || repo.DefaultBranch == "" {
		t.Errorf("incomplete repo: %+v", repo)
	}

	if _, err := p.ResolveRepo(ctx, user, "definitely-does-not-exist-xyz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	if _, err := p.Languages(ctx, repo.ID); err != nil {
		t.Errorf("Languages: %v", err)
	}
	if _, err := p.PublicKeys(ctx, user); err != nil {
		t.Errorf("PublicKeys: %v", err)
	}
	if _, err := p.ListRepos(ctx, user); err != nil {
		t.Errorf("ListRepos: %v", err)
	}
}
