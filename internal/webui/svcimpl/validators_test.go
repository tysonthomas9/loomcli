package svcimpl

import "testing"

// TestReadAgentNameValidatorAcceptsStorableNames guards the cross-endpoint
// consistency fix (Codex P2): the read-path validator (validateAgentName, used
// by the file service; the diff and terminal handlers use the same
// service.IsValidAgentName) must accept every name the create/store path can
// produce — including dotted fleet-db names — while still rejecting path-unsafe
// input and without regressing legacy names (e.g. uppercase).
func TestReadAgentNameValidatorAcceptsStorableNames(t *testing.T) {
	accept := []string{"lead-claude-1", "ABC", "a-b_c-123", "foo.bar", "agent.one", "a"}
	for _, name := range accept {
		if err := validateAgentName(name); err != nil {
			t.Errorf("read validator rejected valid name %q: %v", name, err)
		}
	}
	reject := []string{"", "agent one", "foo/bar", "../etc/passwd", "..", ".foo", "agent@foo!"}
	for _, name := range reject {
		if err := validateAgentName(name); err == nil {
			t.Errorf("read validator accepted unsafe name %q", name)
		}
	}
}

// TestStoredAgentNameValidatorIsCanonical keeps the create/store path strict:
// the lowercase fleet-db charset only, so uppercase and boundary punctuation
// are rejected before persistence.
func TestStoredAgentNameValidatorIsCanonical(t *testing.T) {
	accept := []string{"foo.bar", "lead-claude-1", "a"}
	for _, name := range accept {
		if err := validateStoredAgentName(name); err != nil {
			t.Errorf("store validator rejected valid name %q: %v", name, err)
		}
	}
	reject := []string{"", "ABC", ".foo", "foo.", "..", "foo/bar", "agent one"}
	for _, name := range reject {
		if err := validateStoredAgentName(name); err == nil {
			t.Errorf("store validator accepted invalid name %q", name)
		}
	}
}
