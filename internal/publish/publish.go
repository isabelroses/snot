// Package publish derives the atproto records a knot2-era knot hosts from
// snot's sources of truth (Forgejo repos and the ACL tables) and keeps
// atrepo reconciled with them:
//
//   - each repo DID holds org.tangled.repo.manifest/self,
//     org.tangled.git.refs/self and one org.tangled.graph.collaborator per
//     collaborator;
//   - the knot's did:web holds one org.tangled.knot.member per member.
//
// Every entry point is idempotent, so callers simply resync on any change.
package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/isabelroses/snot/internal/atrepo"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
)

const (
	ManifestNSID     = "org.tangled.repo.manifest"
	RefsNSID         = "org.tangled.git.refs"
	CollaboratorNSID = "org.tangled.graph.collaborator"
	MemberNSID       = "org.tangled.knot.member"

	selfRkey = "self"

	// maxRefsBytes mirrors the lexicon's maxLength; knot2 publishes no refs
	// record at all for a repo past it rather than a truncated one.
	maxRefsBytes         = 1_000_000
	maxDescriptionLength = 140
)

type Publisher struct {
	Store   *atrepo.Store
	Resolve *resolve.Resolver
	ACL     *state.ACL
	KnotDid string
	KnotKey atcrypto.PrivateKey
	Logger  *slog.Logger
}

// SyncKnot publishes the member roll to the knot's own repo.
func (p *Publisher) SyncKnot(ctx context.Context) error {
	members, err := p.ACL.Members()
	if err != nil {
		return err
	}
	recs := make([]atrepo.Record, 0, len(members))
	for _, m := range members {
		recs = append(recs, atrepo.Record{
			Collection: MemberNSID,
			Rkey:       m.Subject,
			Value:      map[string]any{"createdAt": datetime(m.Created)},
		})
	}
	changed, err := p.Store.Sync(ctx, p.KnotDid, p.KnotKey, recs)
	if changed {
		p.Logger.Info("published knot members", "did", p.KnotDid, "members", len(recs))
	}
	return err
}

// SyncRepo reconciles one repo DID with Forgejo. Repos that vanished, went
// private, or lost their owner mapping are deactivated rather than deleted,
// so they come back intact if the cause is undone; repos unpublished through
// deleteRepo are marked deleted.
func (p *Publisher) SyncRepo(ctx context.Context, repoDid string) error {
	info, ok := p.Resolve.Dids.Get(repoDid)
	if !ok {
		return fmt.Errorf("publish: unknown repo DID %s", repoDid)
	}
	if info.Deleted {
		return p.deactivate(repoDid, "deleted")
	}
	key, err := atcrypto.ParsePrivateBytesK256(info.Key)
	if err != nil {
		return fmt.Errorf("publish: repo key for %s: %w", repoDid, err)
	}

	repo, path, err := p.Resolve.RepoFromParam(ctx, repoDid)
	if errors.Is(err, forgejo.ErrNotFound) {
		return p.deactivate(repoDid, "deactivated")
	}
	if err != nil {
		return err
	}
	ownerDid, ok := p.Resolve.DidFor(repo.OwnerName)
	if !ok {
		return p.deactivate(repoDid, "deactivated")
	}

	recs := []atrepo.Record{manifest(ownerDid, repo)}
	refs, err := refsRecord(path, repo.DefaultBranch)
	if err != nil {
		return fmt.Errorf("publish: reading refs of %s: %w", repoDid, err)
	}
	if refs != nil {
		recs = append(recs, *refs)
	} else {
		p.Logger.Warn("refs exceed the record limit; publishing no refs record", "repo", repoDid)
	}

	collabs, err := p.ACL.Collaborators(repoDid)
	if err != nil {
		return err
	}
	for _, c := range collabs {
		recs = append(recs, atrepo.Record{
			Collection: CollaboratorNSID,
			Rkey:       c.Subject,
			Value:      map[string]any{"createdAt": datetime(c.Created)},
		})
	}

	changed, err := p.Store.Sync(ctx, repoDid, key, recs)
	if err != nil {
		return err
	}
	if changed {
		p.Logger.Info("published repo", "did", repoDid, "repo", repo.OwnerName+"/"+repo.Name)
	}
	if err := p.Store.SetActive(repoDid, true, ""); err != nil && !errors.Is(err, atrepo.ErrNotFound) {
		return err
	}
	return nil
}

func (p *Publisher) deactivate(repoDid, status string) error {
	err := p.Store.SetActive(repoDid, false, status)
	if errors.Is(err, atrepo.ErrNotFound) {
		return nil // never published; nothing to announce
	}
	return err
}

// SyncAll reconciles the knot and every repo DID, logging per-repo failures
// so one broken repo cannot stall the rest.
func (p *Publisher) SyncAll(ctx context.Context) error {
	if err := p.SyncKnot(ctx); err != nil {
		return fmt.Errorf("publish: knot: %w", err)
	}
	dids, err := p.Resolve.Dids.All()
	if err != nil {
		return err
	}
	var failed int
	for _, did := range dids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.SyncRepo(ctx, did); err != nil {
			failed++
			p.Logger.Error("publish: repo sync failed", "did", did, "err", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("publish: %d of %d repos failed to sync", failed, len(dids))
	}
	return nil
}

func manifest(ownerDid string, repo *forgejo.Repo) atrepo.Record {
	value := map[string]any{
		"declaration": map[string]any{"owner": ownerDid, "name": repo.Name},
		"createdAt":   repo.CreatedAt.UTC().Format(time.RFC3339),
	}
	if desc := truncate(strings.TrimSpace(repo.Description), maxDescriptionLength); desc != "" {
		value["description"] = desc
	}
	return atrepo.Record{Collection: ManifestNSID, Rkey: selfRkey, Value: value}
}

// refsRecord snapshots the repo's public refs in `git show-ref` form, sorted
// by name, the way knot2 does. It returns nil when the snapshot is too big.
func refsRecord(path, fallbackBranch string) (*atrepo.Record, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, err
	}
	defaultBranch := fallbackBranch
	if head, err := r.Storer.Reference(plumbing.HEAD); err == nil && head.Type() == plumbing.SymbolicReference {
		if name := head.Target(); name.IsBranch() {
			defaultBranch = name.Short()
		}
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	type line struct{ name, hash string }
	var lines []line
	iter, err := r.References()
	if err != nil {
		return nil, err
	}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference && publicRef(ref.Name().String()) {
			lines = append(lines, line{ref.Name().String(), ref.Hash().String()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })

	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.hash)
		b.WriteByte(' ')
		b.WriteString(l.name)
		b.WriteByte('\n')
	}
	if b.Len() > maxRefsBytes {
		return nil, nil
	}
	return &atrepo.Record{
		Collection: RefsNSID,
		Rkey:       selfRkey,
		Value:      map[string]any{"defaultBranch": defaultBranch, "refs": atdata.Bytes(b.String())},
	}, nil
}

// publicRef follows knot2's RefClass: everything under refs/ except the
// knot-internal namespaces, plus Forgejo's refs/pull PR bookkeeping, which is
// Forgejo's and not the repository's.
func publicRef(name string) bool {
	rest, ok := strings.CutPrefix(name, "refs/")
	if !ok {
		return false
	}
	ns, _, _ := strings.Cut(rest, "/")
	switch ns {
	case "cobs", "cob-checkpoints", "hidden", "atproto", "tngl", "pull":
		return false
	}
	return true
}

// datetime normalizes stored timestamps to RFC 3339, falling back to the
// epoch so a malformed row still yields a valid, stable record.
func datetime(s string) string {
	if t, err := syntax.ParseDatetimeLenient(s); err == nil {
		return t.String()
	}
	return "1970-01-01T00:00:00Z"
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
