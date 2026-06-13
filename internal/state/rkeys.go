package state

import "gorm.io/gorm"

// RkeyMap maps sh.tangled.repo record rkeys to Forgejo repo names.
type RkeyMap struct {
	db *gorm.DB
}

// LoadRkeyMap opens the state DB in dir and returns its rkey accessor.
func LoadRkeyMap(dir string) (*RkeyMap, error) {
	db, err := Open(dir)
	if err != nil {
		return nil, err
	}
	return db.Rkeys(), nil
}

// Put records (or overwrites) the repo name for an rkey.
func (r *RkeyMap) Put(rkey, repoName string) error {
	return r.db.Save(&rkeyRow{Rkey: rkey, RepoName: repoName}).Error
}

// RepoByRkey returns the repo name recorded for an rkey.
func (r *RkeyMap) RepoByRkey(rkey string) (string, bool) {
	var row rkeyRow
	if err := r.db.First(&row, "rkey = ?", rkey).Error; err != nil {
		return "", false
	}
	return row.RepoName, true
}

// RkeyByRepo returns an rkey recorded for a repo name.
func (r *RkeyMap) RkeyByRepo(repoName string) (string, bool) {
	var row rkeyRow
	if err := r.db.First(&row, "repo_name = ?", repoName).Error; err != nil {
		return "", false
	}
	return row.Rkey, true
}
