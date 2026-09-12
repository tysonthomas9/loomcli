package daemon

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// statusStateStaleThreshold is how old daemon-agents.json may be, relative to
// now, before its contents stop counting as a description of the running
// daemon. The state updater rewrites the file every 5s, so 30s leaves ample
// headroom. Same number and same rationale as the doctor package's
// stateFileStaleThreshold — deliberately not a second opinion.
const statusStateStaleThreshold = 30 * time.Second

// agentCountUnknown marks an agent count that could not be established. It is
// distinct from a genuine, trusted zero, which is what `Agents: 0` claims.
const agentCountUnknown = -1

// daemonStatusInputs is everything `loom daemon status` gathered about one
// daemon target before deciding what it is willing to assert. Collected by the
// caller (which does the I/O) so the decision itself stays pure and testable.
type daemonStatusInputs struct {
	// RT is the detected daemon: the single source of truth for which daemon
	// is being described. Every path below was derived from RT.Dir.
	RT cli.DaemonRuntimeInfo
	// State is the parsed daemon-agents.json, or nil when it is missing or
	// unparseable.
	State *DaemonState
	// StatePath is the file State was read from, named in warnings so the
	// reader can go look at what was distrusted.
	StatePath string
	// StateMTime is StatePath's modification time; zero when unavailable, in
	// which case freshness is not evaluated.
	StateMTime time.Time
	// LiveCount is the daemon's own answer over the control socket, or
	// agentCountUnknown when the socket did not answer.
	LiveCount int
	// Now anchors the freshness comparison. The repo has no fake clock, so it
	// is passed in explicitly (as evaluateDaemonStuck does).
	Now time.Time
}

// daemonStatusView is the header block of `loom daemon status`, resolved from
// the detected daemon and whatever sidecar evidence proved trustworthy.
//
// The invariant this type exists to enforce: a number is printed only when it
// carries the identity of the daemon that was actually detected. Everything
// else renders as "unknown", with a warning naming what was distrusted.
type daemonStatusView struct {
	PID        int
	Source     string
	Dir        string
	StartedAt  time.Time // zero => unknown
	AgentCount int       // agentCountUnknown => unknown
	Trusted    bool      // state file matched the detected daemon and is fresh
	Warnings   []string
}

// buildDaemonStatusView decides what the status header may assert.
//
// The state file is trusted only when it carries the detected daemon's PID and
// is being actively maintained. Untrusted metadata never degrades to a
// plausible-looking zero: the agent count falls back to the live socket, and
// then to "unknown".
func buildDaemonStatusView(in daemonStatusInputs) daemonStatusView {
	v := daemonStatusView{
		PID:        in.RT.PID,
		Source:     in.RT.Source,
		Dir:        in.RT.Dir,
		StartedAt:  in.RT.StartedAt,
		AgentCount: agentCountUnknown,
	}

	v.Trusted, v.Warnings = stateFileTrust(in)

	if v.Trusted {
		// The state file describes this daemon, so its own start time is the
		// most precise one available; fall back to the detection evidence when
		// the record predates the field.
		if !in.State.StartedAt.IsZero() {
			v.StartedAt = in.State.StartedAt
		}
		v.AgentCount = len(in.State.Agents)
		return v
	}

	// Untrusted: prefer the daemon's live answer, else admit we do not know.
	if in.LiveCount >= 0 {
		v.AgentCount = in.LiveCount
	}
	return v
}

// stateFileTrust reports whether the state file may be believed, along with
// the warnings explaining any refusal. Trust requires three things: the file
// exists, it names the PID we detected, and it is still being written.
func stateFileTrust(in daemonStatusInputs) (bool, []string) {
	if in.State == nil {
		// Absence is not suspicious on its own — the daemon may have just
		// started, or the file may live elsewhere. Nothing to warn about.
		return false, nil
	}

	if in.RT.PID <= 0 {
		// Liveness was proved without an identity (lock held, contents
		// unreadable), so there is nothing to match the file against.
		return false, []string{fmt.Sprintf(
			"daemon PID is unknown, so the state file at %s cannot be verified as belonging to it",
			in.StatePath)}
	}

	if in.State.PID != in.RT.PID {
		return false, []string{fmt.Sprintf(
			"state file at %s belongs to PID %d, daemon is PID %d (ignoring its agent list)",
			in.StatePath, in.State.PID, in.RT.PID)}
	}

	if !in.StateMTime.IsZero() && in.Now.Sub(in.StateMTime) > statusStateStaleThreshold {
		return false, []string{fmt.Sprintf(
			"state file at %s was last written %s and is no longer being maintained (ignoring its agent list)",
			in.StatePath, in.StateMTime.Format(time.RFC3339))}
	}

	return true, nil
}

// HeaderLines renders the header block, one string per line, in print order.
// It never formats a zero time or reports an unknown count as zero.
func (v daemonStatusView) HeaderLines() []string {
	lines := []string{fmt.Sprintf("Daemon: running (PID %d)", v.PID)}

	// The workspace lock is the case where the daemon being described is not
	// the one belonging to the caller's directory. Say so, and say where.
	if v.Source == "workspace-lock" {
		lines = append(lines, fmt.Sprintf("Source: %s (%s)", v.Source, v.Dir))
	}

	if v.StartedAt.IsZero() {
		lines = append(lines, "Started: unknown")
	} else {
		lines = append(lines, fmt.Sprintf("Started: %s", v.StartedAt.Format(time.RFC3339)))
	}

	if v.AgentCount == agentCountUnknown {
		lines = append(lines, "Agents: unknown")
	} else {
		lines = append(lines, fmt.Sprintf("Agents: %d", v.AgentCount))
	}

	for _, w := range v.Warnings {
		lines = append(lines, "  warning: "+w)
	}

	return lines
}
