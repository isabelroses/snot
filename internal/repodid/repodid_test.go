package repodid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tangledrepodid "tangled.org/core/knotserver/repodid"
)

// TestPrepareRepoDID checks that PrepareRepoDID produces a valid did:plc DID
// and non-empty signing key without hitting a real PLC directory. Mint itself
// requires a live Submit call, so we test at the prepare level and separately
// verify that Mint propagates a submit error.
func TestPrepareRepoDID(t *testing.T) {
	// Use a stub PLC server that accepts the genesis op.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// go-didplc posts to /<did> — any non-error response is success.
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	prepared, err := tangledrepodid.PrepareRepoDID(stub.URL, "https://knot.example.com")
	if err != nil {
		t.Fatalf("PrepareRepoDID: %v", err)
	}
	if !strings.HasPrefix(prepared.RepoDid, "did:plc:") {
		t.Errorf("RepoDid = %q, want did:plc: prefix", prepared.RepoDid)
	}
	if len(prepared.SigningKeyRaw) == 0 {
		t.Error("SigningKeyRaw is empty")
	}
}

func TestMintPropagatesSubmitError(t *testing.T) {
	// Server that rejects submissions.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer stub.Close()

	_, _, err := Mint(context.Background(), stub.URL, "https://knot.example.com")
	if err == nil {
		t.Error("Mint should return error when PLC directory rejects submission")
	}
}
