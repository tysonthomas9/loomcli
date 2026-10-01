package fleetsim

import "fmt"

// Completion-hook invariants (enforcement-map row S11). Both are judged per
// attempt — the interposer knows which attempt sent each request — and only
// over requests the attempt sent while BeginHooks was in effect.
//
// HookTargetsOwnClaim: an accepted hook write lands on an issue other than
// the one the attempt's own claim record names (the issue of its last
// successful claim). Today the supervisor takes the hook task ID from the
// agent-writable lock file, so an agent can redirect its hooks.
//
// NoHookAfterOwnershipKill: an accepted hook write sent after the attempt's
// ownership kill was recorded (OwnershipKilled). The kill means the run did
// not conclude under this daemon's ownership; a hook must not certify it.
const (
	InvHookTargetsOwnClaim      = "HookTargetsOwnClaim"
	InvNoHookAfterOwnershipKill = "NoHookAfterOwnershipKill"
)

// BeginHooks marks every request attempt sends from now on as a completion
// hook write, until EndHooks. Call it while no actor of attempt is running.
func (s *Sim) BeginHooks(attempt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hookPhase[attempt] = true
}

// EndHooks ends attempt's completion-hook phase.
func (s *Sim) EndHooks(attempt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.hookPhase, attempt)
}

// OwnershipKilled records that attempt's supervisor killed its agent for
// lost ownership. It is a local fact (nothing crosses the wire); requests
// attempt sends afterwards carry Record.AfterOwnershipKill.
func (s *Sim) OwnershipKilled(attempt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killed[attempt] = true
}

// hookWrite reports whether r is an accepted mutation sent during the
// sending attempt's completion-hook phase.
func hookWrite(r Record) bool {
	return r.Hook && r.Delivery != Drop && r.IssueID != "" && r.Method != "GET" && r.Status/100 == 2
}

func (s *Sim) checkHookWrites() []Violation {
	var out []Violation
	for _, r := range s.Records() {
		if !hookWrite(r) {
			continue
		}
		if r.IssueID != r.ClaimedBefore {
			out = append(out, Violation{Invariant: InvHookTargetsOwnClaim, Seq: r.Seq, Attempt: r.Attempt,
				Detail: fmt.Sprintf("hook %s %s accepted (%d) but the attempt's claim record names %q",
					r.Method, r.Path, r.Status, r.ClaimedBefore)})
		}
		if r.AfterOwnershipKill {
			out = append(out, Violation{Invariant: InvNoHookAfterOwnershipKill, Seq: r.Seq, Attempt: r.Attempt,
				Detail: fmt.Sprintf("hook %s %s accepted (%d) after the attempt's ownership kill",
					r.Method, r.Path, r.Status)})
		}
	}
	return out
}
