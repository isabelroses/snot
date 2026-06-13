package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRkeyMapPersists(t *testing.T) {
	dir := t.TempDir()

	m, err := LoadRkeyMap(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Put("3l5abc123", "myrepo"); err != nil {
		t.Fatal(err)
	}

	// Reopen against the same db file.
	m2, err := LoadRkeyMap(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m2.RepoByRkey("3l5abc123"); !ok || got != "myrepo" {
		t.Errorf("RepoByRkey = %q, %v", got, ok)
	}
	if got, ok := m2.RkeyByRepo("myrepo"); !ok || got != "3l5abc123" {
		t.Errorf("RkeyByRepo = %q, %v", got, ok)
	}
	if _, ok := m2.RepoByRkey("missing"); ok {
		t.Error("expected miss")
	}

	if _, err := os.Stat(filepath.Join(dir, dbFile)); err != nil {
		t.Fatalf("%s not written: %v", dbFile, err)
	}
}

func TestPutOverwrites(t *testing.T) {
	m, err := LoadRkeyMap(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Put("rkey1", "repo1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Put("rkey1", "repo2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.RepoByRkey("rkey1"); got != "repo2" {
		t.Errorf("got %q", got)
	}
}
