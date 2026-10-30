// Package repodid mints and maintains per-repo did:plc identities. Each DID
// names the knot as both its tangled_knot service (what tangled resolves) and
// its atproto_pds service (what relays crawl), the way knot2 mints them.
package repodid

import (
	"context"
	"fmt"
	"net/url"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/did-method-plc/go-didplc/didplc"
)

const (
	KnotServiceID   = "tangled_knot"
	KnotServiceType = "TangledKnot"
	PdsServiceID    = "atproto_pds"
	PdsServiceType  = "AtprotoPersonalDataServer"

	userAgent = "snot"
)

// Prepared is a signed genesis op not yet submitted to the directory.
type Prepared struct {
	Did string
	Key []byte // raw K-256 private key: rotation and #atproto signing key
	op  *didplc.RegularOp
}

// Prepare builds and signs a genesis op naming knotURL as the repo's home.
func Prepare(knotURL string) (*Prepared, error) {
	key, err := atcrypto.GeneratePrivateKeyK256()
	if err != nil {
		return nil, fmt.Errorf("generating signing key: %w", err)
	}
	pub, err := key.PublicKey()
	if err != nil {
		return nil, err
	}
	didKey := pub.DIDKey()
	op := didplc.RegularOp{
		Type:                "plc_operation",
		RotationKeys:        []string{didKey},
		VerificationMethods: map[string]string{"atproto": didKey},
		AlsoKnownAs:         []string{},
		Services:            services(knotURL),
	}
	if err := op.Sign(key); err != nil {
		return nil, fmt.Errorf("signing genesis op: %w", err)
	}
	did, err := op.DID()
	if err != nil {
		return nil, err
	}
	return &Prepared{Did: did, Key: key.Bytes(), op: &op}, nil
}

// Mint prepares a genesis op and submits it. Returns the DID and its key.
func Mint(ctx context.Context, plcURL, knotURL string) (string, []byte, error) {
	if err := checkPlcURL(plcURL); err != nil {
		return "", nil, err
	}
	p, err := Prepare(knotURL)
	if err != nil {
		return "", nil, err
	}
	client := didplc.Client{DirectoryURL: plcURL, UserAgent: userAgent}
	if err := client.Submit(ctx, p.Did, p.op); err != nil {
		return "", nil, fmt.Errorf("submitting repo DID: %w", err)
	}
	return p.Did, p.Key, nil
}

// EnsureServices appends a PLC op pointing both knot services at knotURL if
// the DID's latest op does not already. DIDs minted before knot2 named only
// atproto_pds, a fallback knotmirror means to drop. It reports whether an op
// was submitted.
func EnsureServices(ctx context.Context, plcURL, did string, rawKey []byte, knotURL string) (bool, error) {
	if err := checkPlcURL(plcURL); err != nil {
		return false, err
	}
	key, err := atcrypto.ParsePrivateBytesK256(rawKey)
	if err != nil {
		return false, err
	}
	client := didplc.Client{DirectoryURL: plcURL, UserAgent: userAgent}
	ops, err := client.OpLog(ctx, did)
	if err != nil {
		return false, fmt.Errorf("reading op log: %w", err)
	}
	if len(ops) == 0 {
		return false, fmt.Errorf("%s has an empty op log", did)
	}
	last := ops[len(ops)-1].Regular
	if last == nil {
		return false, fmt.Errorf("%s: latest op is not a regular operation", did)
	}
	want := services(knotURL)
	if sameServices(last.Services, want) {
		return false, nil
	}

	next := didplc.RegularOp{
		Type:                "plc_operation",
		RotationKeys:        last.RotationKeys,
		VerificationMethods: last.VerificationMethods,
		AlsoKnownAs:         last.AlsoKnownAs,
		Services:            map[string]didplc.OpService{},
		Prev:                ptr(last.CID().String()),
	}
	for id, svc := range last.Services {
		next.Services[id] = svc
	}
	for id, svc := range want {
		next.Services[id] = svc
	}
	if err := next.Sign(key); err != nil {
		return false, fmt.Errorf("signing update op: %w", err)
	}
	if err := client.Submit(ctx, did, &next); err != nil {
		return false, fmt.Errorf("submitting update op: %w", err)
	}
	return true, nil
}

func services(knotURL string) map[string]didplc.OpService {
	return map[string]didplc.OpService{
		KnotServiceID: {Type: KnotServiceType, Endpoint: knotURL},
		PdsServiceID:  {Type: PdsServiceType, Endpoint: knotURL},
	}
}

func sameServices(have, want map[string]didplc.OpService) bool {
	for id, svc := range want {
		if have[id] != svc {
			return false
		}
	}
	return true
}

func checkPlcURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("PLC directory URL is invalid: %q", raw)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
