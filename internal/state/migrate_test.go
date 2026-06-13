package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestImportsLegacyJSON(t *testing.T) {
	dir := t.TempDir()

	rkeys := map[string]string{"3l5tid": "demo"}
	rb, _ := json.Marshal(rkeys)
	if err := os.WriteFile(filepath.Join(dir, "rkeys.json"), rb, 0o600); err != nil {
		t.Fatal(err)
	}

	dids := map[string]RepoDidInfo{
		"did:plc:repo1": {User: "isabel", Repo: "demo", Key: []byte("plckey")},
	}
	db, _ := json.Marshal(dids)
	if err := os.WriteFile(filepath.Join(dir, "repodids.json"), db, 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := d.Rkeys().RepoByRkey("3l5tid"); !ok || got != "demo" {
		t.Errorf("rkey not imported: %q %v", got, ok)
	}
	info, ok := d.RepoDids().Get("did:plc:repo1")
	if !ok || info.Repo != "demo" || string(info.Key) != "plckey" {
		t.Errorf("repodid not imported: %+v %v", info, ok)
	}

	// Originals renamed to *.migrated (kept as backup, not re-imported).
	for _, name := range []string{"rkeys.json", "repodids.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been renamed away", name)
		}
		if _, err := os.Stat(filepath.Join(dir, name+".migrated")); err != nil {
			t.Errorf("%s.migrated backup missing: %v", name, err)
		}
	}

	// Reopen: must not error or duplicate (table already populated, files gone).
	if _, err := Open(dir); err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
}
