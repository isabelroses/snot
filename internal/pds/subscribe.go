package pds

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/isabelroses/snot/internal/atrepo"
)

const (
	replayBatch  = 500
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
)

var upgrader = websocket.Upgrader{
	// relays and indexers connect server-to-server; there is no browser
	// session to protect.
	CheckOrigin: func(*http.Request) bool { return true },
}

// subscribeRepos streams sequenced frames: everything after ?cursor= first,
// then live. Without a cursor the stream starts at the current head.
func (h *Handler) subscribeRepos(w http.ResponseWriter, r *http.Request) {
	var cursor *int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "InvalidRequest", "cursor must be a non-negative integer")
			return
		}
		cursor = &n
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already answered
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// the client never speaks, but reading is how close and pong arrive
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	wake, stop := h.Store.Subscribe()
	defer stop()

	head, err := h.Store.LastSeq()
	if err != nil {
		h.Logger.Error("subscribeRepos: reading head", "err", err)
		return
	}
	last := head
	if cursor != nil {
		if *cursor > head {
			if frame, err := encodeErrorFrame("FutureCursor", "cursor is ahead of this knot's stream"); err == nil {
				conn.SetWriteDeadline(time.Now().Add(writeTimeout))
				conn.WriteMessage(websocket.BinaryMessage, frame)
			}
			return
		}
		last = *cursor
	}

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		frames, err := h.Store.FramesAfter(last, replayBatch)
		if err != nil {
			h.Logger.Error("subscribeRepos: reading frames", "err", err)
			return
		}
		for _, f := range frames {
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.BinaryMessage, f.Data); err != nil {
				return
			}
			last = f.Seq
		}
		if len(frames) == replayBatch {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				return
			}
		}
	}
}

func encodeErrorFrame(name, msg string) ([]byte, error) { return atrepo.ErrorFrame(name, msg) }
