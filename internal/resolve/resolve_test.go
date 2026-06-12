package resolve

import (
	"context"
	"errors"
	"testing"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/testutil"
)

func newResolver(t *testing.T) *Resolver {
	t.Helper()
	root := testutil.FixtureRepo(t, "isabel", "demo")
	rkeys, err := state.LoadRkeyMap(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := rkeys.Put("3l5tidrkey", "demo"); err != nil {
		t.Fatal(err)
	}
	dids, err := state.LoadRepoDids(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dids.Put("did:plc:repo123", state.RepoDidInfo{User: "isabel", Repo: "demo", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	return &Resolver{
		Cfg: &config.Config{
			Hostname: "knot.example.com",
			OwnerDid: "did:plc:knotadmin",
			UserMap:  map[string]string{"did:plc:owner123": "isabel"},
			RepoRoot: root,
		},
		Store: &testutil.StubStore{Repos: map[string]*forgejo.Repo{
			"isabel/demo": testutil.StubRepo(1, "isabel", "demo"),
		}},
		Rkeys: rkeys,
		Dids:  dids,
		MintDid: func(ctx context.Context) (string, []byte, error) {
			return "did:plc:minted1", []byte("k"), nil
		},
	}
}

func TestRepoByName(t *testing.T) {
	rs := newResolver(t)
	repo, path, err := rs.Repo(context.Background(), "did:plc:owner123", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "demo" || path == "" {
		t.Errorf("repo=%+v path=%q", repo, path)
	}
}

func TestRepoByRkey(t *testing.T) {
	rs := newResolver(t)
	repo, _, err := rs.Repo(context.Background(), "did:plc:owner123", "3l5tidrkey")
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "demo" {
		t.Errorf("repo=%+v", repo)
	}
}

func TestRepoWrongOwner(t *testing.T) {
	rs := newResolver(t)
	_, _, err := rs.Repo(context.Background(), "did:plc:someoneelse", "demo")
	if !errors.Is(err, forgejo.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRepoFromParamForms(t *testing.T) {
	rs := newResolver(t)
	ctx := context.Background()

	for _, param := range []string{
		"did:plc:owner123/demo",
		"did:plc:owner123/3l5tidrkey",
		"did:plc:repo123", // seeded bare repo DID
	} {
		if _, _, err := rs.RepoFromParam(ctx, param); err != nil {
			t.Errorf("RepoFromParam(%q) = %v", param, err)
		}
	}

	for _, param := range []string{
		"",
		"notadid/demo",
		"did:plc:owner123/private",
		"did:plc:owner123/../../etc",
		"did:plc:unknowndid",     // bare DID not in Dids map
		"did:plc:knotadmin/demo", // knot admin DID owns no repos
	} {
		if _, _, err := rs.RepoFromParam(ctx, param); !errors.Is(err, forgejo.ErrNotFound) {
			t.Errorf("RepoFromParam(%q) = %v, want ErrNotFound", param, err)
		}
	}
}

func TestEnsureRepoDid_SeededReturnsExisting(t *testing.T) {
	rs := newResolver(t)
	repo := testutil.StubRepo(1, "isabel", "demo")

	did, err := rs.EnsureRepoDid(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if did != "did:plc:repo123" {
		t.Errorf("EnsureRepoDid = %q, want did:plc:repo123 (seeded)", did)
	}

	// Second call must be stable (no mint called).
	did2, err := rs.EnsureRepoDid(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if did2 != did {
		t.Errorf("second call returned different DID: %q", did2)
	}
}

func TestEnsureRepoDid_MintsAndPersists(t *testing.T) {
	root := testutil.FixtureRepo(t, "isabel", "other")
	rkeys, _ := state.LoadRkeyMap(t.TempDir())
	dids, _ := state.LoadRepoDids(t.TempDir())
	mintCount := 0
	rs := &Resolver{
		Cfg: &config.Config{
			Hostname: "knot.example.com",
			OwnerDid: "did:plc:knotadmin",
			UserMap:  map[string]string{"did:plc:owner123": "isabel"},
			RepoRoot: root,
		},
		Store: &testutil.StubStore{Repos: map[string]*forgejo.Repo{
			"isabel/other": testutil.StubRepo(2, "isabel", "other"),
		}},
		Rkeys: rkeys,
		Dids:  dids,
		MintDid: func(ctx context.Context) (string, []byte, error) {
			mintCount++
			return "did:plc:minted1", []byte("newkey"), nil
		},
	}

	repo := testutil.StubRepo(2, "isabel", "other")

	did, err := rs.EnsureRepoDid(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if did != "did:plc:minted1" {
		t.Errorf("EnsureRepoDid = %q, want did:plc:minted1", did)
	}
	if mintCount != 1 {
		t.Errorf("MintDid called %d times, want 1", mintCount)
	}

	// Verify persisted.
	if _, ok := dids.Get("did:plc:minted1"); !ok {
		t.Error("minted DID not persisted in Dids")
	}

	// Second call must not remint.
	did2, err := rs.EnsureRepoDid(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if did2 != did {
		t.Errorf("second EnsureRepoDid = %q, want %q", did2, did)
	}
	if mintCount != 1 {
		t.Errorf("MintDid called again on second EnsureRepoDid (total=%d)", mintCount)
	}
}
