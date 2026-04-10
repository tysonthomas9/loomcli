package webui

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
)

// rwRecorder wraps http.ResponseWriter to capture the HTTP status code.
// Implements http.Flusher, http.Hijacker, and http.ResponseController Unwrap.
// Not safe for concurrent use — matches the net/http handler contract.
type rwRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newRWRecorder(w http.ResponseWriter) *rwRecorder {
	return &rwRecorder{ResponseWriter: w}
}

func (r *rwRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *rwRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Status returns the captured status code, defaulting to 200 if WriteHeader
// was never called explicitly (matching net/http implicit behavior).
func (r *rwRecorder) Status() int {
	if !r.wroteHeader {
		return http.StatusOK
	}
	return r.status
}

// Flush delegates to the inner writer if it implements http.Flusher.
// Required for SSE and log-streaming handlers that assert w.(http.Flusher).
func (r *rwRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the inner writer if it implements http.Hijacker.
// Required for WebSocket upgrades which call Hijack directly.
func (r *rwRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// Unwrap returns the inner ResponseWriter for http.ResponseController compatibility.
func (r *rwRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
