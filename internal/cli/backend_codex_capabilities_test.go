//go:build ignore

package cli

import "testing"

// Compile-time interface satisfaction checks.
var (
	_ MetadataProvider       = (*CodexBackend)(nil)
	_ HealthCheckableBackend = (*CodexBackend)(nil)
)

func TestCodexBackend_Meta(t *testing.T) {
	b := &CodexBackend{}
	m := b.Meta()

	if m.DisplayName != "Codex" {
		t.Errorf("DisplayName = %q, want %q", m.DisplayName, "Codex")
	}
	if m.Description != "OpenAI Codex CLI" {
		t.Errorf("Description = %q, want %q", m.Description, "OpenAI Codex CLI")
	}
	if m.URL != "https://github.com/openai/codex" {
		t.Errorf("URL = %q, want %q", m.URL, "https://github.com/openai/codex")
	}
	if m.BinaryName != "codex" {
		t.Errorf("BinaryName = %q, want %q", m.BinaryName, "codex")
	}
}

func TestCodexBackend_HealthCheck(t *testing.T) {
	b := &CodexBackend{}
	hs := b.HealthCheck()

	if hs.Message == "" {
		t.Error("expected non-empty Message")
	}
	if hs.Healthy && (!hs.Installed || !hs.APIKeySet) {
		t.Error("Healthy=true but Installed or APIKeySet is false")
	}
	if !hs.Healthy && hs.Installed && hs.APIKeySet {
		t.Error("Healthy=false but both Installed and APIKeySet are true")
	}
}

func TestCodexBackend_InspectCapabilities(t *testing.T) {
	b := &CodexBackend{}
	caps := InspectCapabilities(b)

	if !caps.HasMeta {
		t.Error("expected HasMeta=true")
	}
	if !caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=true")
	}
	if caps.HasStreaming {
		t.Error("expected HasStreaming=false")
	}
	if caps.HasSessions {
		t.Error("expected HasSessions=false")
	}
	if caps.HasToolControl {
		t.Error("expected HasToolControl=false")
	}
	if caps.HasConfig {
		t.Error("expected HasConfig=false")
	}
}
