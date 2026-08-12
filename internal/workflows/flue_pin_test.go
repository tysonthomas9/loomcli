package workflows

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed FLUE_COMMIT
var pinnedFlueCommit string

// verifiedFlueCommit is the flue commit the builtin workflows are known to run
// against. It is asserted here — not to freeze the pin forever, but to make a
// bump a DELIBERATE act: upstream flue removed the `{ model: false }` agent
// config (commit 46534e97, "Remove model false configuration"), and at flue
// HEAD the root harness initializes eagerly, so every builtin workflow that
// still returns `model: false` (epic-runner, local-task-runner,
// daytona-task-runner, openshell-task-runner) fails at agent-init the moment
// the pin moves past that commit.
//
// If you are bumping FLUE_COMMIT: first give the builtin `defineAgent` stubs a
// real model string (or restructure them to not define an agent), verify the
// bundled runners still build and invoke, then update this constant in the
// same change.
const verifiedFlueCommit = "492bf47b9f3d6c379d00471523987b8fe9511f7d"

func TestFlueCommitPinIsTheVerifiedOne(t *testing.T) {
	got := strings.TrimSpace(pinnedFlueCommit)
	if got != verifiedFlueCommit {
		t.Fatalf("FLUE_COMMIT = %s, but the builtin workflows were last verified against %s.\n"+
			"Upstream removed `model: false` (flue 46534e97); all four builtin runners use it and break at agent-init past that commit.\n"+
			"Fix the builtin agent stubs first, then update verifiedFlueCommit alongside the pin.", got, verifiedFlueCommit)
	}
}
