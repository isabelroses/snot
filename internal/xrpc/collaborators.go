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
			// no consent model here: a grant takes effect when it's added
			EffectiveSince: c.Created,
		})
	}
	total := int64(len(items))
	x.writeJson(w, tangled.RepoListCollaborators_Output{Items: items, Total: &total})
}

// collabTarget parses a collaborator mutation and authorizes it: like
// upstream, only the repo's owner (or the knot owner) manages its collaborators.
func (x *Xrpc) collabTarget(r *http.Request, repo, subject string) (actorDid, repoDid, subjectDid syntax.DID, status int, e *xrpcerr.XrpcError) {
	fail := func(status int, err xrpcerr.XrpcError) (syntax.DID, syntax.DID, syntax.DID, int, *xrpcerr.XrpcError) {
		return "", "", "", status, &err
	}
	actorDid, autherr := actor(r)
	if autherr != nil {
		return fail(http.StatusForbidden, *autherr)
	}
	repoDid, err := syntax.ParseDID(repo)
	if err != nil {
		return fail(http.StatusBadRequest, xrpcerr.InvalidRepoError(repo))
	}
	info, ok := x.Resolve.Dids.Get(repoDid.String())
	if !ok {
		return fail(http.StatusNotFound, xrpcerr.RepoNotFoundError)
	}
	ownerDid, mapped := x.Resolve.DidFor(info.User)
	if actorDid.String() != x.Cfg.OwnerDid && (!mapped || actorDid.String() != ownerDid) {
		return fail(http.StatusForbidden, xrpcerr.AccessControlError(actorDid.String()))
	}
	subjectDid, err = syntax.ParseDID(subject)
	if err != nil {
		return fail(http.StatusBadRequest, xrpcerr.GenericError(err))
	}
	return actorDid, repoDid, subjectDid, 0, nil
}

func (x *Xrpc) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	var data tangled.RepoAddCollaborator_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	actorDid, repoDid, subject, status, xerr := x.collabTarget(r, data.Repo, data.Subject)
	if xerr != nil {
		writeError(w, *xerr, status)
		return
	}
	if err := x.ACL.AddCollaborator(repoDid.String(), subject.String(), actorDid.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (x *Xrpc) RemoveCollaborator(w http.ResponseWriter, r *http.Request) {
	var data tangled.RepoRemoveCollaborator_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	_, repoDid, subject, status, xerr := x.collabTarget(r, data.Repo, data.Subject)
	if xerr != nil {
		writeError(w, *xerr, status)
		return
	}
	if err := x.ACL.RemoveCollaborator(repoDid.String(), subject.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
