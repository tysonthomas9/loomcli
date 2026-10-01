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

// Invariant names. NoSupersededWrite mirrors the TLA+ invariant of the same
// name in test/formal/daemon-attempt (an issue write accepted from an attempt
// that is no longer the claim owner).
const (
	InvNoSupersededWrite = "NoSupersededWrite"
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

// Check evaluates the trace. Ownership at apply time is the live claim-lock
// holder, or, when no lock is live, the assignee of an in_progress issue. A
// write is attributed to the attempt that sent it (the interposer knows),
// never to its X-Actor header.
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
		owner := r.HolderBefore
		if owner == "" && r.StatusBefore == "in_progress" {
			owner = r.AssigneeBefore
		}
		if owner != "" && owner != claim {
			rep.Violations = append(rep.Violations, Violation{
				Invariant: InvNoSupersededWrite, Seq: r.Seq, Attempt: r.Attempt,
				Detail: fmt.Sprintf("%s %s accepted (%d) as X-Actor %q while claim owner is %q (attempt claims as %q)",
					r.Method, r.Path, r.Status, r.Actor, owner, claim),
			})
		}
	}
	return rep
}
