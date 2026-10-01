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

// Control-plane invariants. Ownership authority is per attempt as well (the
// harness analog of the ownership fencing token): the attempt whose ownership
// acquire most recently succeeded holds it while the server's lease is
// active, unexpired and still carries that acquire's fence
// (Record.OwnerAuthorityBefore, Observation.OwnerAuthority).
//
// NoForeignOwnershipRelease (S3, a_fail_token): an ownership release from one
// attempt is accepted while a different attempt holds ownership authority.
//
// NoOwnershipOverlap (S2, S3, S13; SingleLiveProcess analog): an attempt is
// observed still acting as owner — its supervisor believes it owns, or its
// agent process runs — while a different attempt holds ownership authority.
//
// NoSupersededSessionWrite (S7 srun, S12 d_fail_unbound_session_lease): a
// non-terminal session write or a session-lease renewal from one attempt is
// accepted while a different attempt holds ownership authority.
//
// NoStaleCompleted (S4, S7; stage-d invariant of the same name): a session is
// finalized as completed by an attempt that does not hold ownership
// authority at apply time.
//
// TerminalSessionOnce (S7, c_fail_no_guard): an accepted session write
// changes the status of a session that was already terminal.
//
// LiveOwnerKeepsSessionLease (S12, d_fail_ipc_only_renewal): the attempt that
// holds ownership authority has its own session-lease renewal refused.
//
// SessionTerminates (S4, gap L1): a session created by an attempt whose
// process has ended (Sim.EndAttempt) is still non-terminal at Check time.
const (
	InvNoForeignOwnershipRelease  = "NoForeignOwnershipRelease"
	InvNoOwnershipOverlap         = "NoOwnershipOverlap"
	InvNoSupersededSessionWrite   = "NoSupersededSessionWrite"
	InvNoStaleCompleted           = "NoStaleCompleted"
	InvTerminalSessionOnce        = "TerminalSessionOnce"
	InvLiveOwnerKeepsSessionLease = "LiveOwnerKeepsSessionLease"
	InvSessionTerminates          = "SessionTerminates"
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
		if r.Delivery != Drop && r.IssueID == "" {
			rep.Violations = append(rep.Violations, checkControlPlane(r)...)
			continue
		}
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
	rep.Violations = append(rep.Violations, s.checkObservations()...)
	rep.Violations = append(rep.Violations, s.checkSessionsTerminate()...)
	return rep
}

func cpViolation(r Record, inv, detail string) Violation {
	return Violation{Invariant: inv, Seq: r.Seq, Attempt: r.Attempt,
		Detail: fmt.Sprintf("%s %s (%d) %s", r.Method, r.Path, r.Status, detail)}
}

// otherOwner reports whether a different attempt held ownership at apply.
func otherOwner(r Record) bool {
	return r.OwnerAuthorityBefore != "" && r.OwnerAuthorityBefore != r.Attempt
}

func checkControlPlane(r Record) []Violation {
	kind, _, sub := controlPlanePath(r.Path)
	ok := r.Status/100 == 2
	switch {
	case kind == "ownership" && sub == "release" && ok && otherOwner(r):
		return []Violation{cpViolation(r, InvNoForeignOwnershipRelease,
			fmt.Sprintf("ended the lease while attempt %q holds ownership of %s", r.OwnerAuthorityBefore, r.Agent))}
	case kind == "sessions" && r.Method == "PATCH" && ok:
		return checkSessionWrite(r)
	case kind == "leases" && sub == "heartbeat":
		return checkLeaseRenewal(r, ok)
	}
	return nil
}

func checkSessionWrite(r Record) []Violation {
	var out []Violation
	if TerminalSessionStatus(r.SessionStatusBefore) && r.ReqStatus != "" && r.ReqStatus != r.SessionStatusBefore {
		out = append(out, cpViolation(r, InvTerminalSessionOnce,
			fmt.Sprintf("moved terminal session %s from %s to %s", r.SessionID, r.SessionStatusBefore, r.ReqStatus)))
	}
	switch {
	case r.ReqStatus == "completed" && r.OwnerAuthorityBefore != r.Attempt:
		out = append(out, cpViolation(r, InvNoStaleCompleted,
			fmt.Sprintf("finalized session %s as completed while ownership authority is %q", r.SessionID, r.OwnerAuthorityBefore)))
	case !TerminalSessionStatus(r.ReqStatus) && otherOwner(r):
		out = append(out, cpViolation(r, InvNoSupersededSessionWrite,
			fmt.Sprintf("wrote session %s (status %q) while attempt %q holds ownership", r.SessionID, r.ReqStatus, r.OwnerAuthorityBefore)))
	}
	return out
}

func checkLeaseRenewal(r Record, ok bool) []Violation {
	switch {
	case ok && otherOwner(r):
		return []Violation{cpViolation(r, InvNoSupersededSessionWrite,
			fmt.Sprintf("renewed session lease of %s while attempt %q holds ownership", r.SessionID, r.OwnerAuthorityBefore))}
	case !ok && r.OwnerAuthorityBefore == r.Attempt && r.LeaseAttempt == r.Attempt:
		return []Violation{cpViolation(r, InvLiveOwnerKeepsSessionLease,
			fmt.Sprintf("owner's session lease for %s refused", r.SessionID))}
	}
	return nil
}

func (s *Sim) checkObservations() []Violation {
	s.mu.Lock()
	obs := append([]Observation(nil), s.obs...)
	s.mu.Unlock()
	var out []Violation
	for _, o := range obs {
		if o.OwnerAuthority != "" && o.OwnerAuthority != o.Attempt {
			out = append(out, Violation{Invariant: InvNoOwnershipOverlap, Attempt: o.Attempt,
				Detail: fmt.Sprintf("at %s %s (%s) while attempt %q holds ownership of %s",
					o.At.UTC().Format("15:04:05.000"), o.What, o.Attempt, o.OwnerAuthority, o.Agent)})
		}
	}
	return out
}

func (s *Sim) checkSessionsTerminate() []Violation {
	s.mu.Lock()
	created := map[string]string{}
	for sid, a := range s.sessionOf {
		if s.ended[a] {
			created[sid] = a
		}
	}
	s.mu.Unlock()
	var out []Violation
	for _, sid := range sortedKeys(created) {
		if sess, ok := s.Server.SessionSnapshot(sid); ok && !TerminalSessionStatus(sess.Status) {
			out = append(out, Violation{Invariant: InvSessionTerminates, Attempt: created[sid],
				Detail: fmt.Sprintf("session %s still %s after its attempt ended", sid, sess.Status)})
		}
	}
	return out
}
