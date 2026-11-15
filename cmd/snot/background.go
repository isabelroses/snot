package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/publish"
	"github.com/isabelroses/snot/internal/repodid"
	"github.com/isabelroses/snot/internal/state"
)

// resyncInterval bounds how stale a published record can get when a change
// arrives without a webhook (default branch, description, visibility).
const resyncInterval = 15 * time.Minute

type background struct {
	cfg    *config.Config
	base   string
	pub    *publish.Publisher
	dids   *state.RepoDids
	logger *slog.Logger
}

// run backfills records for every repo, then announces the firehose to
// relays (so hydrant sees the backfill) once the server is listening, then
// resyncs periodically.
func (b *background) run(ctx context.Context) {
	b.resync(ctx)
	b.upgradeDids(ctx)
	requestCrawls(ctx, b.cfg.ListenAddr, b.cfg.Crawlers, b.cfg.Hostname, crawlRetryPause, b.logger)

	t := time.NewTicker(resyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.resync(ctx)
		}
	}
}

func (b *background) resync(ctx context.Context) {
	if err := b.pub.SyncAll(ctx); err != nil && ctx.Err() == nil {
		b.logger.Error("resync", "err", err)
	}
}

// upgradeDids adds the tangled_knot service to repo DIDs minted before
// knot2, which named only atproto_pds.
func (b *background) upgradeDids(ctx context.Context) {
	all, err := b.dids.All()
	if err != nil {
		b.logger.Error("listing repo DIDs", "err", err)
		return
	}
	for _, did := range all {
		info, ok := b.dids.Get(did)
		if !ok || info.Deleted || !strings.HasPrefix(did, "did:plc:") {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		changed, err := repodid.EnsureServices(ctx, b.cfg.PlcUrl, did, info.Key, b.base)
		switch {
		case err != nil:
			b.logger.Warn("upgrading repo DID services", "did", did, "err", err)
		case changed:
			b.logger.Info("added tangled_knot service to repo DID", "did", did)
		}
	}
}
