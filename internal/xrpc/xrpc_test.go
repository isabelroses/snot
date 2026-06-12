package xrpc

import (
	"context"
	"net/http/httptest"
	"testing"

	"log/slog"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/testutil"
)

// newXrpc builds an Xrpc over a fixture repo; reused by later handler tests.
func newXrpc(t *testing.T) *Xrpc {
	t.Helper()
	root := testutil.FixtureRepo(t, "isabel", "demo")
	rkeys, err := state.LoadRkeyMap(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dids, err := state.LoadRepoDids(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dids.Put("did:plc:repo123", state.RepoDidInfo{User: "isabel", Repo: "demo", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Hostname: "knot.example.com",
		OwnerDid: "did:plc:knotadmin",
		UserMap:  map[string]string{"did:plc:owner123": "isabel"},
		RepoRoot: root,
	}
	store := &testutil.StubStore{
		Repos: map[string]*forgejo.Repo{
			"isabel/demo": testutil.StubRepo(1, "isabel", "demo"),
		},
		Langs: map[int64]map[string]int64{1: {"Go": 1000, "Nix": 500}},
		Keys:  []forgejo.PublicKey{{Key: "ssh-ed25519 AAAA test"}},
	}
	rs := &resolve.Resolver{
		Cfg:   cfg,
		Store: store,
		Rkeys: rkeys,
		Dids:  dids,
		MintDid: func(ctx context.Context) (string, []byte, error) {
			return "did:plc:minted1", []byte("k"), nil
		},
	}
	return &Xrpc{
		Cfg:     cfg,
		Store:   store,
		Resolve: rs,
		Rkeys:   rkeys,
		Logger:  slog.Default(),
	}
}

func TestParseRepoParam(t *testing.T) {
	x := newXrpc(t)
	if _, err := x.parseRepoParam("did:plc:owner123/demo"); err != nil {
		t.Errorf("did/name form: %v", err)
	}
	if _, err := x.parseRepoParam("did:plc:repo123"); err != nil {
		t.Errorf("bare repoDid form: %v", err)
	}
	if _, err := x.parseRepoParam("did:plc:owner123/nope"); err == nil {
		t.Error("expected error for unknown repo")
	}
	if _, err := x.parseRepoParam("plainstring"); err == nil {
		t.Error("expected error for non-DID")
	}
}

func TestRouterNotImplemented(t *testing.T) {
	x := newXrpc(t)
	srv := httptest.NewServer(x.Router())
	defer srv.Close()

	res, err := srv.Client().Post(srv.URL+"/sh.tangled.repo.delete", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 501 {
		t.Errorf("delete status = %d, want 501", res.StatusCode)
	}
}
