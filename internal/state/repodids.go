package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const repodidFile = "repodids.json"

// RepoDidInfo records a minted repo DID: which repo it names and the PLC
// rotation/signing key that controls it. Losing the key means the DID's
// document can never be updated, so the state dir must be backed up.
type RepoDidInfo struct {
	User string `json:"user"` // forgejo owner (lower)
	Repo string `json:"repo"` // repo name (lower)
	Key  []byte `json:"key"`  // raw private key bytes
}

type RepoDids struct {
	mu   sync.Mutex
	path string
	m    map[string]RepoDidInfo // keyed by DID
}

func LoadRepoDids(stateDir string) (*RepoDids, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	r := &RepoDids{
		path: filepath.Join(stateDir, repodidFile),
		m:    make(map[string]RepoDidInfo),
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

func (r *RepoDids) Put(did string, info RepoDidInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[did] = info

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

func (r *RepoDids) Get(did string) (RepoDidInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.m[did]
	return info, ok
}

// ByRepo returns the DID for the given (user, repo) pair (case-insensitive).
func (r *RepoDids) ByRepo(user, repo string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for did, info := range r.m {
		if strings.EqualFold(info.User, user) && strings.EqualFold(info.Repo, repo) {
			return did, true
		}
	}
	return "", false
}
