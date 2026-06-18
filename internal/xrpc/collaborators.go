package xrpc

import (
	"encoding/json"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
)

// ListCollaborators returns a repo's collaborators. Open endpoint;
// ?subject=<repoDid>.
func (x *Xrpc) ListCollaborators(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	repoDid, err := syntax.ParseDID(subject)
	if err != nil {
		writeError(w, xrpcerr.InvalidRepoError(subject), http.StatusBadRequest)
		return
	}
	if _, ok := x.Resolve.Dids.Get(repoDid.String()); !ok {
		writeError(w, xrpcerr.InvalidRepoError(subject), http.StatusBadRequest)
		return
	}

	collaborators, err := x.ACL.Collaborators(repoDid.String())
	if err != nil {
		x.Logger.Error("failed to list collaborators", "repoDid", repoDid, "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to list collaborators"),
		), http.StatusInternalServerError)
		return
	}
	items := make([]*tangled.RepoListCollaborators_ListItem, 0, len(collaborators))
	for _, c := range collaborators {
		items = append(items, &tangled.RepoListCollaborators_ListItem{
			Subject:   c.Subject,
			AddedBy:   c.AddedBy,
			CreatedAt: c.Created,
		})
	}
	x.writeJson(w, tangled.RepoListCollaborators_Output{Items: items})
}

func (x *Xrpc) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	actor, autherr := x.requireMember(r)
	if autherr != nil {
		writeError(w, *autherr, http.StatusForbidden)
		return
	}
	var data tangled.RepoAddCollaborator_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	repoDid, err := syntax.ParseDID(data.Repo)
	if err != nil {
		writeError(w, xrpcerr.InvalidRepoError(data.Repo), http.StatusBadRequest)
		return
	}
	if _, ok := x.Resolve.Dids.Get(repoDid.String()); !ok {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}
	subject, err := syntax.ParseDID(data.Subject)
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	if err := x.ACL.AddCollaborator(repoDid.String(), subject.String(), actor.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (x *Xrpc) RemoveCollaborator(w http.ResponseWriter, r *http.Request) {
	if _, autherr := x.requireMember(r); autherr != nil {
		writeError(w, *autherr, http.StatusForbidden)
		return
	}
	var data tangled.RepoRemoveCollaborator_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	repoDid, err := syntax.ParseDID(data.Repo)
	if err != nil {
		writeError(w, xrpcerr.InvalidRepoError(data.Repo), http.StatusBadRequest)
		return
	}
	if _, ok := x.Resolve.Dids.Get(repoDid.String()); !ok {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}
	subject, err := syntax.ParseDID(data.Subject)
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	if err := x.ACL.RemoveCollaborator(repoDid.String(), subject.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
