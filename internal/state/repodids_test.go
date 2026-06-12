package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoDidsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	r, err := LoadRepoDids(dir)
	if err != nil {
		t.Fatal(err)
	}

	info := RepoDidInfo{User: "isabel", Repo: "demo", Key: []byte("testkey")}
	if err := r.Put("did:plc:abc123", info); err != nil {
		t.Fatal(err)
	}

	// Reload from disk.
	r2, err := LoadRepoDids(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := r2.Get("did:plc:abc123")
	if !ok {
		t.Fatal("Get: not found after reload")
	}
	if got.User != "isabel" || got.Repo != "demo" || string(got.Key) != "testkey" {
		t.Errorf("Get = %+v", got)
	}
}

func TestRepoDidsByRepo(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRepoDids(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Put("did:plc:abc123", RepoDidInfo{User: "Isabel", Repo: "Demo", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}

	// Case-insensitive hit.
	did, ok := r.ByRepo("isabel", "demo")
	if !ok || did != "did:plc:abc123" {
		t.Errorf("ByRepo hit: got %q, %v", did, ok)
	}

	// Miss.
	if _, ok := r.ByRepo("isabel", "other"); ok {
		t.Error("ByRepo should miss for unknown repo")
	}
}

func TestRepoDidsFilePerms(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRepoDids(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put("did:plc:xyz", RepoDidInfo{User: "u", Repo: "r", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, repodidFile))
	if err != nil {
		t.Fatalf("repodids.json not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRepoDidsToleratesAbsent(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRepoDids(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("did:plc:nobody"); ok {
		t.Error("expected miss on empty store")
	}
}
