// Package pds serves the com.atproto read surface for the repos atrepo
// hosts, the subscribeRepos firehose, and the knot's own did:web identity.
// Relays and hydrant crawl these exactly as they would a PDS.
package pds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/go-chi/chi/v5"

	"github.com/isabelroses/snot/internal/atrepo"
)

const carType = "application/vnd.ipld.car"

type Handler struct {
	Store    *atrepo.Store
	Hostname string
	BaseURL  string // scheme://hostname, the service endpoint DID docs name
	KnotKey  atcrypto.PrivateKey
	PlcUrl   string
	HTTP     *http.Client // PLC lookups; http.DefaultClient when nil
	Logger   *slog.Logger
}

// KnotDid is the knot's own identity.
func (h *Handler) KnotDid() string { return "did:web:" + h.Hostname }

// Routes mounts under /xrpc.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/com.atproto.sync.getRepo", h.getRepo)
	r.Get("/com.atproto.sync.getLatestCommit", h.getLatestCommit)
	r.Get("/com.atproto.sync.getRecord", h.syncGetRecord)
	r.Get("/com.atproto.sync.getBlocks", h.getBlocks)
	r.Get("/com.atproto.sync.listRepos", h.listRepos)
	r.Get("/com.atproto.sync.getRepoStatus", h.getRepoStatus)
	r.Get("/com.atproto.sync.subscribeRepos", h.subscribeRepos)
	r.Get("/com.atproto.repo.getRecord", h.repoGetRecord)
	r.Get("/com.atproto.repo.listRecords", h.listRecords)
	r.Get("/com.atproto.repo.describeRepo", h.describeRepo)
	r.Get("/com.atproto.server.describeServer", h.describeServer)
	r.Get("/com.atproto.identity.resolveHandle", h.resolveHandle)
}

// WellKnown serves /.well-known/did.json and /.well-known/atproto-did.
func (h *Handler) WellKnown(r chi.Router) {
	r.Get("/.well-known/did.json", h.didJSON)
	r.Get("/.well-known/atproto-did", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, h.KnotDid())
	})
}

// KnotDidDoc is the knot's did:web document: its signing key, the knot
// service tangled resolves, and a PDS service so relays crawl it.
func (h *Handler) KnotDidDoc() (map[string]any, error) {
	pub, err := h.KnotKey.PublicKey()
	if err != nil {
		return nil, err
	}
	did := h.KnotDid()
	return map[string]any{
		"@context": []string{
			"https://www.w3.org/ns/did/v1",
			"https://w3id.org/security/multikey/v1",
			"https://w3id.org/security/suites/secp256k1-2019/v1",
		},
		"id":          did,
		"alsoKnownAs": []string{"at://" + h.Hostname},
		"verificationMethod": []map[string]any{{
			"id":                 did + "#atproto",
			"type":               "Multikey",
			"controller":         did,
			"publicKeyMultibase": pub.Multibase(),
		}},
		"service": []map[string]any{
			{"id": "#tangled_knot", "type": "TangledKnot", "serviceEndpoint": h.BaseURL},
			{"id": "#atproto_pds", "type": "AtprotoPersonalDataServer", "serviceEndpoint": h.BaseURL},
		},
	}, nil
}

func (h *Handler) didJSON(w http.ResponseWriter, _ *http.Request) {
	doc, err := h.KnotDidDoc()
	if err != nil {
		h.internal(w, err)
		return
	}
	writeJSON(w, doc)
}

// account resolves a did (or the knot's handle) to a hosted, serving repo.
// Deactivated and deleted repos answer the atproto error names.
func (h *Handler) account(w http.ResponseWriter, raw string) (atrepo.Account, bool) {
	did := h.didOf(raw)
	if did == "" {
		writeErr(w, http.StatusBadRequest, "InvalidRequest", "repo must be a DID")
		return atrepo.Account{}, false
	}
	acct, err := h.Store.Account(did)
	if errors.Is(err, atrepo.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "RepoNotFound", "this knot does not host "+did)
		return atrepo.Account{}, false
	}
	if err != nil {
		h.internal(w, err)
		return atrepo.Account{}, false
	}
	if !acct.Active {
		if acct.Status == "deleted" {
			writeErr(w, http.StatusBadRequest, "RepoNotFound", did+" was deleted")
		} else {
			writeErr(w, http.StatusBadRequest, "RepoDeactivated", did+" is deactivated")
		}
		return atrepo.Account{}, false
	}
	return acct, true
}

func (h *Handler) didOf(raw string) string {
	if strings.EqualFold(raw, h.Hostname) {
		return h.KnotDid()
	}
	if _, err := syntax.ParseDID(raw); err != nil {
		return ""
	}
	return raw
}

func (h *Handler) getRepo(w http.ResponseWriter, r *http.Request) {
	acct, ok := h.account(w, r.URL.Query().Get("did"))
	if !ok {
		return
	}
	h.writeCAR(w, func() ([]byte, error) { return h.Store.RepoCAR(acct.Did) })
}

func (h *Handler) getLatestCommit(w http.ResponseWriter, r *http.Request) {
	acct, ok := h.account(w, r.URL.Query().Get("did"))
	if !ok {
		return
	}
	writeJSON(w, map[string]any{"cid": acct.Head.String(), "rev": acct.Rev})
}

func (h *Handler) syncGetRecord(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, ok := h.account(w, q.Get("did"))
	if !ok {
		return
	}
	h.writeCAR(w, func() ([]byte, error) {
		return h.Store.RecordCAR(acct.Did, q.Get("collection"), q.Get("rkey"))
	})
}

func (h *Handler) getBlocks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, ok := h.account(w, q.Get("did"))
	if !ok {
		return
	}
	cids := q["cids"]
	if len(cids) == 0 {
		writeErr(w, http.StatusBadRequest, "InvalidRequest", "cids is required")
		return
	}
	h.writeCAR(w, func() ([]byte, error) { return h.Store.BlocksCAR(acct.Did, cids) })
}

func (h *Handler) listRepos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := intParam(q.Get("limit"), 500, 1000)
	accts, err := h.Store.Accounts(q.Get("cursor"), limit)
	if err != nil {
		h.internal(w, err)
		return
	}
	repos := make([]map[string]any, 0, len(accts))
	for _, a := range accts {
		row := map[string]any{"did": a.Did, "head": a.Head.String(), "rev": a.Rev, "active": a.Active}
		if !a.Active {
			row["status"] = a.Status
		}
		repos = append(repos, row)
	}
	out := map[string]any{"repos": repos}
	if len(accts) == limit {
		out["cursor"] = accts[len(accts)-1].Did
	}
	writeJSON(w, out)
}

func (h *Handler) getRepoStatus(w http.ResponseWriter, r *http.Request) {
	did := h.didOf(r.URL.Query().Get("did"))
	acct, err := h.Store.Account(did)
	if errors.Is(err, atrepo.ErrNotFound) || did == "" {
		writeErr(w, http.StatusBadRequest, "RepoNotFound", "this knot does not host "+did)
		return
	}
	if err != nil {
		h.internal(w, err)
		return
	}
	out := map[string]any{"did": acct.Did, "active": acct.Active, "rev": acct.Rev}
	if !acct.Active {
		out["status"] = acct.Status
	}
	writeJSON(w, out)
}

func (h *Handler) repoGetRecord(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, ok := h.account(w, q.Get("repo"))
	if !ok {
		return
	}
	rec, err := h.Store.Record(acct.Did, q.Get("collection"), q.Get("rkey"))
	if errors.Is(err, atrepo.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "RecordNotFound", "no such record")
		return
	}
	if err != nil {
		h.internal(w, err)
		return
	}
	if want := q.Get("cid"); want != "" && want != rec.Cid.String() {
		writeErr(w, http.StatusBadRequest, "RecordNotFound", "record is not at that cid")
		return
	}
	writeJSON(w, recordView(acct.Did, rec))
}

func (h *Handler) listRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, ok := h.account(w, q.Get("repo"))
	if !ok {
		return
	}
	limit := intParam(q.Get("limit"), 50, 100)
	reverse, _ := strconv.ParseBool(q.Get("reverse"))
	recs, err := h.Store.Records(acct.Did, q.Get("collection"), q.Get("cursor"), limit, reverse)
	if err != nil {
		h.internal(w, err)
		return
	}
	views := make([]map[string]any, len(recs))
	for i, rec := range recs {
		views[i] = recordView(acct.Did, rec)
	}
	out := map[string]any{"records": views}
	if len(recs) == limit {
		out["cursor"] = recs[len(recs)-1].Rkey
	}
	writeJSON(w, out)
}

func recordView(did string, rec atrepo.StoredRecord) map[string]any {
	return map[string]any{
		"uri":   fmt.Sprintf("at://%s/%s/%s", did, rec.Collection, rec.Rkey),
		"cid":   rec.Cid.String(),
		"value": rec.Value,
	}
}

func (h *Handler) describeRepo(w http.ResponseWriter, r *http.Request) {
	acct, ok := h.account(w, r.URL.Query().Get("repo"))
	if !ok {
		return
	}
	cols, err := h.Store.Collections(acct.Did)
	if err != nil {
		h.internal(w, err)
		return
	}
	handle, handleOK := "handle.invalid", false
	var doc any
	if acct.Did == h.KnotDid() {
		handle, handleOK = h.Hostname, true
		doc, err = h.KnotDidDoc()
	} else {
		doc, err = h.plcDoc(r.Context(), acct.Did)
	}
	if err != nil {
		h.Logger.Warn("describeRepo: did document unavailable", "did", acct.Did, "err", err)
		doc = map[string]any{"id": acct.Did}
	}
	writeJSON(w, map[string]any{
		"did":             acct.Did,
		"handle":          handle,
		"handleIsCorrect": handleOK,
		"didDoc":          doc,
		"collections":     cols,
	})
}

func (h *Handler) plcDoc(ctx context.Context, did string) (json.RawMessage, error) {
	if !strings.HasPrefix(did, "did:plc:") {
		return nil, fmt.Errorf("not a did:plc")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(h.PlcUrl, "/")+"/"+did, nil)
	if err != nil {
		return nil, err
	}
	client := h.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plc answered %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("plc returned invalid json")
	}
	return raw, nil
}

func (h *Handler) describeServer(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"did":                  h.KnotDid(),
		"availableUserDomains": []string{},
		"inviteCodeRequired":   true,
	})
}

func (h *Handler) resolveHandle(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.URL.Query().Get("handle"), h.Hostname) {
		writeErr(w, http.StatusBadRequest, "HandleNotFound", "this knot answers only for its own hostname")
		return
	}
	writeJSON(w, map[string]any{"did": h.KnotDid()})
}

func (h *Handler) writeCAR(w http.ResponseWriter, build func() ([]byte, error)) {
	car, err := build()
	if err != nil {
		h.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", carType)
	w.Write(car)
}

func (h *Handler) internal(w http.ResponseWriter, err error) {
	h.Logger.Error("pds: internal error", "err", err)
	writeErr(w, http.StatusInternalServerError, "InternalServerError", "internal error")
}

func intParam(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	return min(n, max)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, name, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": name, "message": msg})
}
