package state

import "testing"

func TestACLMembers(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acl := db.ACL()

	if err := acl.SeedMembers([]string{"did:plc:owner1", "did:plc:owner2"}); err != nil {
		t.Fatal(err)
	}
	// idempotent: seeding again must not error or duplicate
	if err := acl.SeedMembers([]string{"did:plc:owner1"}); err != nil {
		t.Fatal(err)
	}

	got, err := acl.Members()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("members = %d, want 2: %+v", len(got), got)
	}

	if ok, _ := acl.IsMember("did:plc:owner1"); !ok {
		t.Error("owner1 should be a member")
	}
	if ok, _ := acl.IsMember("did:plc:nobody"); ok {
		t.Error("nobody should not be a member")
	}

	if err := acl.AddMember("did:plc:extra", "did:plc:owner1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := acl.IsMember("did:plc:extra"); !ok {
		t.Error("extra should be a member after AddMember")
	}
	if err := acl.RemoveMember("did:plc:extra"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := acl.IsMember("did:plc:extra"); ok {
		t.Error("extra should be gone after RemoveMember")
	}
}

func TestACLCollaborators(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acl := db.ACL()

	if err := acl.AddCollaborator("did:plc:repoA", "did:plc:bob", "did:plc:owner1"); err != nil {
		t.Fatal(err)
	}
	if err := acl.AddCollaborator("did:plc:repoB", "did:plc:carol", "did:plc:owner1"); err != nil {
		t.Fatal(err)
	}

	a, err := acl.Collaborators("did:plc:repoA")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].Subject != "did:plc:bob" || a[0].AddedBy != "did:plc:owner1" {
		t.Fatalf("repoA collaborators = %+v", a)
	}
	// repoB's collaborator must not leak into repoA
	b, _ := acl.Collaborators("did:plc:repoB")
	if len(b) != 1 || b[0].Subject != "did:plc:carol" {
		t.Fatalf("repoB collaborators = %+v", b)
	}

	if err := acl.RemoveCollaborator("did:plc:repoA", "did:plc:bob"); err != nil {
		t.Fatal(err)
	}
	if c, _ := acl.Collaborators("did:plc:repoA"); len(c) != 0 {
		t.Fatalf("repoA should be empty, got %+v", c)
	}
}
