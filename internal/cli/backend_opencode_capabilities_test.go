//go:build ignore

package cli

import "testing"

// Compile-time interface satisfaction checks.
var (
	_ MetadataProvider       = (*OpenCodeBackend)(nil)
	_ HealthCheckableBackend = (*OpenCodeBackend)(nil)
)

func TestOpenCodeBackend_Meta(t *testing.T) {
	b := &OpenCodeBackend{}
	m := b.Meta()

	if m.DisplayName != "OpenCode" {
		t.Errorf("DisplayName = %q, want %q", m.DisplayName, "OpenCode")
	}
	if m.Description != "OpenCode CLI" {
		t.Errorf("Description = %q, want %q", m.Description, "OpenCode CLI")
	}
	if m.URL != "https://github.com/opencode-ai/opencode" {
		t.Errorf("URL = %q, want %q", m.URL, "https://github.com/opencode-ai/opencode")
	}
	if m.BinaryName != "opencode" {
		t.Errorf("BinaryName = %q, want %q", m.BinaryName, "opencode")
	}
}

func TestOpenCodeBackend_HealthCheck(t *testing.T) {
	b := &OpenCodeBackend{}
	hs := b.HealthCheck()

	if hs.Message == "" {
		t.Error("expected non-empty Message")
	}

	// OpenCode doesn't require an API key, so Healthy = Installed
	if hs.Healthy != hs.Installed {
		t.Errorf("Healthy=%v but Installed=%v (should match for OpenCode)", hs.Healthy, hs.Installed)
	}
	// APIKeySet should always be false for OpenCode
	if hs.APIKeySet {
		t.Error("expected APIKeySet=false for OpenCode")
	}
}

func TestOpenCodeBackend_InspectCapabilities(t *testing.T) {
	b := &OpenCodeBackend{}
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
