package events

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"tangled.org/core/eventstream"
	"tangled.org/core/notifier"

	"github.com/isabelroses/snot/internal/state"
)

func TestEventsStreamDeliversInserted(t *testing.T) {
	db, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.SQL()
	if err != nil {
		t.Fatal(err)
	}
	n := notifier.New()

	srv := httptest.NewServer(Handler(store, &n, slog.Default()))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "?cursor=0"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ev := eventstream.Event{Rkey: "rk-1", Nsid: "sh.tangled.git.refUpdate", EventJson: json.RawMessage(`{"a":1}`)}
	if err := eventstream.Insert(store, ev, &n); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "sh.tangled.git.refUpdate") {
		t.Errorf("unexpected message: %s", msg)
	}
}
