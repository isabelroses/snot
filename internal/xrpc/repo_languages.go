package xrpc

import (
	"errors"
	"math"
	"net/http"

	"tangled.org/core/api/tangled"
	xrpcerr "tangled.org/core/xrpc/errors"

	"github.com/isabelroses/snot/internal/forgejo"
)

func (x *Xrpc) RepoLanguages(w http.ResponseWriter, r *http.Request) {
	repo, _, err := x.Resolve.RepoFromParam(r.Context(), r.URL.Query().Get("repo"))
	if errors.Is(err, forgejo.ErrNotFound) {
		writeError(w, xrpcerr.RepoNotFoundError, http.StatusNotFound)
		return
	}
	if err != nil {
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	sizes, err := x.Store.Languages(r.Context(), repo.ID)
	if err != nil {
		x.Logger.Error("failed to read language stats", "error", err)
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	var apiLanguages []*tangled.RepoLanguages_Language
	var totalSize int64
	for _, size := range sizes {
		totalSize += size
	}
	for name, size := range sizes {
		percentage := math.Round(float64(size) / float64(totalSize) * 100)
		apiLanguages = append(apiLanguages, &tangled.RepoLanguages_Language{
			Name:       name,
			Size:       size,
			Percentage: int64(percentage),
		})
	}

	// Forgejo's language_stat is computed for the default branch only; the
	// ref is echoed back for lexicon shape but does not select a revision.
	response := tangled.RepoLanguages_Output{
		Ref:       r.URL.Query().Get("ref"),
		Languages: apiLanguages,
	}
	if totalSize > 0 {
		response.TotalSize = &totalSize
		totalFiles := int64(len(sizes))
		response.TotalFiles = &totalFiles
	}

	x.writeJson(w, response)
}
