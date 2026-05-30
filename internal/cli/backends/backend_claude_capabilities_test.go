package backends

import "testing"

// Compile-time interface satisfaction checks.
var (
	_ MetadataProvider       = (*ClaudeBackend)(nil)
	_ HealthCheckableBackend = (*ClaudeBackend)(nil)
	_ StreamingBackend       = (*ClaudeBackend)(nil)
	_ SessionAwareBackend    = (*ClaudeBackend)(nil)
)

func TestClaudeBackend_Meta(t *testing.T) {
	b := &ClaudeBackend{}
	m := b.Meta()

	if m.DisplayName != "Claude" {
		t.Errorf("DisplayName = %q, want %q", m.DisplayName, "Claude")
	}
	if m.Description != "Anthropic Claude Code CLI" {
		t.Errorf("Description = %q, want %q", m.Description, "Anthropic Claude Code CLI")
	}
	if m.URL != "https://docs.anthropic.com/en/docs/claude-code" {
		t.Errorf("URL = %q, want %q", m.URL, "https://docs.anthropic.com/en/docs/claude-code")
	}
	if m.BinaryName != "claude" {
		t.Errorf("BinaryName = %q, want %q", m.BinaryName, "claude")
	}
	// Version may or may not be set depending on whether claude is installed
}

func TestClaudeBackend_HealthCheck(t *testing.T) {
	b := &ClaudeBackend{}
	hs := b.HealthCheck()

	// We can't assert specific values for Installed/APIKeySet since they
	// depend on the test environment, but we can verify the struct is valid.
	if hs.Message == "" {
		t.Error("expected non-empty Message")
	}

	// Healthy should only be true if both Installed and APIKeySet are true
	if hs.Healthy && (!hs.Installed || !hs.APIKeySet) {
		t.Error("Healthy=true but Installed or APIKeySet is false")
	}
	if !hs.Healthy && hs.Installed && hs.APIKeySet {
		t.Error("Healthy=false but both Installed and APIKeySet are true")
	}
}

func TestClaudeBackend_LastSessionID(t *testing.T) {
	b := &ClaudeBackend{}
	ClearLastCapturedSessionID()
	t.Cleanup(ClearLastCapturedSessionID)
	if id := b.LastSessionID("/tmp"); id != "" {
		t.Errorf("LastSessionID before capture = %q, want empty string", id)
	}
	SetLastCapturedSessionID("claude-session-1")
	if id := b.LastSessionID("/tmp"); id != "claude-session-1" {
		t.Errorf("LastSessionID after capture = %q, want claude-session-1", id)
	}
}

func TestClaudeBackend_InspectCapabilities(t *testing.T) {
	b := &ClaudeBackend{}
	caps := InspectCapabilities(b)

	if !caps.HasMeta {
		t.Error("expected HasMeta=true")
	}
	if !caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=true")
	}
	if !caps.HasStreaming {
		t.Error("expected HasStreaming=true")
	}
	if !caps.HasSessions {
		t.Error("expected HasSessions=true")
	}
	if caps.HasToolControl {
		t.Error("expected HasToolControl=false")
	}
	if caps.HasConfig {
		t.Error("expected HasConfig=false")
	}
}
