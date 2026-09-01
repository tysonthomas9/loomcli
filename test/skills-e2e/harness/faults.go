package harness

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ResponseDrop is a real HTTP proxy fault that closes the client connection
// only after Fleet has returned a successful tree-publication response.
type ResponseDrop struct {
	t         *testing.T
	activated atomic.Bool
}

type CorruptDownload struct {
	t         *testing.T
	env       *Environment
	activated atomic.Bool
	mu        sync.Mutex
	target    *fileDownloadGrant
}

type SkillCASTrace struct {
	t                *testing.T
	mu               sync.Mutex
	requests         int
	ifMatch          string
	description      string
	fileTreeRevision string
	malformedRequest error
}

type skillCASRequest struct {
	Description      *string `json:"description"`
	FileTreeRevision *string `json:"file_tree_revision"`
}

type fileDownloadGrant struct {
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt time.Time           `json:"expires_at"`
}

// TraceNextSkillCAS forwards one public Loom command to the real Fleet process
// while recording the atomic Skill description/tree-pointer PATCH.
func (e *Environment) TraceNextSkillCAS() *SkillCASTrace {
	e.t.Helper()
	upstream, err := url.Parse(requireEnv(e.t, "LOOM_FLEET_DB_URL"))
	if err != nil {
		e.t.Fatalf("parse LOOM_FLEET_DB_URL: %v", err)
	}
	trace := &SkillCASTrace{t: e.t}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPatch {
			trace.record(request)
		}
		response, err := forwardRequest(request, upstream)
		if err != nil {
			http.Error(w, "Fleet proxy transport failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	e.t.Cleanup(proxy.Close)
	if e.nextEnv == nil {
		e.nextEnv = make(map[string]string)
	}
	e.nextEnv["LOOM_FLEET_DB_URL"] = proxy.URL
	return trace
}

func (t *SkillCASTrace) record(request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err == nil {
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	var patch skillCASRequest
	if err == nil {
		err = json.Unmarshal(body, &patch)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests++
	t.ifMatch = request.Header.Get("If-Match")
	t.malformedRequest = err
	if patch.Description != nil {
		t.description = *patch.Description
	}
	if patch.FileTreeRevision != nil {
		t.fileTreeRevision = *patch.FileTreeRevision
	}
}

func (t *SkillCASTrace) RequireSingleDescriptionAndPointerCAS(expectedPrior, description, revision string) {
	t.t.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.malformedRequest != nil {
		t.t.Fatalf("decode Skill CAS request: %v", t.malformedRequest)
	}
	if t.requests != 1 {
		t.t.Fatalf("Skill CAS requests = %d, want exactly 1", t.requests)
	}
	if t.ifMatch != strconv.Quote(expectedPrior) {
		t.t.Fatalf("Skill CAS If-Match = %q, want %q", t.ifMatch, strconv.Quote(expectedPrior))
	}
	if t.description != description || t.fileTreeRevision != revision {
		t.t.Fatalf("Skill CAS description/tree = %q/%q, want %q/%q", t.description, t.fileTreeRevision, description, revision)
	}
}

// DropNextTreePublicationResponse routes the next Loom command through a real
// forwarding proxy and drops its successful POST /file-trees response.
func (e *Environment) DropNextTreePublicationResponse() *ResponseDrop {
	e.t.Helper()
	upstream, err := url.Parse(requireEnv(e.t, "LOOM_FLEET_DB_URL"))
	if err != nil {
		e.t.Fatalf("parse LOOM_FLEET_DB_URL: %v", err)
	}
	fault := &ResponseDrop{t: e.t}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		response, err := forwardRequest(request, upstream)
		if err != nil {
			http.Error(w, "Fleet proxy transport failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()

		isTreePublish := request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/file-trees")
		if isTreePublish && response.StatusCode >= 200 && response.StatusCode < 300 && fault.activated.CompareAndSwap(false, true) {
			_, _ = io.Copy(io.Discard, response.Body)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "response drop unavailable", http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}

		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	e.t.Cleanup(proxy.Close)
	if e.nextEnv == nil {
		e.nextEnv = make(map[string]string)
	}
	e.nextEnv["LOOM_FLEET_DB_URL"] = proxy.URL
	return fault
}

func (f *ResponseDrop) RequireActivated() {
	f.t.Helper()
	if !f.activated.Load() {
		f.t.Fatal("tree-publication response-drop proxy did not activate")
	}
}

// CorruptNextFileDownload rewrites one real Fleet download grant to a proxy
// that fetches the signed provider URL and truncates the successful response.
func (e *Environment) CorruptNextFileDownload(revision string) *CorruptDownload {
	e.t.Helper()
	upstream, err := url.Parse(requireEnv(e.t, "LOOM_FLEET_DB_URL"))
	if err != nil {
		e.t.Fatalf("parse LOOM_FLEET_DB_URL: %v", err)
	}
	fault := &CorruptDownload{t: e.t, env: e}
	downloadProxy := httptest.NewServer(http.HandlerFunc(fault.serveCorruptDownload))
	e.t.Cleanup(downloadProxy.Close)

	fleetProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fault.serveFleetDownloadGrant(w, request, upstream, downloadProxy.URL, revision)
	}))
	e.t.Cleanup(fleetProxy.Close)
	if e.nextEnv == nil {
		e.nextEnv = make(map[string]string)
	}
	e.nextEnv["LOOM_FLEET_DB_URL"] = fleetProxy.URL
	return fault
}

func (f *CorruptDownload) serveCorruptDownload(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	target := f.target
	f.mu.Unlock()
	if target == nil {
		http.Error(w, "download grant unavailable", http.StatusBadGateway)
		return
	}
	request, err := http.NewRequestWithContext(f.t.Context(), target.Method, target.URL, nil)
	if err != nil {
		http.Error(w, "invalid download grant", http.StatusBadGateway)
		return
	}
	request.Header = http.Header(target.Headers).Clone()
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		http.Error(w, "provider download failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		http.Error(w, "read provider download", http.StatusBadGateway)
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && len(body) > 0 && f.activated.CompareAndSwap(false, true) {
		body = body[:len(body)-1]
	}
	copyResponseHeaders(w.Header(), response.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func (f *CorruptDownload) serveFleetDownloadGrant(w http.ResponseWriter, request *http.Request, upstream *url.URL, downloadURL, revision string) {
	response, err := forwardRequest(request, upstream)
	if err != nil {
		http.Error(w, "Fleet proxy transport failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		http.Error(w, "read Fleet response", http.StatusBadGateway)
		return
	}
	selected := strings.Contains(request.URL.Path, "/file-trees/"+url.PathEscape(revision)+"/downloads/")
	if request.Method == http.MethodPost && selected && response.StatusCode == http.StatusOK {
		body, err = f.rewriteDownloadGrant(body, downloadURL)
		if err != nil {
			http.Error(w, "rewrite download grant", http.StatusBadGateway)
			return
		}
	}
	copyResponseHeaders(w.Header(), response.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, bytes.NewReader(body))
}

func (f *CorruptDownload) rewriteDownloadGrant(body []byte, downloadURL string) ([]byte, error) {
	var grant fileDownloadGrant
	if err := json.Unmarshal(body, &grant); err != nil || grant.URL == "" {
		return body, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.target != nil {
		return body, nil
	}
	original := grant
	f.target = &original
	grant.URL = downloadURL + "/download"
	grant.Headers = nil
	return json.Marshal(grant)
}

func (f *CorruptDownload) RequireActivated() {
	f.t.Helper()
	if !f.activated.Load() {
		f.mu.Lock()
		target := f.target
		f.mu.Unlock()
		f.t.Fatalf("download-corruption proxy did not activate (captured grant: %+v)\nmaterialize stderr:\n%s", target, f.env.lastStderr)
	}
}

func forwardRequest(request *http.Request, upstream *url.URL) (*http.Response, error) {
	forward := request.Clone(request.Context())
	forward.URL.Scheme = upstream.Scheme
	forward.URL.Host = upstream.Host
	forward.Host = upstream.Host
	forward.RequestURI = ""
	return http.DefaultTransport.RoundTrip(forward)
}

func copyResponseHeaders(target, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}
