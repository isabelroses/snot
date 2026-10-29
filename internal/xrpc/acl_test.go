package xrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	"tangled.org/core/xrpc/serviceauth"
)

func TestListMembersReturnsSeeded(t *testing.T) {
	x := newXrpc(t)
	if err := x.ACL.SeedMembers([]string{"did:plc:owner123"}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, x)
	defer srv.Close()

	code, m := getJSON(t, srv, "sh.tangled.knot.listMembers", nil)
	if code != 200 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	items := m["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["subject"] != "did:plc:owner123" {
		t.Errorf("items=%v", items)
	}
}

func TestListCollaborators(t *testing.T) {
	x := newXrpc(t)
	if err := x.ACL.AddCollaborator("did:plc:repo123", "did:plc:bob", "did:plc:owner123"); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, x)
	defer srv.Close()

	// known repoDid (seeded in newXrpc's Dids) → its collaborators
	code, m := getJSON(t, srv, "sh.tangled.repo.listCollaborators",
		url.Values{"subject": {"did:plc:repo123"}})
	if code != 200 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	items := m["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["subject"] != "did:plc:bob" {
		t.Fatalf("items=%v", items)
	}
	// effectiveSince is required by the lexicon; with no consent model it's the add time
	item := items[0].(map[string]any)
	if item["effectiveSince"] == nil || item["effectiveSince"] != item["createdAt"] {
		t.Errorf("effectiveSince=%v createdAt=%v", item["effectiveSince"], item["createdAt"])
	}
	if m["total"] != float64(1) {
		t.Errorf("total=%v", m["total"])
	}

	// unknown repoDid → 400
	code, _ = getJSON(t, srv, "sh.tangled.repo.listCollaborators",
		url.Values{"subject": {"did:plc:unknown"}})
	if code != 400 {
		t.Errorf("unknown repo code=%d", code)
	}
}

// postAs invokes an ACL mutation handler directly with the actor DID injected
// the way serviceauth middleware does.
func postAs(t *testing.T, h func(http.ResponseWriter, *http.Request), actor string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(b))
	d, _ := syntax.ParseDID(actor)
	req = req.WithContext(context.WithValue(req.Context(), serviceauth.ActorDid, d))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestAddMemberRequiresKnotOwner(t *testing.T) {
	x := newXrpc(t) // newXrpc's cfg.OwnerDid is did:plc:knotadmin
	_ = x.ACL.SeedMembers([]string{"did:plc:owner123"})

	// non-member and plain member alike → 403; membership is the knot owner's call
	for _, actor := range []string{"did:plc:intruder", "did:plc:owner123"} {
		rec := postAs(t, x.AddMember, actor, tangled.KnotAddMember_Input{Subject: "did:plc:new"})
		if rec.Code != 403 {
			t.Fatalf("%s add: code=%d", actor, rec.Code)
		}
	}
	if ok, _ := x.ACL.IsMember("did:plc:new"); ok {
		t.Fatal("refused add still added the member")
	}
	// knot owner → 200, new member appears
	rec := postAs(t, x.AddMember, "did:plc:knotadmin", tangled.KnotAddMember_Input{Subject: "did:plc:new"})
	if rec.Code != 200 {
		t.Fatalf("owner add: code=%d body=%s", rec.Code, rec.Body)
	}
	if ok, _ := x.ACL.IsMember("did:plc:new"); !ok {
		t.Error("new member not added")
	}
}

func TestRemoveMemberRefusesConfigSeeded(t *testing.T) {
	x := newXrpc(t) // newXrpc's cfg.UserMap maps did:plc:owner123 -> isabel
	_ = x.ACL.SeedMembers([]string{"did:plc:owner123"})
	_ = x.ACL.AddMember("did:plc:extra", "did:plc:knotadmin")

	// a plain member can't remove anyone → 403
	rec := postAs(t, x.RemoveMember, "did:plc:owner123", tangled.KnotRemoveMember_Input{Subject: "did:plc:extra"})
	if rec.Code != 403 {
		t.Fatalf("member remove: code=%d", rec.Code)
	}

	// removing the config-seeded owner → 400, still a member
	rec = postAs(t, x.RemoveMember, "did:plc:knotadmin", tangled.KnotRemoveMember_Input{Subject: "did:plc:owner123"})
	if rec.Code != 400 {
		t.Fatalf("remove config member: code=%d", rec.Code)
	}
	if ok, _ := x.ACL.IsMember("did:plc:owner123"); !ok {
		t.Error("config owner must remain a member")
	}

	// knot owner removes an extra member → 200
	rec = postAs(t, x.RemoveMember, "did:plc:knotadmin", tangled.KnotRemoveMember_Input{Subject: "did:plc:extra"})
	if rec.Code != 200 {
		t.Fatalf("remove extra: code=%d", rec.Code)
	}
}

func TestAddCollaborator(t *testing.T) {
	x := newXrpc(t)
	_ = x.ACL.SeedMembers([]string{"did:plc:owner123"})

	// known repoDid (did:plc:repo123 seeded in newXrpc's Dids) → 200
	rec := postAs(t, x.AddCollaborator, "did:plc:owner123",
		tangled.RepoAddCollaborator_Input{Repo: "did:plc:repo123", Subject: "did:plc:bob"})
	if rec.Code != 200 {
		t.Fatalf("add collaborator: code=%d body=%s", rec.Code, rec.Body)
	}
	cs, _ := x.ACL.Collaborators("did:plc:repo123")
	if len(cs) != 1 || cs[0].Subject != "did:plc:bob" {
		t.Errorf("collaborators=%+v", cs)
	}

	// unknown repoDid → not 200
	rec = postAs(t, x.AddCollaborator, "did:plc:owner123",
		tangled.RepoAddCollaborator_Input{Repo: "did:plc:unknown", Subject: "did:plc:bob"})
	if rec.Code == 200 {
		t.Errorf("unknown repo should fail")
	}
}

func TestCollaboratorsRequireRepoOwner(t *testing.T) {
	x := newXrpc(t) // did:plc:repo123 belongs to isabel, i.e. did:plc:owner123
	_ = x.ACL.SeedMembers([]string{"did:plc:owner123", "did:plc:other"})

	// another knot member must not grant themselves access to someone's repo
	rec := postAs(t, x.AddCollaborator, "did:plc:other",
		tangled.RepoAddCollaborator_Input{Repo: "did:plc:repo123", Subject: "did:plc:other"})
	if rec.Code != 403 {
		t.Fatalf("non-owner add: code=%d body=%s", rec.Code, rec.Body)
	}
	if cs, _ := x.ACL.Collaborators("did:plc:repo123"); len(cs) != 0 {
		t.Fatalf("refused add still granted: %+v", cs)
	}

	// the knot owner may manage any repo's collaborators
	rec = postAs(t, x.AddCollaborator, "did:plc:knotadmin",
		tangled.RepoAddCollaborator_Input{Repo: "did:plc:repo123", Subject: "did:plc:bob"})
	if rec.Code != 200 {
		t.Fatalf("knot owner add: code=%d body=%s", rec.Code, rec.Body)
	}

	rec = postAs(t, x.RemoveCollaborator, "did:plc:other",
		tangled.RepoRemoveCollaborator_Input{Repo: "did:plc:repo123", Subject: "did:plc:bob"})
	if rec.Code != 403 {
		t.Fatalf("non-owner remove: code=%d", rec.Code)
	}
	if cs, _ := x.ACL.Collaborators("did:plc:repo123"); len(cs) != 1 {
		t.Fatalf("refused remove still revoked: %+v", cs)
	}
}

func TestVersionAdvertisesCapabilities(t *testing.T) {
	srv := newTestServer(t, newXrpc(t))
	defer srv.Close()
	code, m := getJSON(t, srv, "sh.tangled.knot.version", nil)
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	caps, _ := m["capabilities"].([]any)
	for _, want := range []string{"knot-acl", "repo-did-input"} {
		if !slices.Contains(caps, any(want)) {
			t.Errorf("version capabilities missing %s: %v", want, caps)
		}
	}
}
