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
