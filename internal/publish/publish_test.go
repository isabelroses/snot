package publish_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/events"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/isabelroses/snot/internal/atrepo"
	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/pds"
	"github.com/isabelroses/snot/internal/publish"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/testutil"
)

const (
	repoDid  = "did:plc:yzpsv6y5d6dy4mwiwmrgagah"
	ownerDid = "did:plc:owner123"
	collab   = "did:plc:collab456"
	hostname = "knot.example.com"
)

type world struct {
	pub     *publish.Publisher
	store   *atrepo.Store
	db      *state.DB
	stub    *testutil.StubStore
	root    string
	repoKey *atcrypto.PrivateKeyK256
	srv     *httptest.Server
	xc      *xrpc.Client
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := testutil.FixtureRepo(t, "isabel", "demo")
	db, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repoKey, _ := atcrypto.GeneratePrivateKeyK256()
	if err := db.RepoDids().Put(repoDid, state.RepoDidInfo{User: "isabel", Repo: "demo", Key: repoKey.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if err := db.ACL().SeedMembers([]string{ownerDid}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Hostname: hostname, OwnerDid: ownerDid, UserMap: map[string]string{ownerDid: "isabel"}, RepoRoot: root}
	stub := &testutil.StubStore{Repos: map[string]*forgejo.Repo{"isabel/demo": testutil.StubRepo(1, "isabel", "demo")}}
	rs := &resolve.Resolver{Cfg: cfg, Store: stub, Rkeys: db.Rkeys(), Dids: db.RepoDids()}

	store, err := atrepo.New(db.Gorm())
	if err != nil {
		t.Fatal(err)
	}
	knotKey, err := db.KnotKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := &publish.Publisher{
		Store: store, Resolve: rs, ACL: db.ACL(),
		KnotDid: "did:web:" + hostname, KnotKey: knotKey, Logger: slog.Default(),
	}
	h := &pds.Handler{Store: store, Hostname: hostname, BaseURL: "https://" + hostname, KnotKey: knotKey, PlcUrl: "http://127.0.0.1:1", Logger: slog.Default()}
	r := chi.NewRouter()
	h.WellKnown(r)
	r.Route("/xrpc", h.Routes)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &world{pub: pub, store: store, db: db, stub: stub, root: root, repoKey: repoKey, srv: srv, xc: &xrpc.Client{Host: srv.URL}}
}

func (w *world) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Join(w.root, "isabel", "demo.git")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPublishesRepoRecordsReadableByStockClients(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	if err := w.db.ACL().AddCollaborator(repoDid, collab, ownerDid); err != nil {
		t.Fatal(err)
	}
	// a Forgejo PR ref must stay out of the refs snapshot
	head := w.git(t, "rev-parse", "main")
	w.git(t, "update-ref", "refs/pull/1/head", head)
	w.git(t, "update-ref", "refs/tags/v1", head)

	if err := w.pub.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}

	latest, err := comatproto.SyncGetLatestCommit(ctx, w.xc, repoDid)
	if err != nil {
		t.Fatalf("getLatestCommit: %v", err)
	}

	proof, err := comatproto.SyncGetRecord(ctx, w.xc, "org.tangled.git.refs", repoDid, "self")
	if err != nil {
		t.Fatalf("sync.getRecord: %v", err)
	}
	commit, r, err := repo.LoadRepoFromCAR(ctx, bytes.NewReader(proof))
	if err != nil {
		t.Fatalf("proof: %v", err)
	}
	pubKey, _ := w.repoKey.PublicKey()
	if err := commit.VerifySignature(pubKey); err != nil {
		t.Fatalf("commit not signed by the repo DID's key: %v", err)
	}
	if commit.Rev != latest.Rev {
		t.Errorf("proof rev %s != latest %s", commit.Rev, latest.Rev)
	}
	raw, _, err := r.GetRecordBytes(ctx, "org.tangled.git.refs", "self")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := atdata.UnmarshalCBOR(raw)
	if val["defaultBranch"] != "main" {
		t.Errorf("defaultBranch = %v", val["defaultBranch"])
	}
	want := head + " refs/heads/main\n" + head + " refs/tags/v1\n"
	if got := string(val["refs"].(atdata.Bytes)); got != want {
		t.Errorf("refs = %q, want %q", got, want)
	}

	blocks, err := comatproto.SyncGetBlocks(ctx, w.xc, []string{commit.Data.String()}, repoDid)
	if err != nil {
		t.Fatalf("getBlocks: %v", err)
	}
	if _, _, err := repo.LoadRepoFromCAR(ctx, bytes.NewReader(blocks)); err != nil {
		t.Fatalf("getBlocks CAR: %v", err)
	}

	repos, err := comatproto.SyncListRepos(ctx, w.xc, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawRepo, sawKnot bool
	for _, row := range repos.Repos {
		sawRepo = sawRepo || row.Did == repoDid
		sawKnot = sawKnot || row.Did == "did:web:"+hostname
	}
	if !sawRepo || !sawKnot {
		t.Errorf("listRepos misses repo=%v knot=%v", sawRepo, sawKnot)
	}

	// raw LexDo: indigo's typed helpers only decode lexicons they know
	var man struct {
		Value struct {
			Declaration struct{ Owner, Name string } `json:"declaration"`
			Type        string                       `json:"$type"`
		} `json:"value"`
	}
	if err := w.xc.LexDo(ctx, xrpc.Query, "", "com.atproto.repo.getRecord", map[string]any{
		"repo": repoDid, "collection": "org.tangled.repo.manifest", "rkey": "self",
	}, nil, &man); err != nil {
		t.Fatalf("repo.getRecord: %v", err)
	}
	if man.Value.Declaration.Owner != ownerDid || man.Value.Declaration.Name != "demo" || man.Value.Type != "org.tangled.repo.manifest" {
		t.Errorf("manifest = %+v", man.Value)
	}

	type listed struct {
		Records []struct {
			Uri   string `json:"uri"`
			Value struct {
				CreatedAt string `json:"createdAt"`
			} `json:"value"`
		} `json:"records"`
	}
	var recs listed
	if err := w.xc.LexDo(ctx, xrpc.Query, "", "com.atproto.repo.listRecords", map[string]any{
		"repo": repoDid, "collection": "org.tangled.graph.collaborator",
	}, nil, &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs.Records) != 1 || recs.Records[0].Uri != "at://"+repoDid+"/org.tangled.graph.collaborator/"+collab {
		t.Errorf("collaborators = %+v", recs.Records)
	}

	var members listed
	if err := w.xc.LexDo(ctx, xrpc.Query, "", "com.atproto.repo.listRecords", map[string]any{
		"repo": hostname, "collection": "org.tangled.knot.member",
	}, nil, &members); err != nil {
		t.Fatalf("listRecords on knot by handle: %v", err)
	}
	if len(members.Records) != 1 || !strings.HasSuffix(members.Records[0].Uri, "/"+ownerDid) {
		t.Errorf("members = %+v", members.Records)
	}

	desc, err := comatproto.RepoDescribeRepo(ctx, w.xc, repoDid)
	if err != nil {
		t.Fatalf("describeRepo: %v", err)
	}
	if len(desc.Collections) != 3 {
		t.Errorf("collections = %v", desc.Collections)
	}
	status, err := comatproto.SyncGetRepoStatus(ctx, w.xc, repoDid)
	if err != nil || !status.Active || status.Rev == nil || *status.Rev != latest.Rev {
		t.Errorf("getRepoStatus = %+v, %v", status, err)
	}
	resolved, err := comatproto.IdentityResolveHandle(ctx, w.xc, hostname)
	if err != nil || resolved.Did != "did:web:"+hostname {
		t.Errorf("resolveHandle = %+v, %v", resolved, err)
	}
}

type frame struct {
	typ     string
	commit  *comatproto.SyncSubscribeRepos_Commit
	account *comatproto.SyncSubscribeRepos_Account
}

func next(t *testing.T, conn *websocket.Conn) frame {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	var h events.EventHeader
	if err := h.UnmarshalCBOR(r); err != nil {
		t.Fatal(err)
	}
	f := frame{typ: h.MsgType}
	switch h.MsgType {
	case "#commit":
		f.commit = &comatproto.SyncSubscribeRepos_Commit{}
		err = f.commit.UnmarshalCBOR(r)
	case "#account":
		f.account = &comatproto.SyncSubscribeRepos_Account{}
		err = f.account.UnmarshalCBOR(r)
	}
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestFirehoseCarriesPushesAndUnpublish follows a push and then a delete
// across the live firehose, as hydrant would see them.
func TestFirehoseCarriesPushesAndUnpublish(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	if err := w.pub.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}

	url := "ws" + strings.TrimPrefix(w.srv.URL, "http") + "/xrpc/com.atproto.sync.subscribeRepos"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// give the server a moment to subscribe before the live write
	time.Sleep(50 * time.Millisecond)

	w.git(t, "branch", "feature", "main")
	if err := w.pub.SyncRepo(ctx, repoDid); err != nil {
		t.Fatal(err)
	}
	f := next(t, conn)
	if f.commit == nil || f.commit.Repo != repoDid {
		t.Fatalf("want a repo commit, got %+v", f)
	}
	if len(f.commit.Ops) != 1 || f.commit.Ops[0].Action != "update" || f.commit.Ops[0].Path != "org.tangled.git.refs/self" {
		t.Fatalf("ops = %+v", f.commit.Ops)
	}
	if _, err := repo.VerifyCommitMessage(ctx, f.commit); err != nil {
		t.Fatalf("inductive verification: %v", err)
	}

	// a repo that turns private in Forgejo goes dark, then returns
	delete(w.stub.Repos, "isabel/demo")
	if err := w.pub.SyncRepo(ctx, repoDid); err != nil {
		t.Fatal(err)
	}
	if f := next(t, conn); f.account == nil || f.account.Active || *f.account.Status != "deactivated" {
		t.Fatalf("want deactivation, got %+v", f)
	}
	if _, err := comatproto.SyncGetLatestCommit(ctx, w.xc, repoDid); err == nil {
		t.Error("a deactivated repo should not serve commits")
	}
	w.stub.Repos["isabel/demo"] = testutil.StubRepo(1, "isabel", "demo")
	if err := w.pub.SyncRepo(ctx, repoDid); err != nil {
		t.Fatal(err)
	}
	if f := next(t, conn); f.account == nil || !f.account.Active {
		t.Fatalf("want reactivation, got %+v", f)
	}

	if err := w.db.RepoDids().MarkDeleted(repoDid); err != nil {
		t.Fatal(err)
	}
	if err := w.pub.SyncRepo(ctx, repoDid); err != nil {
		t.Fatal(err)
	}
	if f := next(t, conn); f.account == nil || f.account.Active || *f.account.Status != "deleted" {
		t.Fatalf("want deletion, got %+v", f)
	}

	// a cursor replays history from the start
	replay, _, err := websocket.DefaultDialer.Dial(url+"?cursor=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if f := next(t, replay); f.typ != "#identity" {
		t.Errorf("replay starts with %s, want #identity", f.typ)
	}
}

func TestKnotDidDocument(t *testing.T) {
	w := newWorld(t)
	resp, err := w.srv.Client().Get(w.srv.URL + "/.well-known/did.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	body := buf.String()
	for _, want := range []string{`"id":"did:web:` + hostname + `"`, `#tangled_knot`, `#atproto_pds`, `publicKeyMultibase`} {
		if !strings.Contains(body, want) {
			t.Errorf("did.json lacks %s: %s", want, body)
		}
	}
}
