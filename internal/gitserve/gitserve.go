// Package gitserve serves read-only git smart-HTTP from Forgejo's bare repos.
package gitserve

import (
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/idresolver"
	"tangled.org/core/knotserver/git/service"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/resolve"
)

type Handler struct {
	Cfg      *config.Config
	Resolve  *resolve.Resolver
	Logger   *slog.Logger
	Resolver *idresolver.Resolver // nil disables handle redirects (tests)
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	r.Route("/{did}", func(r chi.Router) {
		r.Use(h.resolveDidRedirect)

		// bare repo-DID form
		r.Get("/info/refs", h.InfoRefs)
		r.Post("/git-upload-pack", h.UploadPack)
		r.Post("/git-upload-archive", h.UploadArchive)
		r.Post("/git-receive-pack", h.ReceivePack)

		r.Route("/{name}", func(r chi.Router) {
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)
			r.Post("/git-upload-archive", h.UploadArchive)
			r.Post("/git-receive-pack", h.ReceivePack)
		})
	})

	return r
}

// resolveDidRedirect 307-redirects handle-based URLs to DID-based ones,
// mirroring knotserver.
func (h *Handler) resolveDidRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		didOrHandle := chi.URLParam(r, "did")
		if strings.HasPrefix(didOrHandle, "did:") || h.Resolver == nil {
			next.ServeHTTP(w, r)
			return
		}

		trimmed := strings.TrimPrefix(didOrHandle, "@")
		id, err := h.Resolver.ResolveIdent(r.Context(), trimmed)
		if err != nil {
			h.Logger.Error("failed to resolve did/handle", "handle", trimmed, "err", err)
			http.Error(w, fmt.Sprintf("failed to resolve did/handle: %s", trimmed), http.StatusInternalServerError)
			return
		}

		suffix := strings.TrimPrefix(r.URL.Path, "/"+didOrHandle)
		newPath := "/" + id.DID.String() + suffix
		if r.URL.RawQuery != "" {
			newPath += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, newPath, http.StatusTemporaryRedirect)
	})
}

func (h *Handler) repoPath(r *http.Request) (string, *forgejo.Repo, error) {
	did := chi.URLParam(r, "did")
	name := chi.URLParam(r, "name")

	if name == "" {
		repo, path, err := h.Resolve.RepoFromParam(r.Context(), did)
		if err != nil {
			return "", nil, err
		}
		return path, repo, nil
	}
	repo, path, err := h.Resolve.Repo(r.Context(), did, name)
	if err != nil {
		return "", nil, err
	}
	return path, repo, nil
}

func (h *Handler) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, "repository not found\n")
}

func (h *Handler) InfoRefs(w http.ResponseWriter, r *http.Request) {
	repoPath, repo, err := h.repoPath(r)
	if err != nil {
		h.notFound(w)
		return
	}

	cmd := service.ServiceCommand{
		GitProtocol: r.Header.Get("Git-Protocol"),
		Dir:         repoPath,
		Stdout:      w,
	}

	switch r.URL.Query().Get("service") {
	case "git-upload-pack":
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Header().Set("Connection", "Keep-Alive")
		w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
		w.WriteHeader(http.StatusOK)
		if err := cmd.InfoRefs(); err != nil {
			h.Logger.Error("git: info/refs failed", "error", err)
		}
	case "git-receive-pack":
		h.rejectPush(w, repo)
	default:
		gitError(w, "service unsupported", http.StatusForbidden)
	}
}

func (h *Handler) UploadPack(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, "application/x-git-upload-pack-request",
		"application/x-git-upload-pack-result",
		func(c *service.ServiceCommand) error { return c.UploadPack() })
}

func (h *Handler) UploadArchive(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, "application/x-git-upload-archive-request",
		"application/x-git-upload-archive-result",
		func(c *service.ServiceCommand) error { return c.UploadArchive() })
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, wantCT, respCT string, run func(*service.ServiceCommand) error) {
	repoPath, _, err := h.repoPath(r)
	if err != nil {
		h.notFound(w)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != wantCT {
		gitError(w, fmt.Sprintf("expected Content-Type %q, got %q", wantCT, ct), http.StatusUnsupportedMediaType)
		return
	}

	var body io.ReadCloser = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			gitError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer gz.Close()
		body = gz
	}

	w.Header().Set("Content-Type", respCT)
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.WriteHeader(http.StatusOK)

	cmd := service.ServiceCommand{
		GitProtocol: r.Header.Get("Git-Protocol"),
		Dir:         repoPath,
		Stdout:      w,
		Stdin:       body,
	}
	if err := run(&cmd); err != nil {
		h.Logger.Error("git: service failed", "error", err)
	}
}

func (h *Handler) ReceivePack(w http.ResponseWriter, r *http.Request) {
	_, repo, err := h.repoPath(r)
	if err != nil {
		h.notFound(w)
		return
	}
	h.rejectPush(w, repo)
}

func (h *Handler) rejectPush(w http.ResponseWriter, repo *forgejo.Repo) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, "Pushes go through Forgejo, not this knot.")
	if h.Cfg.PushRemote != "" {
		fmt.Fprintf(w, " Try:\ngit remote set-url --push origin %s:%s/%s\n\n... and push again.",
			h.Cfg.PushRemote, repo.OwnerName, repo.Name)
	}
	fmt.Fprintf(w, "\n\n")
}

func gitError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "%s\n", msg)
}
