package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"tangled.org/core/api/tangled"
	"tangled.org/core/eventstream"
	tgit "tangled.org/core/knotserver/git"
	"tangled.org/core/notifier"
	"tangled.org/core/tid"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/resolve"
)

const publishRetryDelay = 5 * time.Second

// maxBodyBytes bounds a push payload; Forgejo's are a few KB per commit.
const maxBodyBytes = 5 << 20

type Handler struct {
	Cfg      *config.Config
	Resolve  *resolve.Resolver
	Store    eventstream.Store
	Notifier *notifier.Notifier
	Logger   *slog.Logger
	// Publish republishes the repo's org.tangled.git.refs record; nil skips.
	Publish interface {
		SyncRepo(ctx context.Context, repoDid string) error
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// unsigned webhooks would let anyone forge push events into /events
	if h.Cfg.WebhookSecret == "" {
		h.Logger.Warn("webhook: rejected, SNOT_WEBHOOK_SECRET unset")
		http.Error(w, "webhooks disabled: webhook_secret is not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.Cfg.WebhookSecret, body, r.Header.Get("X-Gitea-Signature")) {
		h.Logger.Warn("webhook: bad signature")
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
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
	if err := push.validate(); err != nil {
		h.Logger.Warn("webhook: rejected push payload", "err", err)
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
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

	if err := h.enrich(r.Context(), ownerDid, name, push, &refUpdate); err != nil {
		h.Logger.Warn("webhook: meta computation failed, emitting without meta", "repo", repoDid, "err", err)
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

	// The refs record is what knotmirror fetches from. A failure is not
	// surfaced to Forgejo: its redelivery would duplicate the event above
	// (and any CI it triggers), so retry here and let the periodic resync
	// converge if that fails too.
	if h.Publish != nil {
		if err := h.Publish.SyncRepo(r.Context(), repoDid); err != nil {
			h.Logger.Error("webhook: publishing refs, retrying", "repo", repoDid, "err", err)
			go h.retryPublish(repoDid)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) retryPublish(repoDid string) {
	time.Sleep(publishRetryDelay)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := h.Publish.SyncRepo(ctx, repoDid); err != nil {
		h.Logger.Error("webhook: publishing refs failed again; awaiting resync", "repo", repoDid, "err", err)
	}
}

// enrich fills in commit-count + language metadata and the changed-file list
// from the bare repo, mirroring tangled's knotserver. Branch/tag deletes
// (zero NewSha) are left untouched, matching upstream which only computes
// these otherwise.
func (h *Handler) enrich(ctx context.Context, ownerDid, name string, push *pushPayload, ru *tangled.GitRefUpdate) error {
	newHash := plumbing.NewHash(push.After)
	if newHash.IsZero() {
		return nil
	}
	_, repoPath, err := h.Resolve.Repo(ctx, ownerDid, name)
	if err != nil {
		return err
	}
	gr, err := tgit.Open(repoPath, push.Ref)
	if err != nil {
		return err
	}
	changedFiles, err := gr.ChangedFilesBetween(push.Before, push.After)
	if err != nil {
		return err
	}
	meta, err := gr.RefUpdateMeta(tgit.PostReceiveLine{
		OldSha: plumbing.NewHash(push.Before),
		NewSha: newHash,
		Ref:    push.Ref,
	})
	if err != nil {
		return err
	}
	rec := meta.AsRecord()
	ru.Meta = &rec
	ru.ChangedFiles = changedFiles
	return nil
}
