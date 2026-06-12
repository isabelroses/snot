package events

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"tangled.org/core/notifier"
)

func TestWebsocketConnects(t *testing.T) {
	n := notifier.New()
	srv := httptest.NewServer(Handler(&n, slog.Default()))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "?cursor=0"
	conn, res, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v (res=%v)", err, res)
	}
	conn.Close()
}
