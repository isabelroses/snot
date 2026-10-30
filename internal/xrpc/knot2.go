package xrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	xrpcerr "tangled.org/core/xrpc/errors"

	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/publish"
)

// NSIDs of the knot2-era surface; tangled.org/core can no longer be imported
// at a version that generates these.
const (
	DescribeKnotNSID = "org.tangled.knot.describeKnot"
	CreateRepoNSID   = "org.tangled.repo.createRepo"
	DeleteRepoNSID   = "org.tangled.repo.deleteRepo"
	CreateRecordNSID = "com.atproto.repo.createRecord"
	DeleteRecordNSID = "com.atproto.repo.deleteRecord"
	PutRecordNSID    = "com.atproto.repo.putRecord"

	GitSetDefaultBranchNSID = "org.tangled.git.setDefaultBranch"
	GitDeleteBranchNSID     = "org.tangled.git.deleteBranch"
	MintTokenNSID           = "org.tangled.repo.mintToken"
	ReserveKeyNSID          = "org.tangled.repo.reserveKey"
)

// wireVersion is the knot protocol version knot2 reports in describeKnot.
const wireVersion = "v1.2.0"

// describeKnotCapabilities deliberately omits knot-social-writes: without it
// the appview hosts issues and pulls on a foster knot, which a Forgejo shim
// has no business replacing. repo-did-input is omitted because the knot must
// hold each repo DID's signing key to publish its refs.
var describeKnotCapabilities = []string{"tangled1.0", "knot-acl", "atproto-firehose"}

// Publisher keeps hosted atproto records in step with snot's state.
type Publisher interface {
	SyncRepo(ctx context.Context, repoDid string) error
	SyncKnot(ctx context.Context) error
}

// republish runs after a successful state change. Failures are logged, not
// returned: the change itself stands, and the periodic resync converges.
func (x *Xrpc) republishRepo(ctx context.Context, repoDid string) {
	if x.Publish == nil {
		return
	}
	if err := x.Publish.SyncRepo(ctx, repoDid); err != nil {
		x.Logger.Error("publish repo", "did", repoDid, "err", err)
	}
}

func (x *Xrpc) republishKnot(ctx context.Context) {
	if x.Publish == nil {
		return
	}
	if err := x.Publish.SyncKnot(ctx); err != nil {
		x.Logger.Error("publish knot", "err", err)
	}
}

func (x *Xrpc) knotDid() string { return "did:web:" + x.Cfg.Hostname }

func (x *Xrpc) DescribeKnot(w http.ResponseWriter, r *http.Request) {
	x.writeJson(w, map[string]any{
		"did":          x.knotDid(),
		"version":      wireVersion,
		"capabilities": describeKnotCapabilities,
	})
}

type createRepoInput struct {
	Name        string          `json:"name"`
	Did         string          `json:"did,omitempty"`
	Description string          `json:"description,omitempty"`
	Source      json.RawMessage `json:"source,omitempty"`
}

// CreateRepoV2 is org.tangled.repo.createRepo: like the legacy create, it
// adopts an existing public Forgejo repo of the actor's mapped user.
func (x *Xrpc) CreateRepoV2(w http.ResponseWriter, r *http.Request) {
	actorDid, e := actor(r)
	if e != nil {
		writeError(w, *e, http.StatusForbidden)
		return
	}
	var in createRepoInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, invalid(err.Error()), http.StatusBadRequest)
		return
	}
	if in.Name == "" {
		writeError(w, invalid("name is required"), http.StatusBadRequest)
		return
	}
	if in.Did != "" {
		writeError(w, invalid("this knot mints repo DIDs itself; leave did out"), http.StatusBadRequest)
		return
	}
	if len(in.Source) > 0 && string(in.Source) != "null" {
		writeError(w, invalid("this shim only adopts existing Forgejo repos; forks and imports are unsupported"), http.StatusBadRequest)
		return
	}

	repo, status, xerr := x.adoptable(r.Context(), actorDid, in.Name)
	if xerr != nil {
		writeError(w, *xerr, status)
		return
	}
	repoDid, err := x.Resolve.EnsureRepoDid(r.Context(), repo)
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	x.republishRepo(r.Context(), repoDid)

	out := map[string]any{"did": repoDid}
	if info, ok := x.Resolve.Dids.Get(repoDid); ok {
		if key, err := atcrypto.ParsePrivateBytesK256(info.Key); err == nil {
			if pub, err := key.PublicKey(); err == nil {
				out["key"] = pub.Multibase()
			}
		}
	}
	x.Logger.Info("adopted repo", "repo", repo.Name, "repoDid", repoDid)
	x.writeJson(w, out)
}

// adoptable authorizes adopting name: any mapped DID may adopt its own
// Forgejo user's public repos.
func (x *Xrpc) adoptable(ctx context.Context, actorDid syntax.DID, name string) (*forgejo.Repo, int, *xrpcerr.XrpcError) {
	user, mapped := x.Resolve.UserFor(actorDid.String())
	if !mapped {
		e := xrpcerr.AccessControlError(actorDid.String())
		return nil, http.StatusForbidden, &e
	}
	repo, err := x.Store.ResolveRepo(ctx, user, name)
	if errors.Is(err, forgejo.ErrNotFound) {
		e := invalid(fmt.Sprintf("repo %q does not exist on the Forgejo instance; this shim only adopts existing public repos", name))
		return nil, http.StatusBadRequest, &e
	}
	if err != nil {
		e := xrpcerr.GenericError(err)
		return nil, http.StatusInternalServerError, &e
	}
	return repo, 0, nil
}

type deleteRepoInput struct {
	Repo  string `json:"repo"`
	Force bool   `json:"force,omitempty"`
}

// DeleteRepo unpublishes a repo: its DID is marked deleted (the account goes
// dark on the firehose) and its rkey binding is dropped. Forgejo is untouched.
func (x *Xrpc) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	actorDid, e := actor(r)
	if e != nil {
		writeError(w, *e, http.StatusForbidden)
		return
	}
	var in deleteRepoInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, invalid(err.Error()), http.StatusBadRequest)
		return
	}
	info, ok := x.Resolve.Dids.Get(in.Repo)
	if !ok || info.Deleted {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}
	ownerDid, mapped := x.Resolve.DidFor(info.User)
	if actorDid.String() != x.Cfg.OwnerDid && (!mapped || actorDid.String() != ownerDid) {
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusForbidden)
		return
	}
	if err := x.Resolve.Dids.MarkDeleted(in.Repo); err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}
	if rkey, ok := x.Rkeys.RkeyByRepo(info.Repo); ok {
		if err := x.Rkeys.Delete(rkey); err != nil {
			x.Logger.Error("deleteRepo: dropping rkey", "rkey", rkey, "err", err)
		}
	}
	x.republishRepo(r.Context(), in.Repo)
	x.Logger.Info("unpublished repo", "repoDid", in.Repo, "by", actorDid)
	x.writeJson(w, map[string]any{})
}

type recordInput struct {
	Repo       string          `json:"repo"`
	Collection string          `json:"collection"`
	Rkey       string          `json:"rkey"`
	Record     json.RawMessage `json:"record,omitempty"`
	SwapCommit *string         `json:"swapCommit,omitempty"`
}

// CreateRecord accepts knot2's grant writes: org.tangled.knot.member on the
// knot's own repo (knot owner only) and org.tangled.graph.collaborator on a
// repo DID (repo owner or knot owner). The rkey is the subject DID. Records
// land in the ACL tables, which the publisher mirrors back out.
func (x *Xrpc) CreateRecord(w http.ResponseWriter, r *http.Request) {
	x.grant(w, r, true)
}

func (x *Xrpc) DeleteRecord(w http.ResponseWriter, r *http.Request) {
	x.grant(w, r, false)
}

func (x *Xrpc) PutRecord(w http.ResponseWriter, r *http.Request) {
	writeError(w, invalid("a grant is never edited; delete and recreate"), http.StatusBadRequest)
}

func (x *Xrpc) grant(w http.ResponseWriter, r *http.Request, add bool) {
	var in recordInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, invalid(err.Error()), http.StatusBadRequest)
		return
	}
	if in.SwapCommit != nil {
		writeError(w, invalid("swapCommit is unsupported for grants"), http.StatusBadRequest)
		return
	}
	if in.Rkey == "" {
		writeError(w, invalid("a grant's record key is the subject's DID; send that DID as the rkey"), http.StatusBadRequest)
		return
	}
	if add {
		var body struct {
			Type string `json:"$type"`
		}
		if err := json.Unmarshal(in.Record, &body); err != nil {
			writeError(w, invalid("record must be an object"), http.StatusBadRequest)
			return
		}
		if body.Type != "" && body.Type != in.Collection {
			writeError(w, invalid("record $type does not match collection"), http.StatusBadRequest)
			return
		}
	}

	switch in.Collection {
	case publish.MemberNSID:
		if in.Repo != x.knotDid() {
			writeError(w, invalid("members are granted on the knot's own repo, "+x.knotDid()), http.StatusBadRequest)
			return
		}
		actorDid, e := x.requireKnotOwner(r)
		if e != nil {
			writeError(w, *e, http.StatusForbidden)
			return
		}
		subject, err := syntax.ParseDID(in.Rkey)
		if err != nil {
			writeError(w, invalid("the rkey must be the member's DID"), http.StatusBadRequest)
			return
		}
		if add {
			err = x.ACL.AddMember(subject.String(), actorDid.String())
		} else {
			if _, configManaged := x.Cfg.UserMap[subject.String()]; configManaged {
				writeError(w, invalid("member is config-managed (SNOT_USER_MAP) and cannot be removed"), http.StatusBadRequest)
				return
			}
			err = x.ACL.RemoveMember(subject.String())
		}
		if err != nil {
			writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
			return
		}
		x.republishKnot(r.Context())

	case publish.CollaboratorNSID:
		actorDid, repoDid, subject, status, xerr := x.collabTarget(r, in.Repo, in.Rkey)
		if xerr != nil {
			writeError(w, *xerr, status)
			return
		}
		var err error
		if add {
			err = x.ACL.AddCollaborator(repoDid.String(), subject.String(), actorDid.String())
		} else {
			err = x.ACL.RemoveCollaborator(repoDid.String(), subject.String())
		}
		if err != nil {
			writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
			return
		}
		x.republishRepo(r.Context(), repoDid.String())

	default:
		writeError(w, invalid(fmt.Sprintf(
			"this knot accepts only %s and %s records; social records live on the foster knot",
			publish.MemberNSID, publish.CollaboratorNSID)), http.StatusBadRequest)
		return
	}
	x.writeJson(w, map[string]any{})
}

func invalid(msg string) xrpcerr.XrpcError {
	return xrpcerr.NewXrpcError(xrpcerr.WithTag("InvalidRequest"), xrpcerr.WithMessage(msg))
}
