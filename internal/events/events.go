// Package events serves snot's knot eventstream over /events, backed by the
// snot.db events table. Push events are written elsewhere (internal/webhook)
// via eventstream.Insert; this just streams them to consumers.
package events

import (
	"log/slog"
	"net/http"

	"tangled.org/core/eventstream"
	"tangled.org/core/notifier"
)

type storeBackend struct {
	store eventstream.Store
}

func (b storeBackend) GetEvents(cursor int64, limit int) ([]eventstream.Event, error) {
	return eventstream.List(b.store, cursor, limit)
}

func Handler(store eventstream.Store, n *notifier.Notifier, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = eventstream.Stream(w, r, eventstream.StreamConfig{
			Backend:  storeBackend{store: store},
			Notifier: n,
			Logger:   l,
		})
	}
}
