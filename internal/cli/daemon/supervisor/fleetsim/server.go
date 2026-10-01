// Package fleetsim is a deterministic simulation harness for the Loom
// supervisor at the FleetDB HTTP boundary. The real Loom FleetDB adapter
// (internal/backend/fleet) and real supervisor methods run unchanged; only the
// HTTP transport is replaced by an in-process interposer (Network) in front of
// an in-memory FleetDB model (Server). Time comes from clock.Fake.
//
// Fidelity statement. Server implements the issue-claim subset of FleetDB
// semantics as read from the pinned FleetDB source (fleet-db 40e8431d,
// internal/service/issue_service.go ClaimIssue/AssignIssue/CloseIssue/
// ReleaseIssueLock/releaseStaleClaim, internal/service/claim_reaper.go,
// internal/api/claim.go and issues.go). Request ordering, X-Actor values,
// request bodies, HTTP statuses and final issue state were calibrated against
// the real stale-worker trace verified in task e034de04 (see calibration.go).
// Response BODIES were not captured by that trace; the bodies produced here
// follow the pinned handler source shapes and are labeled source-derived, not
// observed. Control-plane liveness (sessions, leases, nodes) is not modeled,
// so the reaper sees no live session vouching for a claim. Guard switches are
// design exploration only: they are not evidence of any server guard.
package fleetsim

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/clock"
)

// Source-derived FleetDB defaults (fleet-db 40e8431d).
const (
	DefaultLockTTL     = 300 * time.Second // issue_service.go ClaimIssue: ttl==0 ⇒ 300s
	ReaperGrace        = 60 * time.Second  // claim_reaper.go defaultClaimReaperGrace
	ReaperInterval     = 30 * time.Second  // claim_reaper.go defaultClaimReaperInterval
	defaultCloseReason = "Closed"
	systemActor        = "system"
	heartbeatLockTTL   = 300 * time.Second // storage/worker.go defaultHeartbeatLockTTL
)

// Guards are server behaviors that the pinned FleetDB does NOT have. They
// exist to explore candidate designs; an outcome under a guard proves nothing
// about the real server.
type Guards struct {
	// RejectNonHolderWorkflowWrites answers 409 to /assign and /close when a
	// live claim lock is held by an actor other than the request's X-Actor.
	RejectNonHolderWorkflowWrites bool
	// OwnershipLeasesUnsupported answers 404 to every
	// /agent-ownership-leases route, standing in for an older server without
	// that capability (the pinned server has the routes). Used for S1.
	OwnershipLeasesUnsupported bool
}

// Issue is the modeled issue row.
type Issue struct {
	ID          string
	Title       string
	Type        string
	Priority    int
	Design      string
	Status      string
	Assignee    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time
	CloseReason string
}

type lock struct {
	holder    string
	expiresAt time.Time
}

// Event is an append-only record of an accepted state change, in the same
// spirit as FleetDB's event log (action names follow models.Action*).
type Event struct {
	At     time.Time
	Actor  string
	Action string
	Issue  string
	Before string
	After  string
	Reason string
}

// Server is the in-memory FleetDB model. It is an http.Handler; Network
// delivers requests to it in-process.
type Server struct {
	mu        sync.Mutex
	clk       clock.Clock
	workspace string
	guards    Guards
	issues    map[string]*Issue
	locks     map[string]lock
	events    []Event
	workers   map[string]string // worker ID → current task ("" when idle)
	unmodeled []string
	mux       *http.ServeMux
}

// NewServer returns a Server for one workspace.
func NewServer(clk clock.Clock, workspace string, guards Guards) *Server {
	s := &Server{
		clk:       clk,
		workspace: workspace,
		guards:    guards,
		issues:    map[string]*Issue{},
		locks:     map[string]lock{},
		workers:   map[string]string{},
	}
	mux := http.NewServeMux()
	p := "/api/v1/{workspace}"
	mux.HandleFunc("GET "+p+"/issues/ready", s.handleReady)
	mux.HandleFunc("GET "+p+"/ready", s.handleReady)
	mux.HandleFunc("GET "+p+"/issues/{id}", s.handleGet)
	mux.HandleFunc("GET "+p+"/issues/{id}/deps", s.handleEmptyList("dependencies"))
	mux.HandleFunc("GET "+p+"/issues/{id}/comments", s.handleEmptyList("comments"))
	mux.HandleFunc("POST "+p+"/workers/{id}/heartbeat", s.handleWorkerHeartbeat)
	mux.HandleFunc(p+"/agent-ownership-leases/", s.handleOwnershipLeases)
	mux.HandleFunc("GET "+p+"/issues", s.handleList)
	mux.HandleFunc("POST "+p+"/issues/{id}/claim", s.handleClaim)
	mux.HandleFunc("POST "+p+"/issues/{id}/assign", s.handleAssign)
	mux.HandleFunc("POST "+p+"/issues/{id}/close", s.handleClose)
	mux.HandleFunc("POST "+p+"/issues/{id}/release-lock", s.handleReleaseLock)
	mux.HandleFunc("POST "+p+"/issues/{id}/release", s.handleRelease)
	mux.HandleFunc("/", s.handleUnmodeled)
	s.mux = mux
	return s
}

// Seed inserts an issue (status defaults to open).
func (s *Server) Seed(is Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	if is.Status == "" {
		is.Status = "open"
	}
	if is.Type == "" {
		is.Type = "task"
	}
	if is.CreatedAt.IsZero() {
		is.CreatedAt = now
	}
	if is.UpdatedAt.IsZero() {
		is.UpdatedAt = now
	}
	c := is
	s.issues[is.ID] = &c
}

// Snapshot returns a copy of the issue and its live lock holder ("" if none).
func (s *Server) Snapshot(id string) (Issue, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	is, ok := s.issues[id]
	if !ok {
		return Issue{}, "", false
	}
	return *is, s.liveHolderLocked(id), true
}

// LockHolder reports the live lock holder for id.
func (s *Server) LockHolder(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveHolderLocked(id)
}

// Events returns a copy of the accepted-event log.
func (s *Server) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// Unmodeled lists requests that reached no modeled route. A scenario that
// produces any is outside the fake's calibrated surface and must fail.
func (s *Server) Unmodeled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.unmodeled...)
}

// ReapStaleClaims runs one ClaimReaper sweep at the current fake time:
// in_progress issues untouched for ReaperGrace with no live lock revert to
// open/unassigned as the system actor (reason lock_expired). Session/lease
// liveness vouching is not modeled. Returns reverted issue IDs in ID order.
func (s *Server) ReapStaleClaims() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	ids := make([]string, 0, len(s.issues))
	for id := range s.issues {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var reverted []string
	for _, id := range ids {
		is := s.issues[id]
		if is.Status != "in_progress" || now.Sub(is.UpdatedAt) < ReaperGrace {
			continue
		}
		if s.liveHolderLocked(id) != "" {
			continue
		}
		before := fmt.Sprintf("status=%s assignee=%s", is.Status, is.Assignee)
		is.Status, is.Assignee, is.UpdatedAt = "open", "", now
		s.appendLocked(Event{At: now, Actor: systemActor, Action: "issue.release", Issue: id,
			Before: before, After: "status=open assignee=", Reason: "lock_expired"})
		reverted = append(reverted, id)
	}
	return reverted
}

// ServeHTTP dispatches to the modeled routes. As with the pinned server in
// --auth-dev-mode, a request carrying no X-Actor has no identity and is
// rejected by the auth middleware with 401 before any handler runs.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("X-Actor")) == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// WorkerTask reports the worker registry entry (current task) for id.
func (s *Server) WorkerTask(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.workers[id]
	return t, ok
}

func (s *Server) handleEmptyList(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		_, ok := s.issues[r.PathValue("id")]
		s.mu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{key: []any{}})
	}
}

// handleWorkerHeartbeat follows storage/worker.go HeartbeatWorker: unknown
// worker → success=false no_active_session; a current task whose lock is
// missing or held by someone else → HTTP 200 with success=false
// ownership_lost; otherwise the task lock is extended by 300s.
func (s *Server) handleWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	task, ok := s.workers[id]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "no_active_session"})
		return
	}
	if task != "" {
		if s.liveHolderLocked(task) != id {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "ownership_lost"})
			return
		}
		s.locks[task] = lock{holder: id, expiresAt: s.clk.Now().Add(heartbeatLockTTL)}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "ttl": int(heartbeatLockTTL.Seconds())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleOwnershipLeases(w http.ResponseWriter, r *http.Request) {
	if s.guards.OwnershipLeasesUnsupported {
		writeError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	s.handleUnmodeled(w, r)
}

func (s *Server) liveHolderLocked(id string) string {
	l, ok := s.locks[id]
	if !ok {
		return ""
	}
	if !s.clk.Now().Before(l.expiresAt) {
		delete(s.locks, id) // Redis key TTL elapsed
		return ""
	}
	return l.holder
}

func (s *Server) appendLocked(e Event) { s.events = append(s.events, e) }

func (s *Server) handleUnmodeled(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.unmodeled = append(s.unmodeled, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	writeError(w, http.StatusNotImplemented, "not_implemented", "fleetsim: route not modeled", nil)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assignee := r.URL.Query().Get("assignee")
	var out []map[string]any
	for _, id := range s.sortedIDsLocked() {
		is := s.issues[id]
		if is.Status != "open" {
			continue
		}
		if assignee != "" && is.Assignee != assignee {
			continue
		}
		out = append(out, issueJSON(is))
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query()
	var out []map[string]any
	for _, id := range s.sortedIDsLocked() {
		is := s.issues[id]
		if st := q.Get("status"); st != "" && is.Status != st {
			continue
		}
		if st := q.Get("status"); st == "" && is.Status == "closed" {
			continue
		}
		if a := q.Get("assignee"); a != "" && is.Assignee != a {
			continue
		}
		out = append(out, issueJSON(is))
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	is, ok := s.issues[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, issueJSON(is))
}

// handleClaim follows issue_service.go ClaimIssue: acquire the lock (same
// holder refreshes), then validate. An in_progress issue held by another
// assignee whose lock is gone is a stale takeover.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	var body struct {
		LockTTL int `json:"lock_ttl"`
	}
	if err := decodeOptional(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	ttl := time.Duration(body.LockTTL) * time.Second
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	now := s.clk.Now()
	if holder := s.liveHolderLocked(id); holder != "" && holder != actor {
		writeError(w, http.StatusConflict, "already_claimed", "issue is already claimed", map[string]string{"existing_owner": holder})
		return
	}
	is, ok := s.issues[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return
	}
	if is.Status == "in_progress" && is.Assignee == actor {
		s.locks[id] = lock{holder: actor, expiresAt: now.Add(ttl)}
		writeJSON(w, http.StatusOK, issueJSON(is))
		return
	}
	staleTakeover := is.Status == "in_progress" && is.Assignee != "" && is.Assignee != actor
	if !staleTakeover && !claimable(is.Status) {
		writeError(w, http.StatusUnprocessableEntity, "not_claimable", "issue is not claimable", nil)
		return
	}
	s.locks[id] = lock{holder: actor, expiresAt: now.Add(ttl)}
	before := fmt.Sprintf("status=%s assignee=%s", is.Status, is.Assignee)
	is.Status, is.Assignee, is.UpdatedAt = "in_progress", actor, now
	s.appendLocked(Event{At: now, Actor: actor, Action: "issue.claim", Issue: id,
		Before: before, After: "status=in_progress assignee=" + actor})
	s.workers[actor] = id // ClaimIssue best-effort worker registration
	writeJSON(w, http.StatusOK, issueJSON(is))
}

// handleAssign follows AssignIssue: modifiable-status check only, no claim or
// attempt match.
func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	var body struct {
		Assignee *string `json:"assignee"`
	}
	if err := decodeOptional(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if body.Assignee == nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "assignee is required", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	is, ok := s.issues[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return
	}
	if is.Status == "closed" || is.Status == "tombstone" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_transition", "issue is closed", nil)
		return
	}
	if s.guardRejectsLocked(w, id, actor) {
		return
	}
	if is.Assignee != *body.Assignee {
		now := s.clk.Now()
		before := "assignee=" + is.Assignee
		is.Assignee, is.UpdatedAt = *body.Assignee, now
		s.appendLocked(Event{At: now, Actor: actor, Action: "issue.assign", Issue: id,
			Before: before, After: "assignee=" + is.Assignee})
	}
	writeJSON(w, http.StatusOK, issueJSON(is))
}

// handleClose follows CloseIssue: existence and already-closed checks only;
// in_progress assignee is cleared; the claim lock is NOT released. A repeated
// close answers 200 with the current issue (handler idempotency).
func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeOptional(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	is, ok := s.issues[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return
	}
	if is.Status == "closed" {
		writeJSON(w, http.StatusOK, issueJSON(is))
		return
	}
	if s.guardRejectsLocked(w, id, actor) {
		return
	}
	reason := body.Reason
	if reason == "" {
		reason = defaultCloseReason
	}
	now := s.clk.Now()
	before := fmt.Sprintf("status=%s assignee=%s", is.Status, is.Assignee)
	if is.Status == "in_progress" {
		is.Assignee = ""
	}
	closedAt := now
	is.Status, is.ClosedAt, is.CloseReason, is.UpdatedAt = "closed", &closedAt, reason, now
	s.appendLocked(Event{At: now, Actor: actor, Action: "issue.close", Issue: id,
		Before: before, After: "status=closed assignee=" + is.Assignee, Reason: reason})
	writeJSON(w, http.StatusOK, issueJSON(is))
}

// handleReleaseLock follows ReleaseIssueLock: holder-scoped; missing lock is
// idempotent 204; a different live holder is 409 already_claimed.
func (s *Server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	holder := s.liveHolderLocked(id)
	switch {
	case holder == "":
		w.WriteHeader(http.StatusNoContent)
	case holder == actor:
		delete(s.locks, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusConflict, "already_claimed", "issue is not assigned", map[string]string{"existing_owner": holder})
	}
}

// handleRelease follows ReleaseIssue: in_progress and assignee==actor
// required; reverts to open/unassigned and drops the actor's lock.
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	is, ok := s.issues[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return
	}
	if is.Status != "in_progress" {
		writeError(w, http.StatusUnprocessableEntity, "not_claimable", "issue is not claimable", nil)
		return
	}
	if is.Assignee != actor {
		writeError(w, http.StatusConflict, "conflict", "issue is not assigned", nil)
		return
	}
	now := s.clk.Now()
	is.Status, is.Assignee, is.UpdatedAt = "open", "", now
	if l, ok := s.locks[id]; ok && l.holder == actor {
		delete(s.locks, id)
	}
	s.appendLocked(Event{At: now, Actor: actor, Action: "issue.release", Issue: id,
		Before: "status=in_progress assignee=" + actor, After: "status=open assignee="})
	s.workers[actor] = "" // ReleaseIssue re-registers the worker idle
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) guardRejectsLocked(w http.ResponseWriter, id, actor string) bool {
	if !s.guards.RejectNonHolderWorkflowWrites {
		return false
	}
	holder := s.liveHolderLocked(id)
	if holder == "" || holder == actor {
		return false
	}
	writeError(w, http.StatusConflict, "not_lock_holder", "fleetsim guard: write by non-holder", map[string]string{"existing_owner": holder})
	return true
}

func (s *Server) sortedIDsLocked() []string {
	ids := make([]string, 0, len(s.issues))
	for id := range s.issues {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func claimable(status string) bool {
	switch status {
	case "open", "deferred", "review":
		return true
	}
	return false
}

func issueJSON(is *Issue) map[string]any {
	m := map[string]any{
		"id":         is.ID,
		"title":      is.Title,
		"status":     is.Status,
		"priority":   is.Priority,
		"type":       is.Type,
		"has_design": strings.TrimSpace(is.Design) != "",
		"created_at": is.CreatedAt.UTC(),
		"updated_at": is.UpdatedAt.UTC(),
	}
	if is.Assignee != "" {
		m["assignee"] = is.Assignee
	}
	if is.Design != "" {
		m["design"] = is.Design
	}
	if is.ClosedAt != nil {
		m["closed_at"] = is.ClosedAt.UTC()
		m["close_reason"] = is.CloseReason
	}
	return m
}

func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeJSON / writeError mirror fleet-db internal/api/response.go shapes
// (native dialect: bare data on 2xx; {"error":{code,message,meta}} otherwise).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, meta map[string]string) {
	e := map[string]any{"code": code, "message": msg}
	if len(meta) > 0 {
		e["meta"] = meta
	}
	writeJSON(w, status, map[string]any{"error": e})
}
