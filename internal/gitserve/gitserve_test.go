package gitserve

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/testutil"
)

func newServer(t *testing.T) *httptest.Server {
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
		Hostname:   "knot.example.com",
		OwnerDid:   "did:plc:knotadmin",
		UserMap:    map[string]string{"did:plc:owner123": "isabel"},
		RepoRoot:   root,
		PushRemote: "git@forge.example.com",
	}
	store := &testutil.StubStore{Repos: map[string]*forgejo.Repo{
		"isabel/demo": testutil.StubRepo(1, "isabel", "demo"),
	}}
	h := &Handler{
		Cfg:     cfg,
		Resolve: &resolve.Resolver{Cfg: cfg, Store: store, Rkeys: rkeys, Dids: dids},
		Logger:  slog.Default(),
		// Resolver (handle redirect) nil in tests.
	}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func TestCloneByDidName(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", srv.URL+"/did:plc:owner123/demo", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Error("README.md missing in clone")
	}
}

func TestCloneByRepoDid(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone",
		srv.URL+"/did:plc:repo123", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone by repoDid failed: %v\n%s", err, out)
	}
}

func TestCloneUnknownRepo404s(t *testing.T) {
	srv := newServer(t)
	res, err := http.Get(srv.URL + "/did:plc:owner123/nope/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("code = %d", res.StatusCode)
	}
}

func TestPushRejected(t *testing.T) {
	srv := newServer(t)
	res, err := http.Get(srv.URL + "/did:plc:owner123/demo/info/refs?service=git-receive-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("code = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "git@forge.example.com") {
		t.Errorf("push hint missing: %s", body)
	}
}
