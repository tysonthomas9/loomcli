package pty

import "testing"

func TestMatchesAgentSessionSupportsCanonicalDottedNames(t *testing.T) {
	for _, test := range []struct {
		name    string
		session string
		agent   string
		want    bool
	}{
		{name: "dotted task agent", session: "loom-workspac-task-agent.one-42", agent: "agent.one", want: true},
		{name: "dotted plan agent", session: "loom-workspac-plan-agent.one-42", agent: "agent.one", want: true},
		{name: "different agent", session: "loom-workspac-task-agent.two-42", agent: "agent.one"},
		{name: "suffix is not identity", session: "loom-workspac-task-other-agent.one-42", agent: "agent.one"},
		{name: "unknown mode", session: "loom-workspac-worker-agent.one-42", agent: "agent.one"},
		{name: "target injection", session: "loom-workspac-task-agent.one:0-42", agent: "agent.one"},
		{name: "invalid requested agent", session: "loom-workspac-task-agent:one-42", agent: "agent:one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesAgentSession(test.session, "workspac", test.agent); got != test.want {
				t.Fatalf("matchesAgentSession(%q, %q) = %v, want %v", test.session, test.agent, got, test.want)
			}
		})
	}
}
