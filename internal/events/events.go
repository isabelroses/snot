// Package events serves a valid-but-empty knot eventstream: the appview's
// consumer connects and idles; no history is fabricated.
package events

import (
	"log/slog"
	"net/http"

	"tangled.org/core/eventstream"
	"tangled.org/core/notifier"
)

type emptyBackend struct{}

func (emptyBackend) GetEvents(cursor int64, limit int) ([]eventstream.Event, error) {
	return nil, nil
}

func Handler(n *notifier.Notifier, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = eventstream.Stream(w, r, eventstream.StreamConfig{
			Backend:  emptyBackend{},
			Notifier: n,
			Logger:   l,
		})
	}
}
