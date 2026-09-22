package agent

import (
	"github.com/tysonthomas9/loomcli/internal/harnessprofile"
)

// enforceAgentProfile settles this process's per-agent harness profile roots
// and credentials before anything can reach a backend.
//
// The invariant it exists for: a `loom agent` run must never reach a backend on
// ambient auth. Until this call existed, a standalone invocation resolved no
// profile root and exported no per-profile credential, so it ran the harness
// against the OPERATOR's ~/.claude and the operator's own OAuth pair — and
// because refresh tokens are one-time-use, the operator's next /login could
// invalidate whichever profile last shared that pair. That is the exact sharing
// per-agent profiles were built to end. The supervisor closes it at spawn
// (supervisor.AppendProfileEnv) and `loom lead` closes it at startup; this is
// the third and last entry point.
//
// The policy is harnessprofile's, not this package's: there is one
// implementation of resolve-verify-export and no caller may grow a second,
// weaker copy.
func enforceAgentProfile(runtimeDir, agentName string) error {
	return harnessprofile.Enforce(runtimeDir, agentName)
}
