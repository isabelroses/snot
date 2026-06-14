package state

import (
	"encoding/json"
	"testing"

	"tangled.org/core/eventstream"
	"tangled.org/core/notifier"
)

func TestEventsTableRoundTrip(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.SQL()
	if err != nil {
		t.Fatal(err)
	}

	n := notifier.New()
	ev := eventstream.Event{
		Rkey:      "rk-1",
		Nsid:      "sh.tangled.git.refUpdate",
		EventJson: json.RawMessage(`{"hello":"world"}`),
	}
	if err := eventstream.Insert(store, ev, &n); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := eventstream.List(store, 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Rkey != "rk-1" || got[0].Nsid != "sh.tangled.git.refUpdate" {
		t.Fatalf("got %+v", got)
	}
	if string(got[0].EventJson) != `{"hello":"world"}` {
		t.Errorf("event json = %s", got[0].EventJson)
	}
	if more, _ := eventstream.List(store, got[0].Created, 10); len(more) != 0 {
		t.Errorf("expected no rows past cursor, got %d", len(more))
	}
}
