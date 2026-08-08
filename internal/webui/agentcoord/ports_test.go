package agentcoord

import "testing"

func TestValidateAgentNameAcceptsStoredAndLegacyNames(t *testing.T) {
	for _, name := range []string{"lead-claude-1", "ABC", "a-b_c-123", "foo.bar", "agent.one", "a"} {
		if err := ValidateAgentName(name); err != nil {
			t.Errorf("rejected valid name %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "agent one", "foo/bar", "../etc/passwd", "..", ".foo", "agent@foo!"} {
		if err := ValidateAgentName(name); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
}
