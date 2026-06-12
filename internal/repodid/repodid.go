// Package repodid mints per-repo did:plc identities. The knot is set as the
// DID's atproto_pds service endpoint, acting as a pseudo-PDS that answers
// describeRepo for the repo DID.
package repodid

import (
	"context"
	"fmt"

	tangledrepodid "tangled.org/core/knotserver/repodid"
)

// Mint prepares a did:plc genesis op with the knot as service endpoint and
// submits it to the PLC directory. Returns the DID and its rotation key.
func Mint(ctx context.Context, plcURL, knotServiceURL string) (string, []byte, error) {
	prepared, err := tangledrepodid.PrepareRepoDID(plcURL, knotServiceURL)
	if err != nil {
		return "", nil, fmt.Errorf("preparing repo DID: %w", err)
	}
	if err := prepared.Submit(ctx); err != nil {
		return "", nil, fmt.Errorf("submitting repo DID: %w", err)
	}
	return prepared.RepoDid, prepared.SigningKeyRaw, nil
}
