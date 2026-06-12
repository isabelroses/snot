package xrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
	"tangled.org/core/xrpc/serviceauth"

	"github.com/isabelroses/snot/internal/forgejo"
)

// CreateRepo adopts an existing public Forgejo repo so the appview can record
// it; it never creates repositories.
func (x *Xrpc) CreateRepo(w http.ResponseWriter, r *http.Request) {
	l := x.Logger.With("handler", "CreateRepo")
	fail := func(status int, e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, status)
	}

	actorDid, ok := r.Context().Value(serviceauth.ActorDid).(syntax.DID)
	if !ok {
		fail(http.StatusBadRequest, xrpcerr.MissingActorDidError)
		return
	}
	// Any mapped DID may adopt — within its own Forgejo user's repos.
	user, mapped := x.Resolve.UserFor(actorDid.String())
	if !mapped {
		fail(http.StatusForbidden, xrpcerr.AccessControlError(actorDid.String()))
		return
	}

	var data tangled.RepoCreate_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		fail(http.StatusBadRequest, xrpcerr.GenericError(err))
		return
	}
	if data.Name == "" || data.Rkey == "" {
		fail(http.StatusBadRequest, xrpcerr.GenericError(fmt.Errorf("name and rkey are required")))
		return
	}

	repo, err := x.Store.ResolveRepo(r.Context(), user, data.Name)
	if errors.Is(err, forgejo.ErrNotFound) {
		fail(http.StatusBadRequest, xrpcerr.GenericError(fmt.Errorf(
			"repo %q does not exist on the Forgejo instance; this shim only adopts existing public repos", data.Name)))
		return
	}
	if err != nil {
		fail(http.StatusInternalServerError, xrpcerr.GenericError(err))
		return
	}

	if err := x.Rkeys.Put(data.Rkey, repo.Name); err != nil {
		fail(http.StatusInternalServerError, xrpcerr.GenericError(err))
		return
	}

	repoDid, err := x.Resolve.EnsureRepoDid(r.Context(), repo)
	if err != nil {
		fail(http.StatusInternalServerError, xrpcerr.GenericError(err))
		return
	}
	l.Info("adopted repo", "repo", repo.Name, "rkey", data.Rkey, "repoDid", repoDid)
	x.writeJson(w, tangled.RepoCreate_Output{RepoDid: &repoDid})
}
