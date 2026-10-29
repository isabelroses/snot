package xrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/xrpc/serviceauth"
)

// asActor injects the actor DID the way serviceauth middleware does.
func asActor(req *http.Request, did string) *http.Request {
	d, _ := syntax.ParseDID(did)
	return req.WithContext(context.WithValue(req.Context(), serviceauth.ActorDid, d))
}

func postCreate(t *testing.T, x *Xrpc, actor string, input tangled.RepoCreate_Input) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(input)
	req := httptest.NewRequest("POST", "/sh.tangled.repo.create", bytes.NewReader(body))
	req = asActor(req, actor)
	rec := httptest.NewRecorder()
	x.CreateRepo(rec, req)
	return rec
}

func TestCreateRepoAdoptsExisting(t *testing.T) {
	x := newXrpc(t)
	rec := postCreate(t, x, "did:plc:owner123",
		tangled.RepoCreate_Input{Rkey: "3l5newrkey", Name: "demo"})
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var out tangled.RepoCreate_Output
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// The repo already has a seeded DID (did:plc:repo123) so EnsureRepoDid must
	// return that, not mint a new one.
	if out.RepoDid == nil || *out.RepoDid != "did:plc:repo123" {
		t.Errorf("repoDid = %v", out.RepoDid)
	}
	if got, _ := x.Rkeys.RepoByRkey("3l5newrkey"); got != "demo" {
		t.Errorf("rkey not recorded, got %q", got)
	}
	// Dids must contain the DID.
	if _, ok := x.Resolve.Dids.Get("did:plc:repo123"); !ok {
		t.Error("Dids should contain did:plc:repo123 after adopt")
	}
}

func TestCreateRepoAdoptStableOnSecondCall(t *testing.T) {
	x := newXrpc(t)

	// Two adopts of the same repo must return the same DID.
	rec1 := postCreate(t, x, "did:plc:owner123",
		tangled.RepoCreate_Input{Rkey: "3l5rkey1", Name: "demo"})
	rec2 := postCreate(t, x, "did:plc:owner123",
		tangled.RepoCreate_Input{Rkey: "3l5rkey2", Name: "demo"})
	if rec1.Code != 200 || rec2.Code != 200 {
		t.Fatalf("codes=%d %d", rec1.Code, rec2.Code)
	}
	var out1, out2 tangled.RepoCreate_Output
	if err := json.Unmarshal(rec1.Body.Bytes(), &out1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out2); err != nil {
		t.Fatal(err)
	}
	if out1.RepoDid == nil || out2.RepoDid == nil || *out1.RepoDid != *out2.RepoDid {
		t.Errorf("second adopt returned different DID: %v vs %v", out1.RepoDid, out2.RepoDid)
	}
}

func TestCreateRepoRejectsMissingRepo(t *testing.T) {
	x := newXrpc(t)
	rec := postCreate(t, x, "did:plc:owner123",
		tangled.RepoCreate_Input{Rkey: "3l5newrkey", Name: "doesnotexist"})
	if rec.Code == 200 {
		t.Fatalf("adopting a non-existent repo must fail; body=%s", rec.Body)
	}
}

func TestCreateRepoRejectsSource(t *testing.T) {
	x := newXrpc(t)
	// "demo" exists, so without the check a fork would adopt it instead
	rec := postCreate(t, x, "did:plc:owner123", tangled.RepoCreate_Input{
		Rkey: "3l5forkrkey", Name: "demo",
		Source: &tangled.RepoCreate_Source{Url: "https://knot.example/did:plc:other/demo", Kind: "fork"},
	})
	if rec.Code == 200 {
		t.Fatalf("a fork must not adopt the same-named repo; body=%s", rec.Body)
	}
	if got, _ := x.Rkeys.RepoByRkey("3l5forkrkey"); got != "" {
		t.Errorf("rejected fork still recorded rkey -> %q", got)
	}
}

func TestCreateRepoRejectsBadRkeys(t *testing.T) {
	x := newXrpc(t)
	if rec := postCreate(t, x, "did:plc:owner123",
		tangled.RepoCreate_Input{Rkey: "../../etc", Name: "demo"}); rec.Code == 200 {
		t.Errorf("invalid rkey accepted; body=%s", rec.Body)
	}

	// an rkey already bound to one repo must not be re-pointed at another
	if err := x.Rkeys.Put("3l5taken", "other"); err != nil {
		t.Fatal(err)
	}
	rec := postCreate(t, x, "did:plc:owner123", tangled.RepoCreate_Input{Rkey: "3l5taken", Name: "demo"})
	if rec.Code != 409 {
		t.Fatalf("rebinding rkey: code=%d body=%s", rec.Code, rec.Body)
	}
	if got, _ := x.Rkeys.RepoByRkey("3l5taken"); got != "other" {
		t.Errorf("rkey re-pointed to %q", got)
	}
}

func TestCreateRepoRejectsUnmappedActor(t *testing.T) {
	x := newXrpc(t)
	// neither a stranger nor the knot admin may adopt: only mapped DIDs,
	// and the admin DID governs the knot, not repos
	for _, actor := range []string{"did:plc:intruder", "did:plc:knotadmin"} {
		rec := postCreate(t, x, actor,
			tangled.RepoCreate_Input{Rkey: "3l5newrkey", Name: "demo"})
		if rec.Code == 200 {
			t.Fatalf("unmapped actor %s must not adopt repos", actor)
		}
	}
}
