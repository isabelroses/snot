package atrepo

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/events"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testDid = "did:plc:yzpsv6y5d6dy4mwiwmrgagah"

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newKey(t *testing.T) *atcrypto.PrivateKeyK256 {
	t.Helper()
	k, err := atcrypto.GeneratePrivateKeyK256()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

type decoded struct {
	typ     string
	commit  *comatproto.SyncSubscribeRepos_Commit
	sync    *comatproto.SyncSubscribeRepos_Sync
	ident   *comatproto.SyncSubscribeRepos_Identity
	account *comatproto.SyncSubscribeRepos_Account
}

func decode(t *testing.T, f Frame) decoded {
	t.Helper()
	r := bytes.NewReader(f.Data)
	var h events.EventHeader
	if err := h.UnmarshalCBOR(r); err != nil {
		t.Fatal(err)
	}
	d := decoded{typ: h.MsgType}
	var err error
	switch h.MsgType {
	case "#commit":
		d.commit = &comatproto.SyncSubscribeRepos_Commit{}
		err = d.commit.UnmarshalCBOR(r)
	case "#sync":
		d.sync = &comatproto.SyncSubscribeRepos_Sync{}
		err = d.sync.UnmarshalCBOR(r)
	case "#identity":
		d.ident = &comatproto.SyncSubscribeRepos_Identity{}
		err = d.ident.UnmarshalCBOR(r)
	case "#account":
		d.account = &comatproto.SyncSubscribeRepos_Account{}
		err = d.account.UnmarshalCBOR(r)
	default:
		t.Fatalf("unexpected frame %q", h.MsgType)
	}
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 0 {
		t.Fatalf("%s frame has %d trailing bytes", h.MsgType, r.Len())
	}
	return d
}

func refs(sha string) []Record {
	return []Record{{
		Collection: "org.tangled.git.refs",
		Rkey:       "self",
		Value:      map[string]any{"defaultBranch": "main", "refs": atdata.Bytes(sha + " refs/heads/main\n")},
	}}
}

// TestLifecycleMatchesKnot2 checks the creation sequence knot2's interop
// suite asserts: identity, account, empty genesis, record commit, sync.
func TestLifecycleMatchesKnot2(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey(t)
	pub, _ := key.PublicKey()

	changed, err := s.Sync(ctx, testDid, key, refs("aaaa"))
	if err != nil || !changed {
		t.Fatalf("Sync = %v, %v", changed, err)
	}
	frames, err := s.FramesAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var ds []decoded
	for i, f := range frames {
		if i > 0 && f.Seq <= frames[i-1].Seq {
			t.Fatalf("seq not increasing: %d then %d", frames[i-1].Seq, f.Seq)
		}
		d := decode(t, f)
		types = append(types, d.typ)
		ds = append(ds, d)
	}
	want := []string{"#identity", "#account", "#commit", "#commit", "#sync"}
	if len(types) != len(want) {
		t.Fatalf("frames = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("frames = %v, want %v", types, want)
		}
	}

	genesis := ds[2].commit
	if len(genesis.Ops) != 0 || genesis.Since != nil || genesis.PrevData != nil {
		t.Errorf("genesis should be empty with no since/prevData: %+v", genesis)
	}
	rev, _ := syntax.ParseTID(genesis.Rev)
	if rev.Time().UnixMicro() != genesis.Seq {
		t.Errorf("seq %d is not the rev's microsecond tick %d", genesis.Seq, rev.Time().UnixMicro())
	}
	if !ds[1].account.Active || ds[1].account.Status != nil {
		t.Errorf("account frame = %+v", ds[1].account)
	}

	second := ds[3].commit
	if len(second.Ops) != 1 || second.Ops[0].Action != "create" || second.Ops[0].Path != "org.tangled.git.refs/self" {
		t.Fatalf("second commit ops = %+v", second.Ops)
	}
	if second.Since == nil || *second.Since != genesis.Rev || second.PrevData == nil {
		t.Errorf("second commit should chain from genesis")
	}
	// indigo's inductive verifier inverts the ops against prevData.
	if _, err := repo.VerifyCommitMessage(ctx, second); err != nil {
		t.Fatalf("VerifyCommitMessage: %v", err)
	}
	if ds[4].sync.Rev != second.Rev {
		t.Errorf("sync asserts %s, want last rev %s", ds[4].sync.Rev, second.Rev)
	}

	// an identical desired set is a no-op
	if changed, err := s.Sync(ctx, testDid, key, refs("aaaa")); err != nil || changed {
		t.Fatalf("idempotent Sync = %v, %v", changed, err)
	}

	// update then delete chain and verify inductively
	if _, err := s.Sync(ctx, testDid, key, refs("bbbb")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, testDid, key, nil); err != nil {
		t.Fatal(err)
	}
	more, _ := s.FramesAfter(frames[len(frames)-1].Seq, 100)
	if len(more) != 2 {
		t.Fatalf("got %d frames after update+delete, want 2", len(more))
	}
	for i, action := range []string{"update", "delete"} {
		c := decode(t, more[i]).commit
		if c.Ops[0].Action != action || c.Ops[0].Prev == nil {
			t.Errorf("op %d = %+v, want %s with prev", i, c.Ops[0], action)
		}
		if _, err := repo.VerifyCommitMessage(ctx, c); err != nil {
			t.Fatalf("%s: VerifyCommitMessage: %v", action, err)
		}
	}

	// the exported repo is signed by the key and loads cleanly
	if _, err := s.Sync(ctx, testDid, key, refs("cccc")); err != nil {
		t.Fatal(err)
	}
	car, err := s.RepoCAR(testDid)
	if err != nil {
		t.Fatal(err)
	}
	commit, r, err := repo.LoadRepoFromCAR(ctx, bytes.NewReader(car))
	if err != nil {
		t.Fatal(err)
	}
	if err := commit.VerifySignature(pub); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	raw, _, err := r.GetRecordBytes(ctx, "org.tangled.git.refs", "self")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := atdata.UnmarshalCBOR(raw)
	if got := string(val["refs"].(atdata.Bytes)); got != "cccc refs/heads/main\n" {
		t.Errorf("refs = %q", got)
	}
	if val["$type"] != "org.tangled.git.refs" {
		t.Errorf("$type = %v", val["$type"])
	}
}

func TestRecordProofAndBlocks(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey(t)
	recs := append(refs("aaaa"), Record{
		Collection: "org.tangled.graph.collaborator",
		Rkey:       "did:plc:abcdefghijklmnopqrstuvwx",
		Value:      map[string]any{"createdAt": "2026-10-30T00:00:00Z"},
	})
	if _, err := s.Sync(ctx, testDid, key, recs); err != nil {
		t.Fatal(err)
	}
	proof, err := s.RecordCAR(testDid, "org.tangled.git.refs", "self")
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := repo.LoadRepoFromCAR(ctx, bytes.NewReader(proof))
	if err != nil {
		t.Fatalf("proof does not load: %v", err)
	}
	if _, err := r.GetRecordCID(ctx, "org.tangled.git.refs", "self"); err != nil {
		t.Fatal(err)
	}

	acct, _ := s.Account(testDid)
	blocks, err := s.BlocksCAR(testDid, []string{acct.Data.String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.LoadRepoFromCAR(ctx, bytes.NewReader(blocks)); err != nil {
		t.Fatalf("getBlocks of the mst root does not load: %v", err)
	}

	cols, _ := s.Collections(testDid)
	if len(cols) != 2 {
		t.Errorf("collections = %v", cols)
	}
	listed, _ := s.Records(testDid, "org.tangled.graph.collaborator", "", 10, false)
	if len(listed) != 1 || listed[0].Value["createdAt"] != "2026-10-30T00:00:00Z" {
		t.Errorf("listed = %+v", listed)
	}
}

func TestSetActiveAndRestartKeepsSeqIncreasing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *Store {
		db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "t.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		s, err := New(db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	key := newKey(t)
	if _, err := s.Sync(ctx, testDid, key, refs("aaaa")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(testDid, false, "deleted"); err != nil {
		t.Fatal(err)
	}
	last, _ := s.LastSeq()
	frames, _ := s.FramesAfter(last-1, 1)
	d := decode(t, frames[0])
	if d.account == nil || d.account.Active || d.account.Status == nil || *d.account.Status != "deleted" {
		t.Fatalf("deactivation frame = %+v", d)
	}

	s2 := open()
	if err := s2.SetActive(testDid, true, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := s2.LastSeq()
	if after <= last {
		t.Fatalf("seq went backwards across restart: %d then %d", last, after)
	}
}
