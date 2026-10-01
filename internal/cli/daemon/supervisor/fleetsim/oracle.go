package fleetsim

import (
	"fmt"
	"strings"
)

// Violation is one invariant breach found by the oracle.
type Violation struct {
	Invariant string
	Seq       int
	Attempt   string
	Detail    string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s seq=%d attempt=%s: %s", v.Invariant, v.Seq, v.Attempt, v.Detail)
}

// ActorSplit records a write whose wire X-Actor differs from the claim actor of
// the attempt that issued it. It is an observation, not a violation: it is the
// reason FleetDB cannot attribute such writes to an attempt.
type ActorSplit struct {
	Seq        int
	Attempt    string
	ClaimActor string
	WireActor  string
	Method     string
	Path       string
}

// Report is the oracle's verdict over one run.
type Report struct {
	Violations  []Violation
	ActorSplits []ActorSplit
}

// Has reports whether inv was violated.
func (r Report) Has(inv string) bool {
	for _, v := range r.Violations {
		if v.Invariant == inv {
			return true
		}
	}
	return false
}

// Invariant names. Authority is per attempt (the harness analog of the
// model's fencing token): the attempt whose claim most recently succeeded
// holds it while its claim actor holds the live lock.
//
// NoSupersededWrite mirrors the TLA+ invariant of the same name in
// test/formal/daemon-attempt: a workflow write accepted from an attempt while
// a different attempt holds authority.
//
// NoWriteWithoutAuthority is enforcement-map row S6 (a_fail_fence_eq): a
// workflow write accepted from an attempt while nobody holds authority, e.g.
// after its lock was released or expired and before any reacquire.
const (
	InvNoSupersededWrite       = "NoSupersededWrite"
	InvNoWriteWithoutAuthority = "NoWriteWithoutAuthority"
)

// workflowWrite reports whether the request mutates issue workflow state on
// behalf of the work itself. Claims and lock releases are ownership
// operations and are judged by the server's own lock checks.
func workflowWrite(r Record) bool {
	if r.Method == "PATCH" {
		return r.IssueID != ""
	}
	if r.Method != "POST" || r.IssueID == "" {
		return false
	}
	for _, s := range []string{"/assign", "/close", "/release", "/reopen", "/defer", "/undefer"} {
		if strings.HasSuffix(r.Path, s) {
			return true
		}
	}
	return false
}

// Check evaluates the trace. Authority is per attempt: at apply time it
// belongs to the attempt whose claim most recently succeeded, while that
// attempt's claim actor still holds the live lock (Record.AuthorityBefore). An
// accepted workflow write from any other attempt is NoSupersededWrite; one
// applied while nobody holds authority is NoWriteWithoutAuthority. A write is
// attributed to the attempt that sent it (the interposer knows), never to its
// X-Actor header; attempts never bound with BindAttempt are not judged.
func (s *Sim) Check() Report {
	var rep Report
	for _, r := range s.Records() {
		if r.Delivery == Drop || !workflowWrite(r) {
			continue
		}
		claim := s.ClaimActor(r.Attempt)
		if claim != "" && r.Actor != claim {
			rep.ActorSplits = append(rep.ActorSplits, ActorSplit{Seq: r.Seq, Attempt: r.Attempt,
				ClaimActor: claim, WireActor: r.Actor, Method: r.Method, Path: r.Path})
		}
		if r.Status < 200 || r.Status >= 300 || claim == "" {
			continue
		}
		switch {
		case r.AuthorityBefore == r.Attempt:
		case r.AuthorityBefore != "":
			rep.Violations = append(rep.Violations, Violation{
				Invariant: InvNoSupersededWrite, Seq: r.Seq, Attempt: r.Attempt,
				Detail: fmt.Sprintf("%s %s accepted (%d) as X-Actor %q while attempt %q (claims as %q) holds authority",
					r.Method, r.Path, r.Status, r.Actor, r.AuthorityBefore, s.ClaimActor(r.AuthorityBefore)),
			})
		default:
			rep.Violations = append(rep.Violations, Violation{
				Invariant: InvNoWriteWithoutAuthority, Seq: r.Seq, Attempt: r.Attempt,
				Detail: fmt.Sprintf("%s %s accepted (%d) as X-Actor %q with no live authority (lock holder %q, status %s)",
					r.Method, r.Path, r.Status, r.Actor, r.HolderBefore, r.StatusBefore),
			})
		}
	}
	return rep
}
