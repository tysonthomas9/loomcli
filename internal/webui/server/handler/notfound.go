package handler

import (
	"io"
	"net/http"
)

// JSONNotFound answers with the standard {"error":"not found"} envelope.
// It is the fallback for muxes that would otherwise emit Go's built-in
// text/plain "404 page not found" body, which no JSON client can decode.
func JSONNotFound(w http.ResponseWriter, r *http.Request) {
	DrainBody(r)
	RespondError(w, http.StatusNotFound, "not found")
}

// DrainBody consumes and discards up to MaxRequestBody of r.Body. A handler
// that returns without reading the body leaves the server to drain it, and Go
// gives up past a small threshold and closes the connection — so a client
// streaming a large body to a route with no handler sees a broken pipe instead
// of the response. Draining a bounded prefix here gives it a clean answer.
//
// The limit is applied with io.LimitReader, not http.MaxBytesReader: the latter
// puts the ResponseWriter into an error state on exactly the oversized-body
// case this exists to answer cleanly.
func DrainBody(r *http.Request) {
	if r == nil || r.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, MaxRequestBody))
}

// JSONFallbackMux wraps a nested *http.ServeMux so that requests it does not
// match are answered with the JSON error envelope instead of Go's built-in
// text/plain 404 / 405 bodies. Only the unmatched path is intercepted: a
// request that matches a pattern is served with the original ResponseWriter,
// untouched, so streaming and SSE routes keep their flushing behavior.
//
// The status code and any headers the built-in handler sets (notably Allow on a
// 405) are preserved, so wrong-method requests keep returning 405 — which a
// bare mux.Handle("/", ...) fallback would silently turn into a 404, because
// Go only synthesizes 405 when no node matches at all.
func JSONFallbackMux(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			// Matched (including Go's trailing-slash redirect, which reports
			// the redirect target as its pattern). Serve normally.
			mux.ServeHTTP(w, r)
			return
		}
		// No pattern matched: Go's built-in handler will answer 404 or 405 in
		// text/plain. Run it against a body-suppressing writer to learn which
		// and to collect the headers it sets, then emit the JSON envelope with
		// the same status.
		sw := &suppressBodyWriter{ResponseWriter: w, status: http.StatusNotFound}
		h.ServeHTTP(sw, r)
		DrainBody(r)
		msg := "not found"
		if sw.status == http.StatusMethodNotAllowed {
			msg = "method not allowed"
		}
		RespondError(w, sw.status, msg)
	})
}

// suppressBodyWriter records the status code of the wrapped handler and
// discards its body, while letting header writes through to the real
// ResponseWriter. It is only ever used on the no-match path, where the handler
// is one of Go's two built-in text responders and never streams.
type suppressBodyWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *suppressBodyWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = code, true
	}
}

func (s *suppressBodyWriter) Write(b []byte) (int, error) { return len(b), nil }
