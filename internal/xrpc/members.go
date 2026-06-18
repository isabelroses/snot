package xrpc

import (
	"encoding/json"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
	"tangled.org/core/xrpc/serviceauth"
)

// ListMembers returns the knot's members. Open endpoint; the appview reads it
// to determine knot membership and repo-create permission.
func (x *Xrpc) ListMembers(w http.ResponseWriter, r *http.Request) {
	members, err := x.ACL.Members()
	if err != nil {
		x.Logger.Error("failed to list members", "error", err)
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InternalServerError"),
			xrpcerr.WithMessage("failed to list members"),
		), http.StatusInternalServerError)
		return
	}
	items := make([]*tangled.KnotListMembers_ListItem, 0, len(members))
	for _, m := range members {
		items = append(items, &tangled.KnotListMembers_ListItem{
			Subject:   m.Subject,
			AddedBy:   m.AddedBy,
			CreatedAt: m.Created,
		})
	}
	x.writeJson(w, tangled.KnotListMembers_Output{Items: items})
}

// requireMember authorizes a mutation: the service-auth actor must be a
// current knot member. Returns the actor DID or an error to write.
func (x *Xrpc) requireMember(r *http.Request) (syntax.DID, *xrpcerr.XrpcError) {
	actorDid, ok := r.Context().Value(serviceauth.ActorDid).(syntax.DID)
	if !ok {
		e := xrpcerr.MissingActorDidError
		return "", &e
	}
	member, err := x.ACL.IsMember(actorDid.String())
	if err != nil {
		e := xrpcerr.GenericError(err)
		return "", &e
	}
	if !member {
		e := xrpcerr.AccessControlError(actorDid.String())
		return "", &e
	}
	return actorDid, nil
}

func (x *Xrpc) AddMember(w http.ResponseWriter, r *http.Request) {
	actor, autherr := x.requireMember(r)
	if autherr != nil {
		writeError(w, *autherr, http.StatusForbidden)
		return
	}
	var data tangled.KnotAddMember_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	subject, err := syntax.ParseDID(data.Subject)
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	if err := x.ACL.AddMember(subject.String(), actor.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (x *Xrpc) RemoveMember(w http.ResponseWriter, r *http.Request) {
	if _, autherr := x.requireMember(r); autherr != nil {
		writeError(w, *autherr, http.StatusForbidden)
		return
	}
	var data tangled.KnotRemoveMember_Input
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	subject, err := syntax.ParseDID(data.Subject)
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusBadRequest)
		return
	}
	// Config-seeded members (SNOT_USER_MAP) are permanent.
	if _, configManaged := x.Cfg.UserMap[subject.String()]; configManaged {
		writeError(w, xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("member is config-managed (SNOT_USER_MAP) and cannot be removed"),
		), http.StatusBadRequest)
		return
	}
	if err := x.ACL.RemoveMember(subject.String()); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
