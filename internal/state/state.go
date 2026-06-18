// Package state persists the small amount of mutable state the shim owns in a
// SQLite database (via GORM): the mapping from sh.tangled.repo record rkeys to
// Forgejo repo names, and the per-repo did:plc identities it has minted.
package state

import (
	"database/sql"
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

type eventRow struct {
	Rkey    string `gorm:"column:rkey;primaryKey"`
	Nsid    string `gorm:"column:nsid;primaryKey"`
	Event   []byte `gorm:"column:event"`
	Created int64  `gorm:"column:created;index"`
}

func (eventRow) TableName() string { return "events" }

// DB owns snot's persistent SQLite store and hands out typed accessors.
type DB struct {
	gorm *gorm.DB
}

// Open opens (creating if absent) snot.db inside dir and runs migrations.
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
	if err := gdb.AutoMigrate(&rkeyRow{}, &repoDidRow{}, &eventRow{}, &knotMemberRow{}, &repoCollaboratorRow{}); err != nil {
		return nil, err
	}
	// the db holds PLC rotation keys; keep it owner-only.
	_ = os.Chmod(path, 0o600)

	return &DB{gorm: gdb}, nil
}

// SQL returns the underlying *sql.DB, which satisfies tangled's
// eventstream.Store (Exec/Query). The events table holds the knot eventstream.
func (db *DB) SQL() (*sql.DB, error) {
	return db.gorm.DB()
}

// Rkeys returns the rkey↔repo accessor.
func (db *DB) Rkeys() *RkeyMap { return &RkeyMap{db: db.gorm} }

// RepoDids returns the repo-DID accessor.
func (db *DB) RepoDids() *RepoDids { return &RepoDids{db: db.gorm} }

// ACL returns the member/collaborator accessor.
func (db *DB) ACL() *ACL { return &ACL{db: db.gorm} }
