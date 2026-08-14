package agents

import "testing"

func TestAgentIdentifierContracts(t *testing.T) {
	for _, valid := range []string{"lead-claude-1", "ABC", "agent.one", "a"} {
		if !ValidAgentIdentifier(valid) {
			t.Errorf("ValidAgentIdentifier(%q) = false", valid)
		}
	}
	for _, invalid := range []string{"", ".", "..", ".agent", "agent.", "agent/one", "agent:one"} {
		if ValidAgentIdentifier(invalid) {
			t.Errorf("ValidAgentIdentifier(%q) = true", invalid)
		}
	}
	if !ValidStoredAgentName("agent.one") || ValidStoredAgentName("Agent.One") {
		t.Fatal("stored Agent name contract did not preserve canonical lowercase policy")
	}
}
