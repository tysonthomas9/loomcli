package fleetsim

import (
	"bytes"
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
	IssueID    string
	// Server state for IssueID immediately before apply, for the oracle.
	HolderBefore   string
	StatusBefore   string
	AssigneeBefore string
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

// Records returns the interposer log in apply order.
func (s *Sim) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
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
	s.running++ // the parked caller resumes once we reply
	s.mu.Unlock()

	if !at.IsZero() {
		s.Clock.Set(at)
	}
	rec := Record{Seq: p.Seq, AttemptSeq: p.AttemptSeq, Attempt: p.Attempt, Method: p.Method, Path: p.Path, Query: p.Query,
		Actor: p.Actor, Body: p.Body, SentAt: p.SentAt, AppliedAt: s.Clock.Now(), Delivery: d,
		IssueID: issueIDFromPath(p.Path)}
	if rec.IssueID != "" {
		if is, holder, ok := s.Server.Snapshot(rec.IssueID); ok {
			rec.HolderBefore, rec.StatusBefore, rec.AssigneeBefore = holder, is.Status, is.Assignee
		}
	}
	var rp reply
	if d == Drop {
		rp.err = ErrRequestDropped
	} else {
		rr := httptest.NewRecorder()
		p.req.Body = io.NopCloser(strings.NewReader(p.Body))
		s.Server.ServeHTTP(rr, p.req)
		rec.Status = rr.Code
		if d == ApplyLoseResponse {
			rp.err = ErrResponseLost
		} else {
			res := rr.Result()
			res.Request = p.req
			rp.resp = res
		}
	}
	s.mu.Lock()
	s.records = append(s.records, rec)
	s.mu.Unlock()
	p.reply <- rp
	s.Quiesce()
	return rec
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
		Seq:        s.seq,
		AttemptSeq: s.attSeq[t.attempt],
		Attempt:    t.attempt,
		Method:     req.Method,
		Path:       strings.TrimPrefix(req.URL.Path, "/api/v1/"+s.Workspace),
		Query:      req.URL.RawQuery,
		Actor:      req.Header.Get("X-Actor"),
		Body:       string(body),
		SentAt:     s.Clock.Now(),
		req:        clone,
		reply:      make(chan reply, 1),
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
