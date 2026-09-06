// Package route defines the mux abstraction that webui route modules register
// on. It is deliberately a leaf package — it imports only the standard library
// — so that every handler package can depend on it without an import cycle
// through handlermux.
package route

import (
	"net/http"
	"sort"
)

// Router is the subset of [*http.ServeMux] that route modules use. Taking this
// interface instead of the concrete mux lets a caller substitute a [Recorder]
// and observe which patterns a module registers.
type Router interface {
	Handle(pattern string, h http.Handler)
	HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request))
}

// Recorder is a [Router] that serves requests exactly like a [*http.ServeMux]
// while remembering every pattern registered on it.
//
// It embeds the real mux, so ServeHTTP, Handler and the rest of the ServeMux
// surface keep working unchanged; only Handle and HandleFunc are intercepted.
//
// A Recorder is not safe for concurrent registration. Routes are registered
// during server construction, before any request is served.
type Recorder struct {
	*http.ServeMux
	patterns []string
}

// NewRecorder returns a Recorder backed by a fresh [*http.ServeMux].
func NewRecorder() *Recorder {
	return &Recorder{ServeMux: http.NewServeMux()}
}

// Handle records the pattern and registers h on the underlying mux.
func (r *Recorder) Handle(pattern string, h http.Handler) {
	r.patterns = append(r.patterns, pattern)
	r.ServeMux.Handle(pattern, h)
}

// HandleFunc records the pattern and registers h on the underlying mux.
func (r *Recorder) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	r.patterns = append(r.patterns, pattern)
	r.ServeMux.HandleFunc(pattern, h)
}

// Patterns returns the recorded patterns, deduplicated and sorted. The result
// is a fresh slice; mutating it does not affect the Recorder.
//
// Deduplication matters: the same handler is legitimately mounted under two
// patterns in places (see SetupWorkerAPIRoutes), and a caller merging patterns
// from several muxes wants each route once.
func (r *Recorder) Patterns() []string {
	if len(r.patterns) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(r.patterns))
	out := make([]string, 0, len(r.patterns))
	for _, p := range r.patterns {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
