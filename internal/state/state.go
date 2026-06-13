// Package state persists the small amount of mutable state the shim owns in a
// SQLite database (via GORM): the mapping from sh.tangled.repo record rkeys to
// Forgejo repo names, and the per-repo did:plc identities it has minted.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const dbFile = "snot.db"

type rkeyRow struct {
	Rkey     string `gorm:"primaryKey"`
	RepoName string `gorm:"index"`
}

func (rkeyRow) TableName() string { return "rkeys" }

type repoDidRow struct {
	Did  string `gorm:"primaryKey"`
	User string `gorm:"index:idx_repodid_user_repo"`
	Repo string `gorm:"index:idx_repodid_user_repo"`
	Key  []byte
}

func (repoDidRow) TableName() string { return "repo_dids" }

// DB owns snot's persistent SQLite store and hands out typed accessors.
type DB struct {
	gorm *gorm.DB
}

// Open opens (creating if absent) snot.db inside dir, runs migrations, and
// imports any legacy JSON state left by older versions.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, dbFile)

	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if err := gdb.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		return nil, err
	}
	if err := gdb.AutoMigrate(&rkeyRow{}, &repoDidRow{}); err != nil {
		return nil, err
	}
	// the db holds PLC rotation keys; keep it owner-only.
	_ = os.Chmod(path, 0o600)

	db := &DB{gorm: gdb}
	if err := db.importLegacyJSON(dir); err != nil {
		return nil, fmt.Errorf("importing legacy json state: %w", err)
	}
	return db, nil
}

// Rkeys returns the rkey↔repo accessor.
func (db *DB) Rkeys() *RkeyMap { return &RkeyMap{db: db.gorm} }

// RepoDids returns the repo-DID accessor.
func (db *DB) RepoDids() *RepoDids { return &RepoDids{db: db.gorm} }

// importLegacyJSON one-time-imports rkeys.json / repodids.json written by
// pre-SQLite versions. It only imports into an empty table, then renames the
// file to *.migrated so it is kept as a backup and never re-imported.
func (db *DB) importLegacyJSON(dir string) error {
	if err := db.importRkeysJSON(filepath.Join(dir, "rkeys.json")); err != nil {
		return err
	}
	return db.importRepoDidsJSON(filepath.Join(dir, "repodids.json"))
}

func (db *DB) importRkeysJSON(path string) error {
	b, ok, err := readLegacy(path)
	if err != nil || !ok {
		return err
	}
	var count int64
	if err := db.gorm.Model(&rkeyRow{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	rows := make([]rkeyRow, 0, len(m))
	for rkey, repo := range m {
		rows = append(rows, rkeyRow{Rkey: rkey, RepoName: repo})
	}
	if len(rows) > 0 {
		if err := db.gorm.Create(&rows).Error; err != nil {
			return err
		}
	}
	return os.Rename(path, path+".migrated")
}

func (db *DB) importRepoDidsJSON(path string) error {
	b, ok, err := readLegacy(path)
	if err != nil || !ok {
		return err
	}
	var count int64
	if err := db.gorm.Model(&repoDidRow{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var m map[string]RepoDidInfo
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	rows := make([]repoDidRow, 0, len(m))
	for did, info := range m {
		rows = append(rows, repoDidRow{Did: did, User: info.User, Repo: info.Repo, Key: info.Key})
	}
	if len(rows) > 0 {
		if err := db.gorm.Create(&rows).Error; err != nil {
			return err
		}
	}
	return os.Rename(path, path+".migrated")
}

func readLegacy(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
