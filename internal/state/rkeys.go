// Package state persists the small amount of mutable state the shim owns:
// the mapping from sh.tangled.repo record rkeys to Forgejo repo names.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const rkeyFile = "rkeys.json"

type RkeyMap struct {
	mu   sync.Mutex
	path string
	m    map[string]string // rkey -> repo lower_name
}

func LoadRkeyMap(stateDir string) (*RkeyMap, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	r := &RkeyMap{
		path: filepath.Join(stateDir, rkeyFile),
		m:    make(map[string]string),
	}
	b, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &r.m); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RkeyMap) Put(rkey, repoName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[rkey] = repoName

	b, err := json.MarshalIndent(r.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *RkeyMap) RepoByRkey(rkey string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, ok := r.m[rkey]
	return repo, ok
}

func (r *RkeyMap) RkeyByRepo(repoName string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for rkey, repo := range r.m {
		if repo == repoName {
			return rkey, true
		}
	}
	return "", false
}
