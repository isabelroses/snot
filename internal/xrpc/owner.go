package xrpc

import (
	"net/http"

	"tangled.org/core/api/tangled"
)

func (x *Xrpc) Owner(w http.ResponseWriter, r *http.Request) {
	x.writeJson(w, tangled.Owner_Output{Owner: x.Cfg.OwnerDid})
}
