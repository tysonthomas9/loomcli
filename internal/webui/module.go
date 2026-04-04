package webui

import "net/http"

// Module is the interface for a group of related HTTP routes that can register
// themselves on a [*http.ServeMux].
//
// The webui server uses two muxes: an app-level mux for top-level routes and
// a workspace-scoped wsMux for routes under /api/workspaces/{ws}/. The caller
// decides which mux to pass — Module implementations are mux-agnostic.
//
// Register must not be called concurrently. It is called sequentially during
// server startup. If two modules register the same pattern on the same mux,
// [http.ServeMux] panics — this is intentional (fail fast on programming errors).
//
// The caller must pass a non-nil mux; passing nil will panic on the first
// HandleFunc call.
type Module interface {
	Register(mux *http.ServeMux)
}
