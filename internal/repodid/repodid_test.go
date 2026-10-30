package repodid

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/did-method-plc/go-didplc/didplc"
)

func TestPrepareNamesBothKnotServices(t *testing.T) {
	p, err := Prepare("https://knot.example.com")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasPrefix(p.Did, "did:plc:") {
		t.Errorf("Did = %q, want did:plc: prefix", p.Did)
	}
	for _, id := range []string{KnotServiceID, PdsServiceID} {
		if got := p.op.Services[id].Endpoint; got != "https://knot.example.com" {
			t.Errorf("service %s endpoint = %q", id, got)
		}
	}
	key, err := atcrypto.ParsePrivateBytesK256(p.Key)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := key.PublicKey()
	if p.op.VerificationMethods["atproto"] != pub.DIDKey() {
		t.Error("#atproto verification method is not the returned key")
	}
}

func TestMintPropagatesSubmitError(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer stub.Close()

	if _, _, err := Mint(context.Background(), stub.URL, "https://knot.example.com"); err == nil {
		t.Error("Mint should return error when PLC directory rejects submission")
	}
}

// legacyGenesis mints the pre-knot2 shape: atproto_pds only.
func legacyGenesis(t *testing.T) (string, []byte, didplc.RegularOp) {
	t.Helper()
	key, _ := atcrypto.GeneratePrivateKeyK256()
	pub, _ := key.PublicKey()
	op := didplc.RegularOp{
		Type:                "plc_operation",
		RotationKeys:        []string{pub.DIDKey()},
		VerificationMethods: map[string]string{"atproto": pub.DIDKey()},
		AlsoKnownAs:         []string{},
		Services:            map[string]didplc.OpService{PdsServiceID: {Type: PdsServiceType, Endpoint: "https://knot.example.com"}},
	}
	if err := op.Sign(key); err != nil {
		t.Fatal(err)
	}
	did, _ := op.DID()
	return did, key.Bytes(), op
}

func TestEnsureServicesUpgradesLegacyDid(t *testing.T) {
	did, rawKey, genesis := legacyGenesis(t)
	log := []didplc.OpEnum{{Regular: &genesis}}

	var submitted *didplc.RegularOp
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/"+did+"/log":
			json.NewEncoder(w).Encode(log)
		case r.Method == http.MethodPost && r.URL.Path == "/"+did:
			body, _ := io.ReadAll(r.Body)
			var op didplc.RegularOp
			if err := json.Unmarshal(body, &op); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			submitted = &op
			log = append(log, didplc.OpEnum{Regular: &op})
		default:
			http.NotFound(w, r)
		}
	}))
	defer stub.Close()

	ctx := context.Background()
	changed, err := EnsureServices(ctx, stub.URL, did, rawKey, "https://knot.example.com")
	if err != nil || !changed {
		t.Fatalf("EnsureServices = %v, %v", changed, err)
	}
	if submitted.Services[KnotServiceID].Type != KnotServiceType {
		t.Errorf("tangled_knot not added: %+v", submitted.Services)
	}
	if submitted.Prev == nil || *submitted.Prev != genesis.CID().String() {
		t.Errorf("update op must chain from genesis")
	}
	key, _ := atcrypto.ParsePrivateBytesK256(rawKey)
	pub, _ := key.PublicKey()
	if err := submitted.VerifySignature(pub); err != nil {
		t.Errorf("update op signature: %v", err)
	}

	// second run sees both services and submits nothing
	submitted = nil
	if changed, err := EnsureServices(ctx, stub.URL, did, rawKey, "https://knot.example.com"); err != nil || changed {
		t.Fatalf("rerun = %v, %v", changed, err)
	}
	if submitted != nil {
		t.Error("rerun submitted an op")
	}
}
