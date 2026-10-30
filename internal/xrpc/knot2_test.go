package xrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

type fakePublisher struct{ repos, knot []string }

func (f *fakePublisher) SyncRepo(_ context.Context, did string) error {
	f.repos = append(f.repos, did)
	return nil
}

func (f *fakePublisher) SyncKnot(context.Context) error {
	f.knot = append(f.knot, "knot")
	return nil
}

func call(t *testing.T, h http.HandlerFunc, actor string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	if actor != "" {
		req = asActor(req, actor)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestDescribeKnot(t *testing.T) {
	x := newXrpc(t)
	rec := httptest.NewRecorder()
	x.DescribeKnot(rec, httptest.NewRequest("GET", "/", nil))
	var out struct {
		Did          string   `json:"did"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Did != "did:web:knot.example.com" || out.Version == "" {
		t.Errorf("describeKnot = %+v", out)
	}
	if slices.Contains(out.Capabilities, "knot-social-writes") {
		t.Error("the shim must leave social writes to the foster knot")
	}
	if !slices.Contains(out.Capabilities, "atproto-firehose") {
		t.Errorf("capabilities = %v", out.Capabilities)
	}
}

func TestCreateRepoV2AdoptsAndPublishes(t *testing.T) {
	x := newXrpc(t)
	fp := &fakePublisher{}
	x.Publish = fp

	rec := call(t, x.CreateRepoV2, "did:plc:owner123", map[string]any{"name": "demo"})
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var out struct{ Did string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Did != "did:plc:repo123" {
		t.Errorf("did = %q", out.Did)
	}
	if !slices.Equal(fp.repos, []string{"did:plc:repo123"}) {
		t.Errorf("published %v", fp.repos)
	}

	for name, body := range map[string]map[string]any{
		"missing repo":   {"name": "nope"},
		"user DID":       {"name": "demo", "did": "did:plc:mine"},
		"fork or import": {"name": "demo", "source": map[string]any{"$type": "org.tangled.repo.defs#sourceExternal"}},
	} {
		if rec := call(t, x.CreateRepoV2, "did:plc:owner123", body); rec.Code != 400 {
			t.Errorf("%s: code=%d", name, rec.Code)
		}
	}
	if rec := call(t, x.CreateRepoV2, "did:plc:stranger", map[string]any{"name": "demo"}); rec.Code != 403 {
		t.Errorf("unmapped actor: code=%d", rec.Code)
	}
}

func TestDeleteRepoUnpublishes(t *testing.T) {
	x := newXrpc(t)
	fp := &fakePublisher{}
	x.Publish = fp
	x.Rkeys.Put("3lrkey", "demo")

	if rec := call(t, x.DeleteRepo, "did:plc:stranger", map[string]any{"repo": "did:plc:repo123"}); rec.Code != 403 {
		t.Fatalf("stranger delete: code=%d", rec.Code)
	}
	if rec := call(t, x.DeleteRepo, "did:plc:owner123", map[string]any{"repo": "did:plc:repo123"}); rec.Code != 200 {
		t.Fatalf("owner delete: code=%d body=%s", rec.Code, rec.Body)
	}
	if info, _ := x.Resolve.Dids.Get("did:plc:repo123"); !info.Deleted {
		t.Error("DID not marked deleted")
	}
	if _, ok := x.Rkeys.RepoByRkey("3lrkey"); ok {
		t.Error("rkey binding survived delete")
	}
	if _, err := x.parseRepoParam("did:plc:repo123"); err == nil {
		t.Error("a deleted repo DID still resolves")
	}
	if !slices.Equal(fp.repos, []string{"did:plc:repo123"}) {
		t.Errorf("published %v", fp.repos)
	}
	if rec := call(t, x.DeleteRepo, "did:plc:owner123", map[string]any{"repo": "did:plc:repo123"}); rec.Code != 404 {
		t.Errorf("second delete: code=%d", rec.Code)
	}

	// re-adopting mints a fresh DID rather than reviving the deleted one
	rec := call(t, x.CreateRepoV2, "did:plc:owner123", map[string]any{"name": "demo"})
	var out struct{ Did string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Did != "did:plc:minted1" {
		t.Errorf("re-adopt did = %q, want a fresh mint", out.Did)
	}
}

func TestGrantRecords(t *testing.T) {
	x := newXrpc(t)
	fp := &fakePublisher{}
	x.Publish = fp
	knot := "did:web:knot.example.com"
	member := map[string]any{
		"repo": knot, "collection": "org.tangled.knot.member", "rkey": "did:plc:newbie",
		"record": map[string]any{"$type": "org.tangled.knot.member", "createdAt": "2026-10-30T00:00:00Z"},
	}

	if rec := call(t, x.CreateRecord, "did:plc:owner123", member); rec.Code != 403 {
		t.Errorf("non-admin member grant: code=%d", rec.Code)
	}
	if rec := call(t, x.CreateRecord, "did:plc:knotadmin", member); rec.Code != 200 {
		t.Fatalf("admin member grant: code=%d body=%s", rec.Code, rec.Body)
	}
	if ok, _ := x.ACL.IsMember("did:plc:newbie"); !ok {
		t.Error("member not added")
	}
	if len(fp.knot) != 1 {
		t.Errorf("knot republished %d times", len(fp.knot))
	}
	if rec := call(t, x.DeleteRecord, "did:plc:knotadmin", map[string]any{
		"repo": knot, "collection": "org.tangled.knot.member", "rkey": "did:plc:newbie",
	}); rec.Code != 200 {
		t.Fatalf("member revoke: code=%d", rec.Code)
	}
	if ok, _ := x.ACL.IsMember("did:plc:newbie"); ok {
		t.Error("member not removed")
	}

	wrongRepo := map[string]any{"repo": "did:plc:repo123", "collection": "org.tangled.knot.member", "rkey": "did:plc:x", "record": map[string]any{}}
	if rec := call(t, x.CreateRecord, "did:plc:knotadmin", wrongRepo); rec.Code != 400 {
		t.Errorf("member grant on a repo DID: code=%d", rec.Code)
	}

	collab := map[string]any{
		"repo": "did:plc:repo123", "collection": "org.tangled.graph.collaborator", "rkey": "did:plc:friend",
		"record": map[string]any{"createdAt": "2026-10-30T00:00:00Z"},
	}
	if rec := call(t, x.CreateRecord, "did:plc:stranger", collab); rec.Code != 403 {
		t.Errorf("stranger collaborator grant: code=%d", rec.Code)
	}
	if rec := call(t, x.CreateRecord, "did:plc:owner123", collab); rec.Code != 200 {
		t.Fatalf("owner collaborator grant: code=%d body=%s", rec.Code, rec.Body)
	}
	cs, _ := x.ACL.Collaborators("did:plc:repo123")
	if len(cs) != 1 || cs[0].Subject != "did:plc:friend" {
		t.Errorf("collaborators = %+v", cs)
	}
	if !slices.Contains(fp.repos, "did:plc:repo123") {
		t.Error("repo not republished after collaborator grant")
	}

	ticket := map[string]any{"repo": "did:plc:repo123", "collection": "org.tangled.track.ticket", "record": map[string]any{}}
	if rec := call(t, x.CreateRecord, "did:plc:owner123", ticket); rec.Code != 400 {
		t.Errorf("social record: code=%d", rec.Code)
	}
	mistyped := map[string]any{
		"repo": "did:plc:repo123", "collection": "org.tangled.graph.collaborator", "rkey": "did:plc:friend",
		"record": map[string]any{"$type": "org.tangled.knot.member"},
	}
	if rec := call(t, x.CreateRecord, "did:plc:owner123", mistyped); rec.Code != 400 {
		t.Errorf("mismatched $type: code=%d", rec.Code)
	}
}
