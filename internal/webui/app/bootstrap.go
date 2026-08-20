package app

import (
	"net/http"
	"os"
)

// handleLeadBootstrapLoom streams serve's OWN running binary so a booting lead
// sandbox installs exactly the binary this serve process is running (zero
// drift, same architecture on the POC box). It is intentionally UNAUTHENTICATED
// -- it serves the public CLI binary and the box already runs open-auth -- and
// it takes no request-supplied path, so it can never be steered to serve any
// file other than serve's executable.
//
// The route is registered only when LeadBootstrapEnabled, so "disabled" is a
// mux miss that falls through to the /api/ JSON-404 catch-all. Enabled but
// unreadable/empty returns 500 -- a loud failure the fail-hard download
// surfaces at provisioning rather than booting a broken lead.
func (app *Server) handleLeadBootstrapLoom(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, "bootstrap binary unavailable", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(exe) //nolint:gosec // fixed path: serve's own executable, never request-derived
	if err != nil {
		http.Error(w, "bootstrap binary unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() || info.Size() == 0 {
		http.Error(w, "bootstrap binary unavailable", http.StatusInternalServerError)
		return
	}
	// ServeContent sets Content-Length and handles Range/conditional requests;
	// the explicit octet-stream Content-Type keeps it from sniffing the binary.
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "loom", info.ModTime(), f)
}
