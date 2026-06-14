package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"tangled.org/core/api/tangled"
	"tangled.org/core/eventstream"
	tgit "tangled.org/core/knotserver/git"
	"tangled.org/core/notifier"
	"tangled.org/core/tid"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/resolve"
)

type Handler struct {
	Cfg      *config.Config
	Resolve  *resolve.Resolver
	Store    eventstream.Store
	Notifier *notifier.Notifier
	Logger   *slog.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if h.Cfg.WebhookSecret != "" {
		if !verifySignature(h.Cfg.WebhookSecret, body, r.Header.Get("X-Gitea-Signature")) {
			h.Logger.Warn("webhook: bad signature")
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
	} else {
		h.Logger.Warn("webhook: SNOT_WEBHOOK_SECRET unset; accepting unsigned webhook")
	}

	push, err := parsePush(body)
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if push.Ref == "" {
		// not a push event (e.g. ping); ack and ignore.
		w.WriteHeader(http.StatusOK)
		return
	}

	owner := strings.ToLower(push.owner())
	name := strings.ToLower(push.name())

	// Resolve the repo's did:plc; skip cleanly if the owner is unmapped or the
	// repo hasn't been published to tangled.
	ownerDid, ok := h.Resolve.DidFor(owner)
	if !ok {
		h.Logger.Info("webhook: skip, owner not mapped", "owner", owner)
		w.WriteHeader(http.StatusOK)
		return
	}
	repoDid, ok := h.Resolve.Dids.ByRepo(owner, name)
	if !ok {
		h.Logger.Info("webhook: skip, repo not published", "owner", owner, "name", name)
		w.WriteHeader(http.StatusOK)
		return
	}

	committerDid := ownerDid
	if did, ok := h.Resolve.DidFor(strings.ToLower(push.pusher())); ok {
		committerDid = did
	}

	refUpdate := tangled.GitRefUpdate{
		Repo:         repoDid,
		OwnerDid:     &ownerDid,
		CommitterDid: committerDid,
		OldSha:       push.Before,
		NewSha:       push.After,
		Ref:          push.Ref,
	}

	if meta, err := h.computeMeta(r.Context(), ownerDid, name, push); err != nil {
		h.Logger.Warn("webhook: meta computation failed, emitting without meta", "repo", repoDid, "err", err)
	} else if meta != nil {
		refUpdate.Meta = meta
	}

	eventJson, err := json.Marshal(refUpdate)
	if err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
	ev := eventstream.Event{
		Rkey:      tid.TID(),
		Nsid:      tangled.GitRefUpdateNSID,
		EventJson: eventJson,
	}
	if err := eventstream.Insert(h.Store, ev, h.Notifier); err != nil {
		h.Logger.Error("webhook: insert event", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("webhook: emitted refUpdate", "repo", repoDid, "ref", push.Ref, "new", push.After)
	w.WriteHeader(http.StatusOK)
}

// computeMeta builds commit-count + language metadata from the bare repo,
// mirroring tangled's knotserver. Returns nil (no error) for branch/tag
// deletes (zero NewSha), matching upstream which only computes meta then.
func (h *Handler) computeMeta(ctx context.Context, ownerDid, name string, push *pushPayload) (*tangled.GitRefUpdate_Meta, error) {
	newHash := plumbing.NewHash(push.After)
	if newHash.IsZero() {
		return nil, nil
	}
	_, repoPath, err := h.Resolve.Repo(ctx, ownerDid, name)
	if err != nil {
		return nil, err
	}
	gr, err := tgit.Open(repoPath, push.Ref)
	if err != nil {
		return nil, err
	}
	meta, err := gr.RefUpdateMeta(tgit.PostReceiveLine{
		OldSha: plumbing.NewHash(push.Before),
		NewSha: newHash,
		Ref:    push.Ref,
	})
	if err != nil {
		return nil, err
	}
	rec := meta.AsRecord()
	return &rec, nil
}
