package handover

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"

	"github.com/isabelroses/snot/internal/state"
)

const repoDid = "did:plc:t7jdijz7wercjdwk6o42tl2a"

// documentation is a label definition exactly as tangled's foster knot serves
// it, with the CID it serves it at.
const (
	documentationCid   = "bafyreigvtimdri2sy3slh7cauvpbghne3x35scy7xatiytup3wjruf74fm"
	documentationValue = `{"$type":"org.tangled.label.definition","color":"#06B6D4","createdAt":"2025-09-22T10:14:35Z","name":"documentation","x-tngl-editor":"did:plc:qxichs7jsycphrsmbujwqbfb"}`
	duplicateCid       = "bafyreigngrrkotx7alph2n3lwmn665o643sxwbifsog6hdcitfy3zdf64m"
	duplicateValue     = `{"$type":"org.tangled.label.definition","color":"#EF4444","createdAt":"2025-09-22T10:14:35Z","name":"duplicate","x-tngl-editor":"did:plc:qxichs7jsycphrsmbujwqbfb"}`
)

type ack struct {
	Context   string `json:"context"`
	RepoDid   string `json:"repoDid"`
	Records   int    `json:"records"`
	AckedAt   int64  `json:"ackedAt"`
	Signature string `json:"signature"`
}

// foster is a stand-in foster knot shadowing repoDid with two label
// definitions, served one per page, plus the placeholder derived records.
type foster struct {
	*httptest.Server
	shadowed bool
	labelCid string // CID reported for the documentation label
	acks     []ack
}

func newFoster(t *testing.T) *foster {
	t.Helper()
	f := &foster{shadowed: true, labelCid: documentationCid}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /xrpc/com.atproto.repo.describeRepo", func(w http.ResponseWriter, r *http.Request) {
		if !f.shadowed {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"RepoNotFound","message":"couldn't find repo"}`)
			return
		}
		io.WriteString(w, `{"collections":["org.tangled.git.refs","org.tangled.label.definition","org.tangled.repo.manifest"]}`)
	})
	mux.HandleFunc("GET /xrpc/com.atproto.repo.listRecords", func(w http.ResponseWriter, r *http.Request) {
		if c := r.URL.Query().Get("collection"); c != "org.tangled.label.definition" {
			t.Errorf("listed derived collection %s", c)
		}
		uri := "at://" + repoDid + "/org.tangled.label.definition/"
		switch r.URL.Query().Get("cursor") {
		case "":
			io.WriteString(w, `{"cursor":"documentation","records":[{"uri":"`+uri+`documentation","cid":"`+f.labelCid+`","value":`+documentationValue+`}]}`)
		case "documentation":
			io.WriteString(w, `{"cursor":"duplicate","records":[{"uri":"`+uri+`duplicate","cid":"`+duplicateCid+`","value":`+duplicateValue+`}]}`)
		default:
			io.WriteString(w, `{"records":[]}`)
		}
	})
	mux.HandleFunc("POST /xrpc/org.tangled.temp.server.ackHandover", func(w http.ResponseWriter, r *http.Request) {
		var a ack
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			t.Error(err)
		}
		f.acks = append(f.acks, a)
		io.WriteString(w, `{}`)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func newClient(t *testing.T, f *foster) (*Client, *state.Handovers, *int) {
	t.Helper()
	db, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := atcrypto.GeneratePrivateKeyK256()
	if err != nil {
		t.Fatal(err)
	}
	published := 0
	store := db.Handovers()
	return &Client{
		FosterURL: f.URL + "/",
		KnotKey:   key,
		Store:     store,
		Publish: func(context.Context, string) error {
			// the adopted records must be stored before they are published
			if recs, _ := store.Records(repoDid); len(recs) != 2 {
				t.Errorf("published with %d adopted records", len(recs))
			}
			published++
			return nil
		},
		HTTP:   f.Client(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Unix(1794700800, 0) },
	}, store, &published
}

// signedAck is the canonical DAG-CBOR knot2's foster rebuilds and verifies:
// a map whose keys sort length-first, then bytewise.
func signedAck(repo string, records byte, ackedAt uint32) []byte {
	text := func(s string) []byte {
		if len(s) < 24 {
			return append([]byte{0x60 | byte(len(s))}, s...)
		}
		return append([]byte{0x78, byte(len(s))}, s...)
	}
	b := []byte{0xa4}
	b = append(b, text("ackedAt")...)
	b = binary.BigEndian.AppendUint32(append(b, 0x1a), ackedAt)
	b = append(b, text("context")...)
	b = append(b, text("org.tangled.temp.handover.ack.v1")...)
	b = append(b, text("records")...)
	b = append(b, records)
	b = append(b, text("repoDid")...)
	return append(b, text(repo)...)
}

func TestHandoverAdoptsRecordsThenAcks(t *testing.T) {
	f := newFoster(t)
	c, store, published := newClient(t, f)

	c.Run(context.Background(), []string{repoDid})

	if *published != 1 || len(f.acks) != 1 {
		t.Fatalf("published %d times, acked %d times", *published, len(f.acks))
	}
	a := f.acks[0]
	if a.Context != "org.tangled.temp.handover.ack.v1" || a.RepoDid != repoDid || a.Records != 2 || a.AckedAt != 1794700800 {
		t.Errorf("ack = %+v", a)
	}
	sig, err := base64.RawURLEncoding.DecodeString(a.Signature)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := c.KnotKey.PublicKey()
	if err := pub.HashAndVerify(signedAck(repoDid, 2, 1794700800), sig); err != nil {
		t.Errorf("the ack signature does not cover knot2's signed bytes: %v", err)
	}
	if settled, _ := store.Settled(repoDid); !settled {
		t.Error("repo not settled after an accepted ack")
	}

	recs, _ := store.Records(repoDid)
	converted, err := Records(recs)
	if err != nil || len(converted) != 2 {
		t.Fatalf("adopted records = %v, %v", converted, err)
	}
	if converted[0].Path() != "org.tangled.label.definition/documentation" || converted[0].Value["color"] != "#06B6D4" {
		t.Errorf("adopted record = %+v", converted[0])
	}

	// settled repos are left alone
	c.Run(context.Background(), []string{repoDid})
	if len(f.acks) != 1 {
		t.Errorf("acked again: %d", len(f.acks))
	}
}

func TestHandoverSettlesUnshadowedRepoWithoutAck(t *testing.T) {
	f := newFoster(t)
	f.shadowed = false
	c, store, published := newClient(t, f)

	c.Run(context.Background(), []string{repoDid})

	if len(f.acks) != 0 || *published != 0 {
		t.Errorf("acks=%d published=%d for a repo the foster does not shadow", len(f.acks), *published)
	}
	if settled, _ := store.Settled(repoDid); !settled {
		t.Error("an unshadowed repo should settle")
	}
}

func TestHandoverRefusesToAckWhenCidWouldDiffer(t *testing.T) {
	f := newFoster(t)
	f.labelCid = duplicateCid // not what the documentation label encodes to
	c, store, published := newClient(t, f)

	c.Run(context.Background(), []string{repoDid})

	if len(f.acks) != 0 || *published != 0 {
		t.Errorf("acks=%d published=%d despite a CID mismatch", len(f.acks), *published)
	}
	if settled, _ := store.Settled(repoDid); settled {
		t.Error("a failed handover must stay unsettled so it is retried")
	}
}

func TestHandoverRetriesAfterRejectedAck(t *testing.T) {
	f := newFoster(t)
	reject := true
	inner := f.Config.Handler
	f.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject && r.Method == http.MethodPost {
			http.Error(w, `{"error":"InvalidRequest","message":"doesn't serve COBs"}`, http.StatusBadRequest)
			return
		}
		inner.ServeHTTP(w, r)
	})
	c, store, _ := newClient(t, f)

	c.Run(context.Background(), []string{repoDid})
	if settled, _ := store.Settled(repoDid); settled {
		t.Fatal("settled although the foster rejected the ack")
	}

	reject = false
	c.Run(context.Background(), []string{repoDid})
	if settled, _ := store.Settled(repoDid); !settled || len(f.acks) != 1 {
		t.Errorf("settled=%v acks=%d after the retry", settled, len(f.acks))
	}
}
