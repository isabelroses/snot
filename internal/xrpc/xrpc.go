package xrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"
	"tangled.org/core/xrpc/serviceauth"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
)

const maxResponseKB = 5120

type Xrpc struct {
	Cfg         *config.Config
	Store       forgejo.Store
	Resolve     *resolve.Resolver
	Rkeys       *state.RkeyMap
	ACL         *state.ACL
	Logger      *slog.Logger
	ServiceAuth *serviceauth.ServiceAuth // nil in tests: auth middleware skipped
}

func (x *Xrpc) Router() http.Handler {
	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		if x.ServiceAuth != nil {
			r.Use(x.ServiceAuth.VerifyServiceAuth)
		}
		r.Post("/"+tangled.RepoCreateNSID, x.CreateRepo)
		r.Post("/"+tangled.KnotAddMemberNSID, x.AddMember)
		r.Post("/"+tangled.KnotRemoveMemberNSID, x.RemoveMember)
		r.Post("/"+tangled.RepoAddCollaboratorNSID, x.AddCollaborator)
		r.Post("/"+tangled.RepoRemoveCollaboratorNSID, x.RemoveCollaborator)
	})

	// writes the shim does not support
	for _, nsid := range []string{
		tangled.RepoSetDefaultBranchNSID,
		tangled.RepoDeleteBranchNSID,
		tangled.RepoDeleteNSID,
		tangled.RepoForkStatusNSID,
		tangled.RepoForkSyncNSID,
		tangled.RepoHiddenRefNSID,
		tangled.RepoMergeNSID,
		tangled.RepoMergeCheckNSID,
	} {
		r.Post("/"+nsid, x.notImplemented)
	}

	// repo query endpoints (no auth required)
	r.Get("/"+tangled.RepoTreeNSID, x.RepoTree)
	r.Get("/"+tangled.RepoLogNSID, x.RepoLog)
	r.Get("/"+tangled.RepoBranchesNSID, x.RepoBranches)
	r.Get("/"+tangled.RepoTagsNSID, x.RepoTags)
	r.Get("/"+tangled.RepoTagNSID, x.RepoTag)
	r.Get("/"+tangled.RepoBlobNSID, x.RepoBlob)
	r.Get("/"+tangled.RepoDiffNSID, x.RepoDiff)
	r.Get("/"+tangled.RepoCompareNSID, x.RepoCompare)
	r.Get("/"+tangled.RepoGetDefaultBranchNSID, x.RepoGetDefaultBranch)
	r.Get("/"+tangled.RepoDescribeRepoNSID, x.RepoDescribeRepo)
	r.Get("/"+tangled.RepoBranchNSID, x.RepoBranch)
	r.Get("/"+tangled.RepoArchiveNSID, x.RepoArchive)
	r.Get("/"+tangled.RepoLanguagesNSID, x.RepoLanguages)

	// knot/service query endpoints (no auth required)
	r.Get("/"+tangled.KnotListKeysNSID, x.ListKeys)
	r.Get("/"+tangled.KnotListMembersNSID, x.ListMembers)
	r.Get("/"+tangled.RepoListCollaboratorsNSID, x.ListCollaborators)
	r.Get("/"+tangled.KnotVersionNSID, x.Version)
	r.Get("/"+tangled.OwnerNSID, x.Owner)

	return r
}

func (x *Xrpc) notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, xrpcerr.NewXrpcError(
		xrpcerr.WithTag("MethodNotImplemented"),
		xrpcerr.WithMessage("this knot is a read-only Forgejo shim; use Forgejo directly"),
	), http.StatusNotImplemented)
}

// parseRepoParam resolves the `repo` query parameter (ownerDid/name,
// ownerDid/rkey, or bare repo DID) to an on-disk repo path. Signature matches
// upstream so handler files copy verbatim.
func (x *Xrpc) parseRepoParam(repo string) (string, error) {
	_, path, err := x.Resolve.RepoFromParam(context.Background(), repo)
	if errors.Is(err, forgejo.ErrNotFound) {
		return "", xrpcerr.RepoNotFoundError
	}
	if err != nil {
		return "", xrpcerr.GenericError(err)
	}
	return path, nil
}

func writeError(w http.ResponseWriter, e xrpcerr.XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}

type limitWriter struct {
	buf     bytes.Buffer
	limit   int
	written int
}

var errResponseTooLarge = errors.New("response too large")

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.written+len(p) > lw.limit {
		return 0, errResponseTooLarge
	}
	n, err := lw.buf.Write(p)
	lw.written += n
	return n, err
}

func (x *Xrpc) writeJson(w http.ResponseWriter, response any) {
	lw := &limitWriter{limit: maxResponseKB * 1024}
	if err := json.NewEncoder(lw).Encode(response); err != nil {
		if errors.Is(err, errResponseTooLarge) {
			writeError(w, xrpcerr.RequestTooLargeError, http.StatusRequestEntityTooLarge)
		} else {
			writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(lw.buf.Bytes())
}
