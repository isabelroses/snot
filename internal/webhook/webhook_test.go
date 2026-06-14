package webhook

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tangled.org/core/api/tangled"
	"tangled.org/core/eventstream"
	"tangled.org/core/notifier"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/testutil"
)

func headSHA(t *testing.T, bareDir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bareDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func newHandler(t *testing.T) (*Handler, eventstream.Store, string) {
	t.Helper()
	root := testutil.FixtureRepo(t, "isabel", "demo")

	db, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.SQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RepoDids().Put("did:plc:repo1", state.RepoDidInfo{User: "isabel", Repo: "demo", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Hostname:      "knot.example.com",
		OwnerDid:      "did:plc:knotadmin",
		UserMap:       map[string]string{"did:plc:owner1": "isabel"},
		RepoRoot:      root,
		WebhookSecret: "s3cr3t",
	}
	stubStore := &testutil.StubStore{Repos: map[string]*forgejo.Repo{
		"isabel/demo": testutil.StubRepo(1, "isabel", "demo"),
	}}
	rs := &resolve.Resolver{Cfg: cfg, Store: stubStore, Rkeys: db.Rkeys(), Dids: db.RepoDids()}

	n := notifier.New()
	h := &Handler{Cfg: cfg, Resolve: rs, Store: store, Notifier: &n, Logger: slog.Default()}
	return h, store, filepath.Join(root, "isabel", "demo.git")
}

func TestWebhookEmitsRefUpdate(t *testing.T) {
	h, store, bareDir := newHandler(t)
	head := headSHA(t, bareDir)

	body, _ := json.Marshal(map[string]any{
		"ref":    "refs/heads/main",
		"before": "0000000000000000000000000000000000000000",
		"after":  head,
		"repository": map[string]any{
			"name":  "demo",
			"owner": map[string]any{"username": "isabel"},
		},
		"pusher": map[string]any{"username": "isabel"},
	})

	req := httptest.NewRequest("POST", "/hooks/forgejo", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Signature", sign("s3cr3t", body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("code = %d, body=%s", rec.Code, rec.Body)
	}

	events, err := eventstream.List(store, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Nsid != tangled.GitRefUpdateNSID {
		t.Errorf("nsid = %s", events[0].Nsid)
	}
	var ru tangled.GitRefUpdate
	if err := json.Unmarshal(events[0].EventJson, &ru); err != nil {
		t.Fatal(err)
	}
	if ru.Repo != "did:plc:repo1" || ru.Ref != "refs/heads/main" || ru.NewSha != head {
		t.Errorf("ru = %+v", ru)
	}
	if ru.CommitterDid != "did:plc:owner1" {
		t.Errorf("committer = %q, want mapped owner did", ru.CommitterDid)
	}
	if ru.OwnerDid == nil || *ru.OwnerDid != "did:plc:owner1" {
		t.Errorf("ownerDid = %v", ru.OwnerDid)
	}
	if ru.Meta == nil || !ru.Meta.IsDefaultRef {
		t.Errorf("meta missing or not default ref: %+v", ru.Meta)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	h, store, _ := newHandler(t)
	body := []byte(`{"ref":"refs/heads/main","repository":{"name":"demo","owner":{"username":"isabel"}}}`)
	req := httptest.NewRequest("POST", "/hooks/forgejo", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Signature", "deadbeef")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("code = %d", rec.Code)
	}
	if events, _ := eventstream.List(store, 0, 10); len(events) != 0 {
		t.Errorf("no event should be emitted on bad signature, got %d", len(events))
	}
}

func TestWebhookSkipsUnpublishedRepo(t *testing.T) {
	h, store, _ := newHandler(t)
	body, _ := json.Marshal(map[string]any{
		"ref":    "refs/heads/main",
		"before": "0000000000000000000000000000000000000000",
		"after":  "2222222222222222222222222222222222222222",
		"repository": map[string]any{
			"name":  "other",
			"owner": map[string]any{"username": "isabel"},
		},
		"pusher": map[string]any{"username": "isabel"},
	})
	req := httptest.NewRequest("POST", "/hooks/forgejo", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Signature", sign("s3cr3t", body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("code = %d (should ack-and-skip)", rec.Code)
	}
	if events, _ := eventstream.List(store, 0, 10); len(events) != 0 {
		t.Errorf("unpublished repo should emit nothing, got %d", len(events))
	}
}
