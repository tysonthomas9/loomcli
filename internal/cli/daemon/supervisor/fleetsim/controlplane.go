package fleetsim

// Control-plane subset of the FleetDB model: agent ownership leases, agent
// sessions, session (agent) leases, worker deregistration, and the background
// lease reaper. Every rule is read from the pinned FleetDB source (fleet-db
// 40e8431d) on its Redis path, which is what the local-mode stack runs:
//
//   - internal/api/control_plane.go: routes, request defaults (5m TTL when
//     ttl_seconds is absent), status codes (201 create, 200 otherwise) and
//     writeStorageError (404 not_found, 409 already_exists / already_claimed,
//     403 forbidden for not-owner, 410 lease_expired).
//   - internal/storage/redis.go acquireOwnershipLeaseLua /
//     renewOwnershipLeaseLua / releaseOwnershipLeaseLua and
//     internal/storage/control_plane.go: ownership acquire refuses a live lease
//     of another owner (409 already_claimed), bumps a per-workspace fence on
//     every success and keeps the token on a live same-owner re-acquire;
//     renew checks token (403), then liveness (410); release checks the
//     token ONLY and marks the lease released (no fence, no liveness).
//   - CreateAgentSession / UpdateAgentSession: the update is an unconditional
//     read-modify-write; Validate checks only that the status is a known
//     value, so a terminal session can be moved back to running.
//   - CreateAgentLease / RenewAgentLease / ReleaseAgentLease: any number of
//     leases per session, a per-workspace lease fence, renew checks token then
//     liveness, release checks token only.
//   - internal/service/worker_service.go DeregisterWorker (DELETE
//     /workers/{id}): releases a claim the worker still holds, then removes
//     the registration; 204.
//   - internal/service/lease_reaper.go ReapWorkspace (see ReapControlPlane).
//
// Nothing here was observed on a real FleetDB: the e034de04 capture contains
// no control-plane traffic. Response bodies are source-derived.

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source-derived control-plane defaults (fleet-db 40e8431d).
const (
	controlPlaneDefaultTTL = 5 * time.Minute  // api/control_plane.go parseTTL / create defaults
	LeaseReaperGrace       = 5 * time.Minute  // lease_reaper.go defaultLeaseReaperGrace (running)
	StuckStartupGrace      = 30 * time.Minute // lease_reaper.go defaultStuckStartupGrace (queued/leased/starting)
	SessionHeartbeatTTL    = 10 * time.Minute // lease_reaper.go defaultSessionHeartbeatTTL
	heartbeatEvidence      = 60 * time.Second // lease_reaper.go heartbeatEvidenceWindow
	sessionLeaseLostClass  = "lease_lost"
	sessionHBLostClass     = "heartbeat_lost"
)

// OwnershipLease is the modeled agent-ownership lease hash.
type OwnershipLease struct {
	AgentID       string
	LeaseID       string
	OwnerID       string
	NodeID        string
	Token         string
	FencingToken  int64
	Status        string // active | released
	ExpiresAt     time.Time
	LastHeartbeat time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Live reports whether the lease is active and unexpired at now (the Lua
// scripts compare expires_at_unix > now).
func (l OwnershipLease) Live(now time.Time) bool {
	return l.Status == "active" && l.ExpiresAt.After(now)
}

// Session is the modeled agent-session record.
type Session struct {
	SessionID     string
	AgentID       string
	NodeID        string
	TaskID        string
	Status        string
	Phase         string
	StartedAt     time.Time
	LastHeartbeat time.Time
	FinishedAt    *time.Time
	ErrorClass    string
	Summary       string
	ExitCode      *int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SessionLease is the modeled agent (session) lease.
type SessionLease struct {
	LeaseID       string
	SessionID     string
	AgentID       string
	NodeID        string
	Token         string
	FencingToken  int64
	Status        string // active | released | expired
	ExpiresAt     time.Time
	LastHeartbeat time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TerminalSessionStatus mirrors models.AgentSessionStatus.IsTerminal.
func TerminalSessionStatus(s string) bool {
	switch s {
	case "completed", "failed", "cancelled", "expired":
		return true
	}
	return false
}

func validSessionStatus(s string) bool {
	switch s {
	case "queued", "leased", "starting", "running", "idle", "yielded",
		"completed", "failed", "cancelled", "expired":
		return true
	}
	return false
}

type controlPlane struct {
	ownership    map[string]*OwnershipLease // agent ID → lease
	ownFence     int64
	sessions     map[string]*Session
	leases       map[string]*SessionLease
	leaseFence   int64
	tokenCounter int
}

func newControlPlane() controlPlane {
	return controlPlane{
		ownership: map[string]*OwnershipLease{},
		sessions:  map[string]*Session{},
		leases:    map[string]*SessionLease{},
	}
}

func (s *Server) registerControlPlane(mux *http.ServeMux, p string) {
	mux.HandleFunc("POST "+p+"/agent-ownership-leases/{agent}/acquire", s.handleOwnershipAcquire)
	mux.HandleFunc("GET "+p+"/agent-ownership-leases/{agent}", s.handleOwnershipGet)
	mux.HandleFunc("POST "+p+"/agent-ownership-leases/{agent}/heartbeat", s.handleOwnershipHeartbeat)
	mux.HandleFunc("POST "+p+"/agent-ownership-leases/{agent}/release", s.handleOwnershipRelease)
	mux.HandleFunc("POST "+p+"/agent-sessions", s.handleSessionCreate)
	mux.HandleFunc("GET "+p+"/agent-sessions/{sid}", s.handleSessionGet)
	mux.HandleFunc("PATCH "+p+"/agent-sessions/{sid}", s.handleSessionUpdate)
	mux.HandleFunc("POST "+p+"/agent-sessions/{sid}/leases", s.handleLeaseCreate)
	mux.HandleFunc("GET "+p+"/agent-leases/{lid}", s.handleLeaseGet)
	mux.HandleFunc("POST "+p+"/agent-leases/{lid}/heartbeat", s.handleLeaseHeartbeat)
	mux.HandleFunc("POST "+p+"/agent-leases/{lid}/release", s.handleLeaseRelease)
	mux.HandleFunc("DELETE "+p+"/workers/{id}", s.handleWorkerDeregister)
}

func (s *Server) newTokenLocked(prefix string) string {
	s.cp.tokenCounter++
	return fmt.Sprintf("%s-%04d", prefix, s.cp.tokenCounter)
}

func ttlFromSeconds(sec int) time.Duration {
	if sec <= 0 {
		return controlPlaneDefaultTTL
	}
	return time.Duration(sec) * time.Second
}

func queryTTL(r *http.Request) time.Duration {
	sec, _ := strconv.Atoi(r.URL.Query().Get("ttl_seconds"))
	return ttlFromSeconds(sec)
}

// --- ownership leases ---

func (s *Server) ownershipUnsupported(w http.ResponseWriter) bool {
	if s.guards.OwnershipLeasesUnsupported {
		writeError(w, http.StatusNotFound, "not_found", "not found", nil)
		return true
	}
	return false
}

func (s *Server) handleOwnershipAcquire(w http.ResponseWriter, r *http.Request) {
	if s.ownershipUnsupported(w) {
		return
	}
	var req struct {
		LeaseID      string `json:"lease_id"`
		OwnerID      string `json:"owner_id"`
		NodeID       string `json:"node_id"`
		TTLSeconds   int    `json:"ttl_seconds"`
		TakeoverFrom string `json:"takeover_from_owner_id"`
	}
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.OwnerID) == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "owner_id is required", nil)
		return
	}
	takeover := strings.TrimSpace(req.TakeoverFrom)
	if takeover == strings.TrimSpace(req.OwnerID) {
		takeover = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	agent := r.PathValue("agent")
	now := s.clk.Now()
	token := s.newTokenLocked("own")
	if cur, ok := s.cp.ownership[agent]; ok && cur.Live(now) {
		if cur.OwnerID != req.OwnerID && (takeover == "" || cur.OwnerID != takeover) {
			writeError(w, http.StatusConflict, "already_claimed", "acquire agent ownership lease failed", nil)
			return
		}
		if cur.OwnerID == req.OwnerID {
			token = cur.Token // live same-owner re-acquire keeps the token
		}
	}
	if req.LeaseID == "" {
		req.LeaseID = "ol-" + s.newTokenLocked("id")
	}
	s.cp.ownFence++
	l := &OwnershipLease{AgentID: agent, LeaseID: req.LeaseID, OwnerID: req.OwnerID, NodeID: req.NodeID,
		Token: token, FencingToken: s.cp.ownFence, Status: "active", ExpiresAt: now.Add(ttlFromSeconds(req.TTLSeconds)),
		LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	s.cp.ownership[agent] = l
	writeJSON(w, http.StatusOK, ownershipJSON(s.workspace, l))
}

func (s *Server) handleOwnershipGet(w http.ResponseWriter, r *http.Request) {
	if s.ownershipUnsupported(w) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.ownership[r.PathValue("agent")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "get agent ownership lease failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, ownershipJSON(s.workspace, l))
}

func (s *Server) handleOwnershipHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.ownershipUnsupported(w) {
		return
	}
	token := r.Header.Get("X-Agent-Ownership-Lease-Token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "X-Agent-Ownership-Lease-Token required", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	l, ok := s.cp.ownership[r.PathValue("agent")]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "not_found", "heartbeat agent ownership lease failed", nil)
	case l.Token != token:
		writeError(w, http.StatusForbidden, "forbidden", "heartbeat agent ownership lease failed", nil)
	case !l.Live(now):
		writeError(w, http.StatusGone, "lease_expired", "heartbeat agent ownership lease failed", nil)
	default:
		l.ExpiresAt, l.LastHeartbeat, l.UpdatedAt = now.Add(queryTTL(r)), now, now
		writeJSON(w, http.StatusOK, ownershipJSON(s.workspace, l))
	}
}

func (s *Server) handleOwnershipRelease(w http.ResponseWriter, r *http.Request) {
	if s.ownershipUnsupported(w) {
		return
	}
	token := r.Header.Get("X-Agent-Ownership-Lease-Token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "X-Agent-Ownership-Lease-Token required", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.ownership[r.PathValue("agent")]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "not_found", "release agent ownership lease failed", nil)
	case l.Token != token:
		writeError(w, http.StatusForbidden, "forbidden", "release agent ownership lease failed", nil)
	default:
		l.Status, l.UpdatedAt = "released", s.clk.Now()
		writeJSON(w, http.StatusOK, ownershipJSON(s.workspace, l))
	}
}

// --- sessions ---

func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		NodeID    string `json:"node_id"`
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		Phase     string `json:"phase"`
	}
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if req.Status == "" {
		req.Status = "queued"
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.AgentID) == "" || !validSessionStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "validation_failed", "create agent session: invalid", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.cp.sessions[req.SessionID]; dup {
		writeError(w, http.StatusConflict, "already_exists", "create agent session failed", nil)
		return
	}
	now := s.clk.Now()
	sess := &Session{SessionID: req.SessionID, AgentID: req.AgentID, NodeID: req.NodeID, TaskID: req.TaskID,
		Status: req.Status, Phase: req.Phase, StartedAt: now, CreatedAt: now, UpdatedAt: now}
	s.cp.sessions[req.SessionID] = sess
	writeJSON(w, http.StatusCreated, sessionJSON(s.workspace, sess))
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.cp.sessions[r.PathValue("sid")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "get agent session failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, sessionJSON(s.workspace, sess))
}

// handleSessionUpdate is UpdateAgentSession: read, apply, validate the status
// value, write. No transition, ownership or terminal check.
func (s *Server) handleSessionUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID        *string    `json:"task_id"`
		Status        *string    `json:"status"`
		Phase         *string    `json:"phase"`
		LastHeartbeat *time.Time `json:"last_heartbeat"`
		FinishedAt    *time.Time `json:"finished_at"`
		Summary       *string    `json:"summary"`
		ErrorClass    *string    `json:"error_class"`
		ExitCode      *int       `json:"exit_code"`
	}
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.cp.sessions[r.PathValue("sid")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "update agent session failed", nil)
		return
	}
	if req.Status != nil && !validSessionStatus(*req.Status) {
		writeError(w, http.StatusBadRequest, "validation_failed", "status is invalid", nil)
		return
	}
	applySessionUpdate(sess, req.TaskID, req.Status, req.Phase, req.LastHeartbeat, req.FinishedAt, req.Summary, req.ErrorClass, req.ExitCode)
	sess.UpdatedAt = s.clk.Now()
	writeJSON(w, http.StatusOK, sessionJSON(s.workspace, sess))
}

func applySessionUpdate(sess *Session, taskID, status, phase *string, hb, finished *time.Time, summary, errClass *string, exit *int) {
	if taskID != nil {
		sess.TaskID = *taskID
	}
	if status != nil {
		sess.Status = *status
	}
	if phase != nil {
		sess.Phase = *phase
	}
	if hb != nil {
		sess.LastHeartbeat = *hb
	}
	if finished != nil {
		f := *finished
		sess.FinishedAt = &f
	}
	if summary != nil {
		sess.Summary = *summary
	}
	if errClass != nil {
		sess.ErrorClass = *errClass
	}
	if exit != nil {
		e := *exit
		sess.ExitCode = &e
	}
}

// --- session (agent) leases ---

func (s *Server) handleLeaseCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LeaseID    string `json:"lease_id"`
		AgentID    string `json:"agent_id"`
		NodeID     string `json:"node_id"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.LeaseID == "" {
		req.LeaseID = "lease-" + s.newTokenLocked("id")
	}
	if _, dup := s.cp.leases[req.LeaseID]; dup {
		writeError(w, http.StatusConflict, "already_exists", "create agent lease failed", nil)
		return
	}
	now := s.clk.Now()
	s.cp.leaseFence++
	l := &SessionLease{LeaseID: req.LeaseID, SessionID: r.PathValue("sid"), AgentID: req.AgentID, NodeID: req.NodeID,
		Token: s.newTokenLocked("lease"), FencingToken: s.cp.leaseFence, Status: "active",
		ExpiresAt: now.Add(ttlFromSeconds(req.TTLSeconds)), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	s.cp.leases[req.LeaseID] = l
	writeJSON(w, http.StatusCreated, leaseJSON(s.workspace, l))
}

func (s *Server) handleLeaseGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.leases[r.PathValue("lid")]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "get agent lease failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, leaseJSON(s.workspace, l))
}

func (s *Server) handleLeaseHeartbeat(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agent-Lease-Token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "X-Agent-Lease-Token required", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	l, ok := s.cp.leases[r.PathValue("lid")]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "not_found", "heartbeat agent lease failed", nil)
	case l.Token != token:
		writeError(w, http.StatusForbidden, "forbidden", "heartbeat agent lease failed", nil)
	case l.Status != "active" || !l.ExpiresAt.After(now):
		writeError(w, http.StatusGone, "lease_expired", "heartbeat agent lease failed", nil)
	default:
		l.LastHeartbeat, l.ExpiresAt, l.UpdatedAt = now, now.Add(queryTTL(r)), now
		writeJSON(w, http.StatusOK, leaseJSON(s.workspace, l))
	}
}

func (s *Server) handleLeaseRelease(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agent-Lease-Token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "X-Agent-Lease-Token required", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.leases[r.PathValue("lid")]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "not_found", "release agent lease failed", nil)
	case l.Token != token:
		writeError(w, http.StatusForbidden, "forbidden", "release agent lease failed", nil)
	default:
		l.Status, l.UpdatedAt = "released", s.clk.Now()
		writeJSON(w, http.StatusOK, leaseJSON(s.workspace, l))
	}
}

// handleWorkerDeregister is DeregisterWorker: if the worker's current task is
// still locked by the worker, revert the claim (releaseClaimAs: only while the
// issue is in_progress and assigned to the worker) and drop the lock; then
// remove the registration. 204.
func (s *Server) handleWorkerDeregister(w http.ResponseWriter, r *http.Request) {
	actor := r.Header.Get("X-Actor")
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	if task, ok := s.workers[id]; ok && task != "" && s.liveHolderLocked(task) == id {
		if is := s.issues[task]; is != nil && is.Status == "in_progress" && is.Assignee == id {
			now := s.clk.Now()
			is.Status, is.Assignee, is.UpdatedAt = "open", "", now
			s.appendLocked(Event{At: now, Actor: actor, Action: "issue.release", Issue: task,
				Before: "status=in_progress assignee=" + id, After: "status=open assignee="})
		}
		delete(s.locks, task)
	}
	delete(s.workers, id)
	w.WriteHeader(http.StatusNoContent)
}

// ReapResult reports what one ReapControlPlane sweep retired.
type ReapResult struct {
	LeasesExpired   []string
	LeasesReleased  []string
	SessionsRetired map[string]string // session ID → new status
}

// ReapControlPlane runs one LeaseReaper.ReapWorkspace sweep at the current
// fake time with the pinned defaults (grace 5m for running, 30m for
// queued/leased/starting, heartbeat TTL 10m gated on heartbeat evidence):
//  1. expire every active lease whose expires_at <= now;
//  2. release active leases whose session is terminal;
//  3. retire non-terminal sessions: heartbeat rule (failed/heartbeat_lost)
//     first, else the lease rule (expired/lease_lost) when no active
//     unexpired lease names the session and it has been silent for the
//     status's grace.
//
// The pinned server runs this every 30s by default (FLEET_LEASE_REAPER_INTERVAL
// unset ⇒ enabled); a scenario calls it at the instants it wants a sweep.
// Disabling the reaper (FLEET_LEASE_REAPER_INTERVAL=0) is modeled by never
// calling it.
func (s *Server) ReapControlPlane() ReapResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	res := ReapResult{SessionsRetired: map[string]string{}}
	leased := s.reapLeasesLocked(now, &res)
	s.reapSessionsLocked(now, leased, &res)
	return res
}

// reapLeasesLocked expires lapsed leases, releases leases of terminal
// sessions, and returns the sessions an active unexpired lease still vouches
// for.
func (s *Server) reapLeasesLocked(now time.Time, res *ReapResult) map[string]bool {
	for _, id := range sortedKeys(s.cp.leases) {
		l := s.cp.leases[id]
		if l.Status == "active" && !l.ExpiresAt.After(now) {
			l.Status, l.UpdatedAt = "expired", now
			res.LeasesExpired = append(res.LeasesExpired, id)
		}
	}
	leased := map[string]bool{}
	for _, id := range sortedKeys(s.cp.leases) {
		l := s.cp.leases[id]
		if l.Status != "active" {
			continue
		}
		if sess := s.cp.sessions[l.SessionID]; sess != nil && TerminalSessionStatus(sess.Status) && l.Token != "" {
			l.Status, l.UpdatedAt = "released", now
			res.LeasesReleased = append(res.LeasesReleased, id)
			continue
		}
		if l.ExpiresAt.After(now) {
			leased[l.SessionID] = true
		}
	}
	return leased
}

// reapSessionsLocked applies the heartbeat rule, then the lease rule, per
// non-terminal status policy.
func (s *Server) reapSessionsLocked(now time.Time, leased map[string]bool, res *ReapResult) {
	policies := []struct {
		status string
		grace  time.Duration
	}{
		{"running", LeaseReaperGrace}, {"queued", StuckStartupGrace}, {"leased", StuckStartupGrace},
		{"starting", StuckStartupGrace}, {"idle", 0}, {"yielded", 0},
	}
	for _, p := range policies {
		for _, id := range sortedKeys(s.cp.sessions) {
			sess := s.cp.sessions[id]
			if sess.Status != p.status {
				continue
			}
			status, class := "", ""
			switch {
			case sess.LastHeartbeat.After(sess.CreatedAt.Add(heartbeatEvidence)) && now.Sub(sess.LastHeartbeat) >= SessionHeartbeatTTL:
				status, class = "failed", sessionHBLostClass
			case p.grace > 0 && !leased[id] && now.Sub(sessionFreshness(sess)) >= p.grace:
				status, class = "expired", sessionLeaseLostClass
			default:
				continue
			}
			f := now
			sess.Status, sess.ErrorClass, sess.FinishedAt, sess.UpdatedAt = status, class, &f, now
			res.SessionsRetired[id] = status
		}
	}
}

func sessionFreshness(sess *Session) time.Time {
	t := sess.LastHeartbeat
	if sess.StartedAt.After(t) {
		t = sess.StartedAt
	}
	if sess.CreatedAt.After(t) {
		t = sess.CreatedAt
	}
	return t
}

// OwnershipSnapshot returns a copy of the agent's ownership lease.
func (s *Server) OwnershipSnapshot(agent string) (OwnershipLease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.ownership[agent]
	if !ok {
		return OwnershipLease{}, false
	}
	return *l, true
}

// SessionSnapshot returns a copy of the session record.
func (s *Server) SessionSnapshot(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.cp.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *sess, true
}

// LeaseSnapshot returns a copy of the session lease.
func (s *Server) LeaseSnapshot(id string) (SessionLease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cp.leases[id]
	if !ok {
		return SessionLease{}, false
	}
	return *l, true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ownershipJSON(ws string, l *OwnershipLease) map[string]any {
	return map[string]any{
		"workspace_key": ws, "agent_id": l.AgentID, "lease_id": l.LeaseID, "owner_id": l.OwnerID,
		"node_id": l.NodeID, "token": l.Token, "fencing_token": l.FencingToken, "status": l.Status,
		"expires_at": l.ExpiresAt.UTC(), "last_heartbeat": l.LastHeartbeat.UTC(),
		"created_at": l.CreatedAt.UTC(), "updated_at": l.UpdatedAt.UTC(),
	}
}

func sessionJSON(ws string, sess *Session) map[string]any {
	m := map[string]any{
		"workspace_key": ws, "session_id": sess.SessionID, "agent_id": sess.AgentID, "node_id": sess.NodeID,
		"kind": "task", "task_id": sess.TaskID, "status": sess.Status, "phase": sess.Phase,
		"started_at": sess.StartedAt.UTC(), "last_heartbeat": sess.LastHeartbeat.UTC(),
		"error_class": sess.ErrorClass, "summary": sess.Summary,
		"created_at": sess.CreatedAt.UTC(), "updated_at": sess.UpdatedAt.UTC(),
	}
	if sess.FinishedAt != nil {
		m["finished_at"] = sess.FinishedAt.UTC()
	}
	if sess.ExitCode != nil {
		m["exit_code"] = *sess.ExitCode
	}
	return m
}

func leaseJSON(ws string, l *SessionLease) map[string]any {
	return map[string]any{
		"workspace_key": ws, "lease_id": l.LeaseID, "session_id": l.SessionID, "agent_id": l.AgentID,
		"node_id": l.NodeID, "token": l.Token, "fencing_token": l.FencingToken, "status": l.Status,
		"expires_at": l.ExpiresAt.UTC(), "last_heartbeat": l.LastHeartbeat.UTC(),
		"created_at": l.CreatedAt.UTC(), "updated_at": l.UpdatedAt.UTC(),
	}
}
