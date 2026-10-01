package fleetsim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/clock"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

// BaseURL is the address every simulated FleetBackend targets. Nothing listens
// there: the Network transport answers in-process.
const BaseURL = "http://fleetsim.invalid"

// ErrResponseLost is returned to a client whose request the server applied
// but whose response the interposer dropped.
var ErrResponseLost = errors.New("fleetsim: response lost after apply")

// ErrRequestDropped is returned to a client whose request never reached the
// server.
var ErrRequestDropped = errors.New("fleetsim: request dropped before apply")

// ErrClientGaveUp is returned to a client whose request Abandon released
// before the server applied it: the caller's context deadline fired while the
// request was still in flight. It wraps context.DeadlineExceeded, which is
// what net/http reports in that case. The request stays pending and may still
// apply later.
var ErrClientGaveUp = fmt.Errorf("fleetsim: client gave up before apply: %w", context.DeadlineExceeded)

// Delivery selects what the interposer does with a pending request.
type Delivery int

const (
	// Apply delivers the request and returns the response.
	Apply Delivery = iota
	// ApplyLoseResponse applies the request, then fails the client call.
	ApplyLoseResponse
	// Drop fails the client call without applying.
	Drop
)

func (d Delivery) String() string {
	switch d {
	case Apply:
		return "applied"
	case ApplyLoseResponse:
		return "applied_response_lost"
	case Drop:
		return "dropped"
	}
	return "unknown"
}

// Pending is a request parked in the interposer.
type Pending struct {
	// Seq is the global send order. When actors run concurrently it depends
	// on goroutine scheduling; AttemptSeq (per attempt) does not, so
	// schedulers key on (Attempt, AttemptSeq).
	Seq        int
	AttemptSeq int
	Attempt    string
	Method     string
	Path       string // workspace-relative, e.g. /issues/LOCALMODE-3/claim
	Query      string
	Actor      string // X-Actor header as Loom sent it
	Body       string
	SentAt     time.Time
	// Abandoned is set once Abandon released the caller; the request is
	// still in flight and applies whenever it is delivered.
	Abandoned bool
	// Hook and AfterOwnershipKill are send-time stamps; see Record.
	Hook               bool
	AfterOwnershipKill bool

	req   *http.Request
	reply chan reply
}

type reply struct {
	resp *http.Response
	err  error
}

// Record is one interposer decision, in server-apply order.
type Record struct {
	Seq        int
	AttemptSeq int
	Attempt    string
	Method     string
	Path       string
	Query      string
	Actor      string
	Body       string
	SentAt     time.Time
	AppliedAt  time.Time
	Delivery   Delivery
	Status     int // 0 when dropped
	// RespBody is the fake server's response body. It is source-derived,
	// never an observed FleetDB body.
	RespBody string
	IssueID  string
	// Server state for IssueID immediately before apply, for the oracle.
	HolderBefore   string
	StatusBefore   string
	AssigneeBefore string
	// AuthorityBefore is the attempt holding write authority on IssueID at
	// apply time: the attempt whose claim most recently succeeded, while its
	// claim actor still holds the live lock. "" means nobody holds authority.
	AuthorityBefore string
	// Abandoned: the caller had already given up (Abandon) when this applied.
	Abandoned bool

	// Control-plane stamps (zero for issue routes).
	// Agent is the agent ID the control-plane request concerns.
	Agent string
	// OwnerAuthorityBefore is the attempt holding ownership authority for
	// Agent at apply time: the attempt whose ownership acquire most recently
	// succeeded, while that acquire's fence is still the server's active,
	// unexpired lease. "" means nobody holds ownership.
	OwnerAuthorityBefore string
	// SessionID / SessionStatusBefore / SessionAttempt describe the session a
	// session or session-lease request touches; SessionAttempt is the attempt
	// whose create of that session succeeded.
	SessionID           string
	SessionStatusBefore string
	SessionAttempt      string
	// LeaseAttempt is the attempt whose create of the session lease succeeded.
	LeaseAttempt string
	// ReqStatus is the "status" a session PATCH asked for.
	ReqStatus string

	// Completion-hook stamps, taken when the request was SENT (see
	// BeginHooks, OwnershipKilled). ClaimedBefore is the issue the sending
	// attempt's own claim record names at apply time: the issue of its last
	// successful claim ("" if it never claimed).
	Hook               bool
	AfterOwnershipKill bool
	ClaimedBefore      string
}

// Observation is a local fact about an attempt that never crosses the wire:
// its supervisor still believes it owns the agent, or its agent process is
// still running. The oracle judges it against server-side ownership at the
// observation instant.
type Observation struct {
	At             time.Time
	Attempt        string
	Agent          string
	What           string
	OwnerAuthority string
}

type ownGrant struct {
	attempt string
	fence   int64
}

// Sim couples a fake clock, the FleetDB model and the interposer, and
// schedules actor goroutines deterministically: actors run until every one is
// parked on the network or finished (quiescence), then exactly one pending
// request is resolved.
type Sim struct {
	Clock     *clock.Fake
	Server    *Server
	Workspace string

	mu      sync.Mutex
	cond    *sync.Cond
	running int
	actors  map[string]bool // name → finished
	errs    map[string]error
	pending []*Pending
	seq     int
	attSeq  map[string]int
	records []Record
	claimOf map[string]string // attempt → claim actor
	auth    map[string]string // issue → attempt of the last successful claim

	own       map[string]ownGrant // agent → last successful ownership acquire
	sessionOf map[string]string   // session ID → creating attempt
	leaseOf   map[string]string   // session lease ID → creating attempt
	ended     map[string]bool     // attempts whose process is gone
	obs       []Observation

	claimed   map[string]string // attempt → issue of its last successful claim
	hookPhase map[string]bool   // attempts currently running completion hooks
	killed    map[string]bool   // attempts whose ownership kill was recorded
}

// New builds a Sim at start for workspace.
func New(start time.Time, workspace string, guards Guards) *Sim {
	clk := clock.NewFake(start)
	s := &Sim{
		Clock:     clk,
		Server:    NewServer(clk, workspace, guards),
		Workspace: workspace,
		actors:    map[string]bool{},
		errs:      map[string]error{},
		claimOf:   map[string]string{},
		attSeq:    map[string]int{},
		auth:      map[string]string{},
		own:       map[string]ownGrant{},
		sessionOf: map[string]string{},
		leaseOf:   map[string]string{},
		ended:     map[string]bool{},
		claimed:   map[string]string{},
		hookPhase: map[string]bool{},
		killed:    map[string]bool{},
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// BindAttempt records that attempt holds (or held) claims as claimActor. The
// oracle attributes writes by attempt, never by X-Actor, so this is where the
// actor split is made explicit.
func (s *Sim) BindAttempt(attempt, claimActor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimOf[attempt] = claimActor
}

// ClaimActor returns the claim actor bound to attempt.
func (s *Sim) ClaimActor(attempt string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claimOf[attempt]
}

// Backend returns the real Loom FleetDB adapter wired to this Sim. Every
// request it makes is attributed to attempt. actor is the adapter's configured
// process X-Actor (per-call overrides still apply, exactly as in production).
func (s *Sim) Backend(attempt, actor string) *fleet.FleetBackend {
	b, err := fleet.New(fleet.Config{
		BaseURL:     BaseURL,
		WorkspaceID: s.Workspace,
		Actor:       actor,
		HTTPClient:  &http.Client{Transport: &transport{sim: s, attempt: attempt}},
	})
	if err != nil {
		panic(err) // static config; cannot fail
	}
	return b
}

// ControlStore returns the real Loom control-plane client
// (internal/infra/fleetdb) wired to this Sim, attributed to attempt.
func (s *Sim) ControlStore(attempt, actor string) *fleetdb.Client {
	c, err := fleetdb.New(fleetdb.Config{
		BaseURL:    BaseURL,
		Actor:      actor,
		HTTPClient: &http.Client{Transport: &transport{sim: s, attempt: attempt}},
	})
	if err != nil {
		panic(err) // static config; cannot fail
	}
	return c
}

// Go starts a named actor. Its goroutine counts as running until it parks on
// the network or returns. fn's error is kept for Err.
func (s *Sim) Go(name string, fn func() error) {
	s.mu.Lock()
	if _, dup := s.actors[name]; dup {
		s.mu.Unlock()
		panic("fleetsim: duplicate actor " + name)
	}
	s.actors[name] = false
	s.running++
	s.mu.Unlock()
	go func() {
		err := fn()
		s.mu.Lock()
		s.actors[name] = true
		s.errs[name] = err
		s.running--
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
}

// Quiesce blocks until no actor is running (all parked or finished).
func (s *Sim) Quiesce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.running > 0 {
		s.cond.Wait()
	}
}

// Finished reports whether actor name has returned.
func (s *Sim) Finished(name string) bool {
	s.Quiesce()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actors[name]
}

// Err returns the error actor name returned (after it finished).
func (s *Sim) Err(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs[name]
}

// Pending returns the parked requests in send order, after quiescence.
func (s *Sim) Pending() []Pending {
	s.Quiesce()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pending, 0, len(s.pending))
	for _, p := range s.pending {
		out = append(out, *p)
	}
	return out
}

// AwaitPending blocks until at least n requests are parked. Use it for
// requests issued by goroutines the supervisor starts itself (heartbeat
// tickers), which are not Sim actors; such goroutines must not run
// concurrently with Sim actors, because only actors count toward quiescence.
func (s *Sim) AwaitPending(n int) []Pending {
	s.mu.Lock()
	for len(s.pending) < n {
		s.cond.Wait()
	}
	out := make([]Pending, 0, len(s.pending))
	for _, p := range s.pending {
		out = append(out, *p)
	}
	s.mu.Unlock()
	return out
}

// Records returns the interposer log in apply order.
func (s *Sim) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// stampBefore records the issue state and attempt authority the request
// meets at apply time.
func (s *Sim) stampBefore(rec *Record) {
	if rec.IssueID == "" {
		s.stampControlPlane(rec)
		return
	}
	if is, holder, ok := s.Server.Snapshot(rec.IssueID); ok {
		rec.HolderBefore, rec.StatusBefore, rec.AssigneeBefore = holder, is.Status, is.Assignee
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.ClaimedBefore = s.claimed[rec.Attempt]
	if a := s.auth[rec.IssueID]; a != "" && rec.HolderBefore != "" && rec.HolderBefore == s.claimOf[a] {
		rec.AuthorityBefore = a
	}
}

// Deliver resolves the pending request seq. When at is non-zero the fake clock
// is first set to at, so the server stamps the calibrated time. It waits for
// quiescence afterwards.
func (s *Sim) Deliver(seq int, d Delivery, at time.Time) Record {
	s.Quiesce()
	s.mu.Lock()
	idx := -1
	for i, p := range s.pending {
		if p.Seq == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		panic(fmt.Sprintf("fleetsim: no pending request seq=%d", seq))
	}
	p := s.pending[idx]
	s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
	if !p.Abandoned {
		s.running++ // the parked caller resumes once we reply
	}
	s.mu.Unlock()

	if !at.IsZero() {
		s.Clock.Set(at)
	}
	rec := Record{Seq: p.Seq, AttemptSeq: p.AttemptSeq, Attempt: p.Attempt, Method: p.Method, Path: p.Path, Query: p.Query,
		Actor: p.Actor, Body: p.Body, SentAt: p.SentAt, AppliedAt: s.Clock.Now(), Delivery: d,
		IssueID: issueIDFromPath(p.Path), Abandoned: p.Abandoned,
		Hook: p.Hook, AfterOwnershipKill: p.AfterOwnershipKill}
	s.stampBefore(&rec)
	rp := s.serve(p, d, &rec)
	s.noteControlPlane(rec)
	s.mu.Lock()
	if rec.IssueID != "" && rec.Method == "POST" && strings.HasSuffix(rec.Path, "/claim") && rec.Status/100 == 2 && d != Drop {
		s.auth[rec.IssueID] = rec.Attempt
		s.claimed[rec.Attempt] = rec.IssueID
	}
	s.records = append(s.records, rec)
	s.mu.Unlock()
	if !p.Abandoned {
		p.reply <- rp
	}
	s.Quiesce()
	return rec
}

// serve applies p per d, filling rec's status and body, and returns the reply
// its caller gets.
func (s *Sim) serve(p *Pending, d Delivery, rec *Record) reply {
	if d == Drop {
		return reply{err: ErrRequestDropped}
	}
	rr := httptest.NewRecorder()
	p.req.Body = io.NopCloser(strings.NewReader(p.Body))
	s.Server.ServeHTTP(rr, p.req)
	rec.Status = rr.Code
	rec.RespBody = rr.Body.String()
	if d == ApplyLoseResponse {
		return reply{err: ErrResponseLost}
	}
	res := rr.Result()
	res.Request = p.req
	return reply{resp: res}
}

// Abandon answers the caller of pending request seq with ErrClientGaveUp
// without applying it — the caller's own deadline expired while the request
// was still on the network — and leaves the request pending, so a later
// Deliver applies it late. Waits for quiescence afterwards.
func (s *Sim) Abandon(seq int) {
	s.Quiesce()
	s.mu.Lock()
	var p *Pending
	for _, q := range s.pending {
		if q.Seq == seq {
			p = q
			break
		}
	}
	if p == nil || p.Abandoned {
		s.mu.Unlock()
		panic(fmt.Sprintf("fleetsim: cannot abandon seq=%d", seq))
	}
	p.Abandoned = true
	s.running++
	s.mu.Unlock()
	p.reply <- reply{err: ErrClientGaveUp}
	s.Quiesce()
}

// EndAttempt records that attempt's agent process is gone. Sessions it
// created that are still non-terminal when Check runs are reported as
// SessionTerminates violations.
func (s *Sim) EndAttempt(attempt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended[attempt] = true
}

// Observe records a local fact about attempt (see Observation) at the current
// fake time, stamped with the server-side ownership authority for agent.
func (s *Sim) Observe(attempt, agent, what string) Observation {
	auth := s.ownerAuthority(agent)
	o := Observation{At: s.Clock.Now(), Attempt: attempt, Agent: agent, What: what, OwnerAuthority: auth}
	s.mu.Lock()
	s.obs = append(s.obs, o)
	s.mu.Unlock()
	return o
}

// OwnerAuthority reports which attempt holds ownership authority for agent
// right now ("" if none).
func (s *Sim) OwnerAuthority(agent string) string { return s.ownerAuthority(agent) }

func (s *Sim) ownerAuthority(agent string) string {
	l, ok := s.Server.OwnershipSnapshot(agent)
	if !ok || !l.Live(s.Clock.Now()) {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.own[agent]
	if g.attempt != "" && g.fence == l.FencingToken {
		return g.attempt
	}
	return ""
}

// stampControlPlane fills the control-plane fields of rec from server state
// at apply time (called before the request is served).
func (s *Sim) stampControlPlane(rec *Record) {
	kind, id, _ := controlPlanePath(rec.Path)
	switch kind {
	case "ownership":
		rec.Agent = id
	case "sessions":
		var body struct {
			SessionID string `json:"session_id"`
			AgentID   string `json:"agent_id"`
			Status    string `json:"status"`
		}
		_ = json.Unmarshal([]byte(rec.Body), &body)
		rec.ReqStatus = body.Status
		if id == "" {
			rec.SessionID, rec.Agent = body.SessionID, body.AgentID
			break
		}
		rec.SessionID = id
		if sess, ok := s.Server.SessionSnapshot(id); ok {
			rec.Agent, rec.SessionStatusBefore = sess.AgentID, sess.Status
		}
	case "leases":
		if l, ok := s.Server.LeaseSnapshot(id); ok {
			rec.Agent, rec.SessionID = l.AgentID, l.SessionID
			if sess, ok := s.Server.SessionSnapshot(l.SessionID); ok {
				rec.SessionStatusBefore = sess.Status
			}
		}
	default:
		return
	}
	if rec.Agent != "" {
		rec.OwnerAuthorityBefore = s.ownerAuthority(rec.Agent)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.SessionAttempt = s.sessionOf[rec.SessionID]
	if kind == "leases" {
		rec.LeaseAttempt = s.leaseOf[id]
	}
}

// noteControlPlane updates attempt bookkeeping after a control-plane
// request applied.
func (s *Sim) noteControlPlane(rec Record) {
	if rec.Delivery == Drop || rec.Status/100 != 2 {
		return
	}
	kind, id, sub := controlPlanePath(rec.Path)
	var resp struct {
		LeaseID      string `json:"lease_id"`
		SessionID    string `json:"session_id"`
		FencingToken int64  `json:"fencing_token"`
	}
	_ = json.Unmarshal([]byte(rec.RespBody), &resp)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case kind == "ownership" && sub == "acquire":
		s.own[id] = ownGrant{attempt: rec.Attempt, fence: resp.FencingToken}
	case kind == "sessions" && id == "" && rec.Method == "POST":
		s.sessionOf[resp.SessionID] = rec.Attempt
	case kind == "sessions" && sub == "leases":
		s.leaseOf[resp.LeaseID] = rec.Attempt
	}
}

// controlPlanePath splits a workspace-relative control-plane path into its
// resource kind ("ownership", "sessions", "leases"), the resource ID and the
// sub-resource.
func controlPlanePath(path string) (kind, id, sub string) {
	for prefix, k := range map[string]string{
		"/agent-ownership-leases": "ownership",
		"/agent-sessions":         "sessions",
		"/agent-leases":           "leases",
	} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, "/")
		id, sub, _ = strings.Cut(rest, "/")
		return k, id, sub
	}
	return "", "", ""
}

// Match selects a pending request.
type Match func(Pending) bool

// Req matches by attempt, method and path suffix; an empty method matches
// any method.
func Req(attempt, method, pathSuffix string) Match {
	return func(p Pending) bool {
		return p.Attempt == attempt && (method == "" || p.Method == method) && strings.HasSuffix(p.Path, pathSuffix)
	}
}

// DeliverNext delivers the earliest-sent pending request matching m. It
// reports false when none matches.
func (s *Sim) DeliverNext(m Match, d Delivery, at time.Time) (Record, bool) {
	for _, p := range s.Pending() {
		if m(p) {
			return s.Deliver(p.Seq, d, at), true
		}
	}
	return Record{}, false
}

// DrainFIFO applies pending requests in send order until none remain and all
// actors have finished.
func (s *Sim) DrainFIFO() {
	for {
		ps := s.Pending()
		if len(ps) == 0 {
			return
		}
		s.Deliver(ps[0].Seq, Apply, time.Time{})
	}
}

// DrainSeeded applies pending requests chosen by a seeded PRNG until none
// remain, advancing the fake clock by step before each apply. The same seed
// and the same actors always produce the same apply order.
func (s *Sim) DrainSeeded(seed int64, step time.Duration) {
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic schedule, not security
	for {
		ps := s.Pending()
		if len(ps) == 0 {
			return
		}
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Attempt != ps[j].Attempt {
				return ps[i].Attempt < ps[j].Attempt
			}
			return ps[i].AttemptSeq < ps[j].AttemptSeq
		})
		pick := ps[rng.Intn(len(ps))]
		s.Clock.Advance(step)
		s.Deliver(pick.Seq, Apply, time.Time{})
	}
}

type transport struct {
	sim     *Sim
	attempt string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	s := t.sim
	s.mu.Lock()
	s.seq++
	s.attSeq[t.attempt]++
	p := &Pending{
		Seq:                s.seq,
		AttemptSeq:         s.attSeq[t.attempt],
		Attempt:            t.attempt,
		Method:             req.Method,
		Path:               strings.TrimPrefix(req.URL.Path, "/api/v1/"+s.Workspace),
		Query:              req.URL.RawQuery,
		Actor:              req.Header.Get("X-Actor"),
		Body:               string(body),
		SentAt:             s.Clock.Now(),
		Hook:               s.hookPhase[t.attempt],
		AfterOwnershipKill: s.killed[t.attempt],
		req:                clone,
		reply:              make(chan reply, 1),
	}
	s.pending = append(s.pending, p)
	s.running--
	s.cond.Broadcast()
	s.mu.Unlock()

	select {
	case r := <-p.reply:
		return r.resp, r.err
	case <-req.Context().Done():
		// Only reachable if a caller's real-time context expires while the
		// test holds its request; deterministic tests never do this.
		s.mu.Lock()
		for i, q := range s.pending {
			if q == p {
				s.pending = append(s.pending[:i], s.pending[i+1:]...)
				s.running++
				break
			}
		}
		s.mu.Unlock()
		return nil, req.Context().Err()
	}
}

func issueIDFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/issues/")
	if !ok || rest == "" || rest == "ready" {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	return id
}
