package atrepo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/glebarez/sqlite"
	"github.com/ipfs/go-cid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// benchRecords is the record set publish hands over for one repo: a
// manifest, a collaborator, and a refs snapshot of the given number of
// branches. salt varies the snapshot so consecutive syncs differ.
func benchRecords(refs int, salt byte) []Record {
	hash := strings.Repeat(string('a'+rune(salt%6)), 40)
	var b strings.Builder
	for i := range refs {
		fmt.Fprintf(&b, "%s refs/heads/b%05d\n", hash, i)
	}
	return []Record{
		{Collection: "org.tangled.repo.manifest", Rkey: "self", Value: map[string]any{
			"declaration": map[string]any{"owner": "did:plc:owner123", "name": "demo"},
			"createdAt":   "2023-11-14T22:13:20Z",
		}},
		{Collection: "org.tangled.git.refs", Rkey: "self", Value: map[string]any{
			"defaultBranch": "main", "refs": atdata.Bytes(b.String()),
		}},
		{Collection: "org.tangled.graph.collaborator", Rkey: "did:plc:collab456", Value: map[string]any{
			"createdAt": "2023-11-14T22:13:20Z",
		}},
	}
}

// BenchmarkSync measures one repo's reconcile: "unchanged" is the periodic
// resync that finds nothing to do, "changed" is a push that moved a ref.
// 16k refs is just under the refs record's size limit.
func BenchmarkSync(b *testing.B) {
	ctx := context.Background()
	for _, refs := range []int{10, 16000} {
		for _, changed := range []bool{false, true} {
			name := fmt.Sprintf("refs=%d/unchanged", refs)
			if changed {
				name = fmt.Sprintf("refs=%d/changed", refs)
			}
			b.Run(name, func(b *testing.B) {
				db, err := gorm.Open(sqlite.Open(filepath.Join(b.TempDir(), "t.db")), &gorm.Config{
					Logger: logger.Default.LogMode(logger.Silent),
				})
				if err != nil {
					b.Fatal(err)
				}
				s, err := New(db)
				if err != nil {
					b.Fatal(err)
				}
				key, err := atcrypto.GeneratePrivateKeyK256()
				if err != nil {
					b.Fatal(err)
				}
				sets := [][]Record{benchRecords(refs, 0), benchRecords(refs, 1)}
				if _, err := s.Sync(ctx, testDid, key, sets[0]); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					i++
					set := sets[0]
					if changed {
						set = sets[i%2]
					}
					did, err := s.Sync(ctx, testDid, key, set)
					if err != nil {
						b.Fatal(err)
					}
					if did != changed {
						b.Fatalf("Sync reported changed=%v, want %v", did, changed)
					}
				}
			})
		}
	}
}

func benchBlocks(refs int) []blockRow {
	var rows []blockRow
	for _, rec := range benchRecords(refs, 0) {
		raw, _ := atdata.MarshalCBOR(rec.Value)
		c, _ := cborCid(raw)
		rows = append(rows, blockRow{Did: testDid, Cid: c.String(), Kind: kindRecord, Data: raw})
	}
	return rows
}

func BenchmarkEncodeCAR(b *testing.B) {
	for _, refs := range []int{10, 16000} {
		b.Run(fmt.Sprintf("refs=%d", refs), func(b *testing.B) {
			rows := benchBlocks(refs)
			root, _ := cid.Decode(rows[0].Cid)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := encodeCAR(root, rows); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRepoCAR is com.atproto.sync.getRepo's store read.
func BenchmarkRepoCAR(b *testing.B) {
	ctx := context.Background()
	for _, refs := range []int{10, 16000} {
		b.Run(fmt.Sprintf("refs=%d", refs), func(b *testing.B) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(b.TempDir(), "t.db")), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			if err != nil {
				b.Fatal(err)
			}
			s, err := New(db)
			if err != nil {
				b.Fatal(err)
			}
			key, _ := atcrypto.GeneratePrivateKeyK256()
			if _, err := s.Sync(ctx, testDid, key, benchRecords(refs, 0)); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.RepoCAR(testDid); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
