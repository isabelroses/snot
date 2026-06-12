package xrpc

import (
	"errors"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"

	"github.com/isabelroses/snot/internal/forgejo"
)

func (x *Xrpc) RepoDescribeRepo(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("repoDid")
	repoDid, err := syntax.ParseDID(raw)
	if err != nil {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing or invalid repoDid parameter"),
		), http.StatusBadRequest)
		return
	}

	repo, _, err := x.Resolve.RepoFromParam(r.Context(), repoDid.String())
	if errors.Is(err, forgejo.ErrNotFound) {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	// The owner is the DID mapped to this repo's Forgejo user — not the
	// knot's admin DID, which governs the knot alone.
	ownerDid, ok := x.Resolve.DidFor(repo.OwnerName)
	if !ok {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}

	// The record rkey is learned at adopt time; fall back to the repo name,
	// which matches records that use name-as-rkey.
	rkey, ok := x.Rkeys.RkeyByRepo(repo.Name)
	if !ok {
		rkey = repo.Name
	}

	x.writeJson(w, tangled.RepoDescribeRepo_Output{
		RepoDid:  repoDid.String(),
		OwnerDid: ownerDid,
		Rkey:     rkey,
	})
}
