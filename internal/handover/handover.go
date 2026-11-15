// Package handover takes repo DIDs back from tangled's foster knot.
//
// For the 2026-10-28 knot2 migration the foster knot shadow-hosts every
// legacy knot's repo DIDs, and tangled's index resolves a shadowed DID to the
// foster rather than to its knot until the knot acknowledges the handover.
// Until then nothing snot publishes for that DID is read. This package does
// what knot2 does at boot: adopt the shadow's records, serve them at the same
// CIDs, and send the foster a signed ack so it retires the shadow.
package handover

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/atdata"

	"github.com/isabelroses/snot/internal/atrepo"
	"github.com/isabelroses/snot/internal/state"
)

const (
	// ackContext is knot2's ACK_CONTEXT, bound into the signed ack.
	ackContext = "org.tangled.temp.handover.ack.v1"
	ackNSID    = "org.tangled.temp.server.ackHandover"

	listLimit = 100
	// maxPages bounds how much of one collection is adopted; a shadow larger
	// than this is not something a shim should silently take on.
	maxPages = 100
	maxBody  = 8 << 20
)

// derived are the collections snot publishes from Forgejo and its own ACL.
// The foster's copies of these are placeholders and are not adopted.
var derived = map[string]bool{
	"org.tangled.repo.manifest":      true,
	"org.tangled.git.refs":           true,
	"org.tangled.graph.collaborator": true,
}

// Records converts adopted foster records to publishable ones.
func Records(recs []state.FosterRecord) ([]atrepo.Record, error) {
	out := make([]atrepo.Record, 0, len(recs))
	for _, r := range recs {
		value, err := atdata.UnmarshalJSON(r.Value)
		if err != nil {
			return nil, fmt.Errorf("handover: decoding %s/%s: %w", r.Collection, r.Rkey, err)
		}
		out = append(out, atrepo.Record{Collection: r.Collection, Rkey: r.Rkey, Value: value})
	}
	return out, nil
}

// Client hands repo DIDs over from one foster knot.
type Client struct {
	// FosterURL is the foster knot's base URL.
	FosterURL string
	KnotKey   atcrypto.PrivateKey
	Store     *state.Handovers
	// Publish republishes a repo DID so the adopted records are served
	// before the foster spot-checks them.
	Publish func(ctx context.Context, repoDid string) error
	HTTP    *http.Client
	Logger  *slog.Logger
	// Now is the ack timestamp source; time.Now when nil.
	Now func() time.Time
}

// Run hands over every repo DID that is not settled yet. Failures are logged
// and retried on the next run; one repo cannot hold up the rest.
func (c *Client) Run(ctx context.Context, repoDids []string) {
	for _, did := range repoDids {
		if ctx.Err() != nil {
			return
		}
		settled, err := c.Store.Settled(did)
		if err != nil {
			c.Logger.Error("handover: reading state", "repo", did, "err", err)
			continue
		}
		if settled {
			continue
		}
		if err := c.handover(ctx, did); err != nil {
			c.Logger.Warn("handover failed; will retry", "repo", did, "err", err)
		}
	}
}

func (c *Client) handover(ctx context.Context, repoDid string) error {
	collections, shadowed, err := c.collections(ctx, repoDid)
	if err != nil {
		return err
	}
	if !shadowed {
		c.Logger.Info("the foster knot does not shadow this repo; nothing to hand over", "repo", repoDid)
		return c.Store.MarkSettled(repoDid, c.now().Unix())
	}

	var adopted []state.FosterRecord
	for _, collection := range collections {
		if derived[collection] {
			continue
		}
		recs, err := c.records(ctx, repoDid, collection)
		if err != nil {
			return err
		}
		adopted = append(adopted, recs...)
	}
	if err := verifyCids(adopted); err != nil {
		return err
	}
	if err := c.Store.PutRecords(repoDid, adopted); err != nil {
		return err
	}
	if err := c.Publish(ctx, repoDid); err != nil {
		return fmt.Errorf("handover: publishing adopted records: %w", err)
	}

	ackedAt := c.now().Unix()
	if err := c.ack(ctx, repoDid, len(adopted), ackedAt); err != nil {
		return err
	}
	c.Logger.Info("handed over from the foster knot", "repo", repoDid, "records", len(adopted))
	return c.Store.MarkSettled(repoDid, ackedAt)
}

// verifyCids checks snot would serve every adopted record at the CID the
// foster recorded, which is what the foster spot-checks before retiring.
func verifyCids(recs []state.FosterRecord) error {
	converted, err := Records(recs)
	if err != nil {
		return err
	}
	for i, rec := range converted {
		got, err := atrepo.RecordCid(rec)
		if err != nil {
			return fmt.Errorf("handover: encoding %s: %w", rec.Path(), err)
		}
		if got != recs[i].Cid {
			return fmt.Errorf("handover: %s would be served at %s, the foster has %s", rec.Path(), got, recs[i].Cid)
		}
	}
	return nil
}

// collections lists the shadow's collections. shadowed is false only when
// the foster definitely does not host the repo.
func (c *Client) collections(ctx context.Context, repoDid string) (collections []string, shadowed bool, err error) {
	var out struct {
		Collections []string `json:"collections"`
	}
	status, name, err := c.get(ctx, "com.atproto.repo.describeRepo", url.Values{"repo": {repoDid}}, &out)
	if err != nil {
		return nil, false, err
	}
	if name == "RepoNotFound" {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("handover: foster describeRepo returned %d %s", status, name)
	}
	return out.Collections, true, nil
}

func (c *Client) records(ctx context.Context, repoDid, collection string) ([]state.FosterRecord, error) {
	var recs []state.FosterRecord
	cursor := ""
	for range maxPages {
		q := url.Values{"repo": {repoDid}, "collection": {collection}, "limit": {fmt.Sprint(listLimit)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Cursor  string `json:"cursor"`
			Records []struct {
				URI   string          `json:"uri"`
				Cid   string          `json:"cid"`
				Value json.RawMessage `json:"value"`
			} `json:"records"`
		}
		status, _, err := c.get(ctx, "com.atproto.repo.listRecords", q, &page)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("handover: foster listRecords %s returned %d", collection, status)
		}
		for _, r := range page.Records {
			rkey := r.URI[strings.LastIndex(r.URI, "/")+1:]
			recs = append(recs, state.FosterRecord{Collection: collection, Rkey: rkey, Cid: r.Cid, Value: r.Value})
		}
		if page.Cursor == "" || len(page.Records) == 0 {
			return recs, nil
		}
		cursor = page.Cursor
	}
	return nil, fmt.Errorf("handover: %s has more than %d pages of %s", repoDid, maxPages, collection)
}

// get decodes a 200 response into out; otherwise it reports the status and
// the XRPC error name.
func (c *Client) get(ctx context.Context, nsid string, q url.Values, out any) (status int, errName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(nsid)+"?"+q.Encode(), nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("handover: foster %s: %w", nsid, err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxBody)
	if resp.StatusCode != http.StatusOK {
		var xerr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(body).Decode(&xerr)
		return resp.StatusCode, xerr.Error, nil
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return 0, "", fmt.Errorf("handover: decoding foster %s: %w", nsid, err)
	}
	return resp.StatusCode, "", nil
}

// ack sends knot2's signed handover ack. The signature covers the canonical
// DAG-CBOR of the ack fields, made with the knot's did:web key.
func (c *Client) ack(ctx context.Context, repoDid string, records int, ackedAt int64) error {
	signed, err := atdata.MarshalCBOR(map[string]any{
		"context": ackContext,
		"repoDid": repoDid,
		"records": int64(records),
		"ackedAt": ackedAt,
	})
	if err != nil {
		return err
	}
	sig, err := c.KnotKey.HashAndSign(signed)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"context":   ackContext,
		"repoDid":   repoDid,
		"records":   records,
		"ackedAt":   ackedAt,
		"signature": base64.RawURLEncoding.EncodeToString(sig),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(ackNSID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("handover: sending ack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("handover: the foster knot rejected the ack with %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

func (c *Client) endpoint(nsid string) string {
	return strings.TrimSuffix(c.FosterURL, "/") + "/xrpc/" + nsid
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
