package xrpc

import (
	"net/http"
	"slices"
	"time"

	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
)

// ListKeys returns the Forgejo SSH keys of every mapped user, attributed to
// their DID, in one page; the lexicon's cursor/limit pagination is not
// implemented since a small instance holds at most a handful of keys.
func (x *Xrpc) ListKeys(w http.ResponseWriter, r *http.Request) {
	dids := make([]string, 0, len(x.Cfg.UserMap))
	for did := range x.Cfg.UserMap {
		dids = append(dids, did)
	}
	slices.Sort(dids)

	var publicKeys []*tangled.KnotListKeys_PublicKey
	for _, did := range dids {
		keys, err := x.Store.PublicKeys(r.Context(), x.Cfg.UserMap[did])
		if err != nil {
			x.Logger.Error("failed to get public keys", "error", err)
			writeError(w, xrpcerr.NewXrpcError(
				xrpcerr.WithTag("InternalServerError"),
				xrpcerr.WithMessage("failed to retrieve public keys"),
			), http.StatusInternalServerError)
			return
		}
		for _, key := range keys {
			publicKeys = append(publicKeys, &tangled.KnotListKeys_PublicKey{
				Did:       did,
				Key:       key.Key,
				CreatedAt: key.CreatedAt.Format(time.RFC3339),
			})
		}
	}
	if publicKeys == nil {
		publicKeys = []*tangled.KnotListKeys_PublicKey{}
	}

	x.writeJson(w, tangled.KnotListKeys_Output{Keys: publicKeys})
}
