package state

import "gorm.io/gorm"

// RepoDidInfo records a minted repo DID: which repo it names and the PLC
// rotation/signing key that controls it. Losing the key means the DID's
// document can never be updated, so the state DB must be backed up.
type RepoDidInfo struct {
	User string `json:"user"` // forgejo owner (lower)
	Repo string `json:"repo"` // repo name (lower)
	Key  []byte `json:"key"`  // raw private key bytes
	// Deleted marks a repo unpublished via deleteRepo. The row (and key) is
	// kept so the DID's history stays attributable; re-adopting mints anew.
	Deleted bool `json:"deleted"`
}

// RepoDids stores the per-repo did:plc identities the shim has minted.
type RepoDids struct {
	db *gorm.DB
}

// LoadRepoDids opens the state DB in dir and returns its repo-DID accessor.
func LoadRepoDids(dir string) (*RepoDids, error) {
	db, err := Open(dir)
	if err != nil {
		return nil, err
	}
	return db.RepoDids(), nil
}

// Put records (or overwrites) the info for a repo DID.
func (r *RepoDids) Put(did string, info RepoDidInfo) error {
	return r.db.Save(&repoDidRow{Did: did, User: info.User, Repo: info.Repo, Key: info.Key, Deleted: info.Deleted}).Error
}

// Get returns the info recorded for a repo DID.
func (r *RepoDids) Get(did string) (RepoDidInfo, bool) {
	var row repoDidRow
	if err := r.db.First(&row, "did = ?", did).Error; err != nil {
		return RepoDidInfo{}, false
	}
	return RepoDidInfo{User: row.User, Repo: row.Repo, Key: row.Key, Deleted: row.Deleted}, true
}

// All returns every repo DID ever minted, deleted ones included.
func (r *RepoDids) All() ([]string, error) {
	var dids []string
	err := r.db.Model(&repoDidRow{}).Order("did asc").Pluck("did", &dids).Error
	return dids, err
}

// MarkDeleted unpublishes a repo DID; ByRepo stops returning it.
func (r *RepoDids) MarkDeleted(did string) error {
	return r.db.Model(&repoDidRow{}).Where("did = ?", did).Update("deleted", true).Error
}

// ByRepo returns the DID for the given (user, repo) pair (case-insensitive).
func (r *RepoDids) ByRepo(user, repo string) (string, bool) {
	var row repoDidRow
	err := r.db.First(&row, "user = ? COLLATE NOCASE AND repo = ? COLLATE NOCASE AND NOT deleted", user, repo).Error
	if err != nil {
		return "", false
	}
	return row.Did, true
}
