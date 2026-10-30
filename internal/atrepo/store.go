// Package atrepo hosts small signed atproto repositories: one per repo DID,
// plus the knot's own did:web repo. Records are derived state; callers hand
// over the full desired record set and Store commits the difference, signing
// with the DID's key and sequencing a firehose frame for every change.
//
// Repos here hold a handful of records, so each commit rebuilds the MST from
// the record map rather than mutating a persisted tree.
package atrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/atproto/repo/mst"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/ipfs/go-cid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned for DIDs or records this store does not host.
var ErrNotFound = errors.New("atrepo: not found")

const (
	kindCommit = "commit"
	kindNode   = "node"
	kindRecord = "record"
)

type accountRow struct {
	Did    string `gorm:"primaryKey"`
	Rev    string
	Head   string // commit CID
	Data   string // MST root CID
	Active bool
	Status string // "" when active; deactivated/deleted otherwise
}

func (accountRow) TableName() string { return "at_accounts" }

type recordRow struct {
	Did        string `gorm:"primaryKey"`
	Collection string `gorm:"primaryKey"`
	Rkey       string `gorm:"primaryKey"`
	Cid        string
	Value      []byte // DAG-CBOR
}

func (recordRow) TableName() string { return "at_records" }

func (r recordRow) path() string { return r.Collection + "/" + r.Rkey }

// blockRow holds the blocks of each repo's current state only; past states
// survive solely inside the firehose frames that announced them.
type blockRow struct {
	Did  string `gorm:"primaryKey"`
	Cid  string `gorm:"primaryKey"`
	Kind string
	Data []byte
}

func (blockRow) TableName() string { return "at_blocks" }

type eventRow struct {
	Seq   int64 `gorm:"primaryKey;autoIncrement:false"`
	Did   string
	Frame []byte
}

func (eventRow) TableName() string { return "at_events" }

// Account is the hosting status and head of one repo.
type Account struct {
	Did    string
	Rev    string
	Head   cid.Cid
	Data   cid.Cid
	Active bool
	Status string
}

// Store persists hosted repos in the shim's SQLite database.
type Store struct {
	db    *gorm.DB
	mu    sync.Mutex // serializes commits so revs and seqs stay ordered
	clock *syntax.TIDClock
	hub   *hub
}

// New migrates the atrepo tables and seeds the TID clock past every seq
// already handed out, so cursors keep increasing across restarts.
func New(db *gorm.DB) (*Store, error) {
	if err := db.AutoMigrate(&accountRow{}, &recordRow{}, &blockRow{}, &eventRow{}); err != nil {
		return nil, err
	}
	var last int64
	if err := db.Model(&eventRow{}).Select("COALESCE(MAX(seq), 0)").Scan(&last).Error; err != nil {
		return nil, err
	}
	clock := syntax.ClockFromTID(syntax.NewTID(last, 0))
	return &Store{db: db, clock: &clock, hub: newHub()}, nil
}

// Record is one desired record, keyed by collection and rkey.
type Record struct {
	Collection string
	Rkey       string
	Value      map[string]any
}

// Path returns the record's MST key.
func (r Record) Path() string { return r.Collection + "/" + r.Rkey }

// Exists reports whether the store hosts did.
func (s *Store) Exists(did string) (bool, error) {
	var n int64
	err := s.db.Model(&accountRow{}).Where("did = ?", did).Count(&n).Error
	return n > 0, err
}

// Sync makes did's repo hold exactly the desired records, creating the repo
// (identity, account, genesis commit, then a #sync assertion) if it is new.
// It reports whether anything was committed.
func (s *Store) Sync(ctx context.Context, did string, key atcrypto.PrivateKey, desired []Record) (bool, error) {
	if _, err := syntax.ParseDID(did); err != nil {
		return false, err
	}
	want := make(map[string]recordRow, len(desired))
	for _, rec := range desired {
		if _, err := syntax.ParseNSID(rec.Collection); err != nil {
			return false, fmt.Errorf("record %s: %w", rec.Path(), err)
		}
		if _, err := syntax.ParseRecordKey(rec.Rkey); err != nil {
			return false, fmt.Errorf("record %s: %w", rec.Path(), err)
		}
		value := map[string]any{"$type": rec.Collection}
		for k, v := range rec.Value {
			value[k] = v
		}
		raw, err := atdata.MarshalCBOR(value)
		if err != nil {
			return false, fmt.Errorf("encoding %s: %w", rec.Path(), err)
		}
		c, err := cborCid(raw)
		if err != nil {
			return false, err
		}
		want[rec.Path()] = recordRow{Did: did, Collection: rec.Collection, Rkey: rec.Rkey, Cid: c.String(), Value: raw}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var acct accountRow
	err := s.db.First(&acct, "did = ?", did).Error
	fresh := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !fresh {
		return false, err
	}

	var (
		frames []eventRow
		final  *built
		old    map[string]bool
	)
	if fresh {
		identity, err := identityFrame(did, s.seq(), s.now())
		if err != nil {
			return false, err
		}
		account, err := accountFrame(did, true, "", s.seq(), s.now())
		if err != nil {
			return false, err
		}
		acct = accountRow{Did: did, Active: true}
		genesis, err := s.commit(ctx, &acct, key, nil, map[string]recordRow{}, map[string]recordRow{})
		if err != nil {
			return false, err
		}
		frames = append(frames, identity, account, genesis.frame)
		final, old = genesis, cidSet(genesis.blocks)
	} else if old, err = s.blockCids(did); err != nil {
		return false, err
	}

	have, err := s.records(did)
	if err != nil {
		return false, err
	}
	changed := len(have) != len(want)
	for path, row := range want {
		if prev, ok := have[path]; !ok || prev.Cid != row.Cid {
			changed = true
			break
		}
	}
	if !changed && !fresh {
		return false, nil
	}

	if changed {
		if final, err = s.commit(ctx, &acct, key, old, have, want); err != nil {
			return false, err
		}
		frames = append(frames, final.frame)
	}
	if fresh {
		sync, err := s.syncFrame(&acct, final)
		if err != nil {
			return false, err
		}
		frames = append(frames, sync)
	}
	if err := s.persist(&acct, final, want, frames); err != nil {
		return false, err
	}
	return true, nil
}

// SetActive flips a hosted repo's account status, announcing the change.
// status is ignored when active; otherwise "deactivated" or "deleted".
func (s *Store) SetActive(did string, active bool, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var acct accountRow
	if err := s.db.First(&acct, "did = ?", did).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if active {
		status = ""
	}
	if acct.Active == active && acct.Status == status {
		return nil
	}
	acct.Active, acct.Status = active, status
	frame, err := accountFrame(did, active, status, s.seq(), s.now())
	if err != nil {
		return err
	}
	return s.persist(&acct, nil, nil, []eventRow{frame})
}

type built struct {
	commit    *repo.Commit
	commitCid cid.Cid
	blocks    []blockRow // full current state: commit, MST nodes, records
	frame     eventRow
}

// commit signs a new commit moving acct from have to want; old is the set of
// block CIDs the previous state held, which the frame's CAR omits. It mutates
// acct's head fields but writes nothing.
func (s *Store) commit(ctx context.Context, acct *accountRow, key atcrypto.PrivateKey, old map[string]bool, have, want map[string]recordRow) (*built, error) {
	leaves := make(map[string]cid.Cid, len(want))
	for path, row := range want {
		c, err := cid.Decode(row.Cid)
		if err != nil {
			return nil, err
		}
		leaves[path] = c
	}
	tree, err := mst.LoadTreeFromMap(leaves)
	if err != nil {
		return nil, err
	}
	bs := newMemBlockstore()
	root, err := tree.WriteDiffBlocks(ctx, bs)
	if err != nil {
		return nil, err
	}

	rev := s.clock.Next()
	commit := repo.Commit{DID: acct.Did, Version: repo.ATPROTO_REPO_VERSION, Data: *root, Rev: rev.String()}
	if err := commit.Sign(key); err != nil {
		return nil, fmt.Errorf("signing commit: %w", err)
	}
	var buf bytes.Buffer
	if err := commit.MarshalCBOR(&buf); err != nil {
		return nil, err
	}
	commitCid, err := cborCid(buf.Bytes())
	if err != nil {
		return nil, err
	}

	blocks := []blockRow{{Did: acct.Did, Cid: commitCid.String(), Kind: kindCommit, Data: buf.Bytes()}}
	for _, b := range bs.sorted() {
		blocks = append(blocks, blockRow{Did: acct.Did, Cid: b.Cid().String(), Kind: kindNode, Data: b.RawData()})
	}
	for _, path := range sortedKeys(want) {
		row := want[path]
		blocks = append(blocks, blockRow{Did: acct.Did, Cid: row.Cid, Kind: kindRecord, Data: row.Value})
	}

	var diff []blockRow
	for _, b := range blocks {
		if !old[b.Cid] {
			diff = append(diff, b)
		}
	}
	car, err := encodeCAR(commitCid, diff)
	if err != nil {
		return nil, err
	}

	evt := &comatproto.SyncSubscribeRepos_Commit{
		Repo:   acct.Did,
		Rev:    rev.String(),
		Seq:    rev.Time().UnixMicro(),
		Commit: lexutil.LexLink(commitCid),
		Blocks: car,
		Ops:    ops(have, want),
		Blobs:  []lexutil.LexLink{},
		Time:   rev.Time().UTC().Format(time.RFC3339Nano),
	}
	if acct.Rev != "" {
		since := acct.Rev
		evt.Since = &since
		prevData, err := cid.Decode(acct.Data)
		if err != nil {
			return nil, err
		}
		link := lexutil.LexLink(prevData)
		evt.PrevData = &link
	}
	frame, err := encodeFrame("#commit", evt)
	if err != nil {
		return nil, err
	}

	acct.Rev, acct.Head, acct.Data = rev.String(), commitCid.String(), root.String()
	return &built{
		commit:    &commit,
		commitCid: commitCid,
		blocks:    blocks,
		frame:     eventRow{Seq: evt.Seq, Did: acct.Did, Frame: frame},
	}, nil
}

func (s *Store) syncFrame(acct *accountRow, b *built) (eventRow, error) {
	commitBlock := b.blocks[0]
	head, err := cid.Decode(acct.Head)
	if err != nil {
		return eventRow{}, err
	}
	car, err := encodeCAR(head, []blockRow{commitBlock})
	if err != nil {
		return eventRow{}, err
	}
	seq := s.seq()
	frame, err := encodeFrame("#sync", &comatproto.SyncSubscribeRepos_Sync{
		Did:    acct.Did,
		Rev:    acct.Rev,
		Seq:    seq,
		Blocks: car,
		Time:   s.now(),
	})
	if err != nil {
		return eventRow{}, err
	}
	return eventRow{Seq: seq, Did: acct.Did, Frame: frame}, nil
}

// persist writes the account, its new state (when b is non-nil) and frames in
// one transaction, then wakes subscribers.
func (s *Store) persist(acct *accountRow, b *built, records map[string]recordRow, frames []eventRow) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(acct).Error; err != nil {
			return err
		}
		if b != nil {
			if err := tx.Where("did = ?", acct.Did).Delete(&blockRow{}).Error; err != nil {
				return err
			}
			if err := tx.CreateInBatches(b.blocks, 100).Error; err != nil {
				return err
			}
			if err := tx.Where("did = ?", acct.Did).Delete(&recordRow{}).Error; err != nil {
				return err
			}
			if len(records) > 0 {
				rows := make([]recordRow, 0, len(records))
				for _, path := range sortedKeys(records) {
					rows = append(rows, records[path])
				}
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(rows, 100).Error; err != nil {
					return err
				}
			}
		}
		if len(frames) > 0 {
			return tx.Create(&frames).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(frames) > 0 {
		s.hub.broadcast()
	}
	return nil
}

// seq hands out a fresh sequence number from the shared TID clock.
func (s *Store) seq() int64 { return s.clock.Next().Time().UnixMicro() }

func (s *Store) now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (s *Store) records(did string) (map[string]recordRow, error) {
	var rows []recordRow
	if err := s.db.Where("did = ?", did).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]recordRow, len(rows))
	for _, r := range rows {
		out[r.path()] = r
	}
	return out, nil
}

func (s *Store) blockCids(did string) (map[string]bool, error) {
	var cids []string
	if err := s.db.Model(&blockRow{}).Where("did = ?", did).Pluck("cid", &cids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(cids))
	for _, c := range cids {
		out[c] = true
	}
	return out, nil
}

func cidSet(blocks []blockRow) map[string]bool {
	out := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		out[b.Cid] = true
	}
	return out
}

func ops(have, want map[string]recordRow) []*comatproto.SyncSubscribeRepos_RepoOp {
	paths := map[string]bool{}
	for p := range have {
		paths[p] = true
	}
	for p := range want {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	out := []*comatproto.SyncSubscribeRepos_RepoOp{}
	for _, p := range sorted {
		prev, had := have[p]
		next, wants := want[p]
		switch {
		case !had && wants:
			out = append(out, &comatproto.SyncSubscribeRepos_RepoOp{Action: "create", Path: p, Cid: link(next.Cid)})
		case had && !wants:
			out = append(out, &comatproto.SyncSubscribeRepos_RepoOp{Action: "delete", Path: p, Prev: link(prev.Cid)})
		case prev.Cid != next.Cid:
			out = append(out, &comatproto.SyncSubscribeRepos_RepoOp{Action: "update", Path: p, Cid: link(next.Cid), Prev: link(prev.Cid)})
		}
	}
	return out
}

func link(s string) *lexutil.LexLink {
	c, err := cid.Decode(s)
	if err != nil {
		return nil
	}
	l := lexutil.LexLink(c)
	return &l
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
