//go:build sessionsreplay

package hooks

import (
	"path/filepath"
	"testing"
)

// #690: Claude keeps subagents under a directory named for the parent sidecar.
func TestSessionsReplay690SubagentPath(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent.jsonl")
	want := filepath.Join(parent[:len(parent)-len(".jsonl")], "subagents", "agent-child.jsonl")
	if got := deriveSubagentPath(parent, "child"); got != want {
		t.Fatalf("#690: subagent path = %q, want %q", got, want)
	}
}
