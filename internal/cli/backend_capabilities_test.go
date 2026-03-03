package cli

import (
	"context"
	"io"
	"strings"
	"testing"
)

// fullCapabilityBackend implements Backend and all 6 optional interfaces.
type fullCapabilityBackend struct {
	mockBackend
}

func (f *fullCapabilityBackend) InvokeStreaming(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("streamed")), nil
}

func (f *fullCapabilityBackend) ContinueSession(_, _, _ string) error { return nil }
func (f *fullCapabilityBackend) LastSessionID(_ string) string        { return "sess-1" }

func (f *fullCapabilityBackend) SetAllowedTools(_ []string) {}
func (f *fullCapabilityBackend) SetDeniedTools(_ []string)  {}

func (f *fullCapabilityBackend) HealthCheck() HealthStatus {
	return HealthStatus{Healthy: true, Installed: true, Version: "1.0", APIKeySet: true}
}

func (f *fullCapabilityBackend) Options() []BackendOption {
	return []BackendOption{{Key: "model", Description: "Model name", Default: "gpt-4", CurrentValue: "gpt-4"}}
}
func (f *fullCapabilityBackend) SetOption(_, _ string) error        { return nil }
func (f *fullCapabilityBackend) GetOption(_ string) (string, error) { return "gpt-4", nil }

func (f *fullCapabilityBackend) Meta() BackendMeta {
	return BackendMeta{DisplayName: "Full", Version: "1.0", Description: "full backend", URL: "https://example.com", BinaryName: "full"}
}

// partialCapabilityBackend implements Backend + MetadataProvider + HealthCheckableBackend only.
type partialCapabilityBackend struct {
	mockBackend
}

func (p *partialCapabilityBackend) Meta() BackendMeta {
	return BackendMeta{DisplayName: "Partial", Version: "0.1"}
}

func (p *partialCapabilityBackend) HealthCheck() HealthStatus {
	return HealthStatus{Healthy: false, Message: "not ready"}
}

func TestInspectCapabilities_NoOptionalInterfaces(t *testing.T) {
	b := &mockBackend{name: "plain"}
	caps := InspectCapabilities(b)

	if caps.HasStreaming {
		t.Error("expected HasStreaming=false")
	}
	if caps.HasSessions {
		t.Error("expected HasSessions=false")
	}
	if caps.HasToolControl {
		t.Error("expected HasToolControl=false")
	}
	if caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=false")
	}
	if caps.HasConfig {
		t.Error("expected HasConfig=false")
	}
	if caps.HasMeta {
		t.Error("expected HasMeta=false")
	}

	if caps.Streaming != nil {
		t.Error("expected Streaming=nil")
	}
	if caps.Sessions != nil {
		t.Error("expected Sessions=nil")
	}
	if caps.Tools != nil {
		t.Error("expected Tools=nil")
	}
	if caps.Health != nil {
		t.Error("expected Health=nil")
	}
	if caps.Config != nil {
		t.Error("expected Config=nil")
	}
	if caps.Meta != nil {
		t.Error("expected Meta=nil")
	}
}

func TestInspectCapabilities_AllInterfaces(t *testing.T) {
	b := &fullCapabilityBackend{mockBackend{name: "full"}}
	caps := InspectCapabilities(b)

	if !caps.HasStreaming {
		t.Error("expected HasStreaming=true")
	}
	if !caps.HasSessions {
		t.Error("expected HasSessions=true")
	}
	if !caps.HasToolControl {
		t.Error("expected HasToolControl=true")
	}
	if !caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=true")
	}
	if !caps.HasConfig {
		t.Error("expected HasConfig=true")
	}
	if !caps.HasMeta {
		t.Error("expected HasMeta=true")
	}

	if caps.Streaming == nil {
		t.Error("expected Streaming!=nil")
	}
	if caps.Sessions == nil {
		t.Error("expected Sessions!=nil")
	}
	if caps.Tools == nil {
		t.Error("expected Tools!=nil")
	}
	if caps.Health == nil {
		t.Error("expected Health!=nil")
	}
	if caps.Config == nil {
		t.Error("expected Config!=nil")
	}
	if caps.Meta == nil {
		t.Error("expected Meta!=nil")
	}
}

func TestInspectCapabilities_PartialInterfaces(t *testing.T) {
	b := &partialCapabilityBackend{mockBackend{name: "partial"}}
	caps := InspectCapabilities(b)

	// Should have only MetadataProvider and HealthCheckableBackend
	if caps.HasStreaming {
		t.Error("expected HasStreaming=false")
	}
	if caps.HasSessions {
		t.Error("expected HasSessions=false")
	}
	if caps.HasToolControl {
		t.Error("expected HasToolControl=false")
	}
	if !caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=true")
	}
	if caps.HasConfig {
		t.Error("expected HasConfig=false")
	}
	if !caps.HasMeta {
		t.Error("expected HasMeta=true")
	}

	if caps.Health == nil {
		t.Error("expected Health!=nil")
	}
	if caps.Meta == nil {
		t.Error("expected Meta!=nil")
	}
}

func TestInspectCapabilities_NilBackend(t *testing.T) {
	caps := InspectCapabilities(nil)

	if caps.HasStreaming || caps.HasSessions || caps.HasToolControl ||
		caps.HasHealthCheck || caps.HasConfig || caps.HasMeta {
		t.Error("expected all Has* flags to be false for nil backend")
	}
	if caps.Streaming != nil || caps.Sessions != nil || caps.Tools != nil ||
		caps.Health != nil || caps.Config != nil || caps.Meta != nil {
		t.Error("expected all typed fields to be nil for nil backend")
	}
}

func TestBackendCapabilities_ZeroValue(t *testing.T) {
	var caps BackendCapabilities

	if caps.HasStreaming || caps.HasSessions || caps.HasToolControl ||
		caps.HasHealthCheck || caps.HasConfig || caps.HasMeta {
		t.Error("expected all Has* flags to be false on zero value")
	}
	if caps.Streaming != nil || caps.Sessions != nil || caps.Tools != nil ||
		caps.Health != nil || caps.Config != nil || caps.Meta != nil {
		t.Error("expected all typed fields to be nil on zero value")
	}
}

func TestHealthStatus_Fields(t *testing.T) {
	var hs HealthStatus
	if hs.Healthy || hs.Installed || hs.APIKeySet {
		t.Error("expected zero-value booleans to be false")
	}
	if hs.Version != "" || hs.Message != "" {
		t.Error("expected zero-value strings to be empty")
	}

	hs = HealthStatus{
		Healthy:   true,
		Installed: true,
		Version:   "2.0",
		APIKeySet: true,
		Message:   "all good",
	}
	if !hs.Healthy || !hs.Installed || !hs.APIKeySet {
		t.Error("expected set booleans to be true")
	}
	if hs.Version != "2.0" || hs.Message != "all good" {
		t.Error("expected set strings to match")
	}
}

func TestBackendMeta_Fields(t *testing.T) {
	var bm BackendMeta
	if bm.DisplayName != "" || bm.Version != "" || bm.Description != "" ||
		bm.URL != "" || bm.BinaryName != "" {
		t.Error("expected zero-value strings to be empty")
	}

	bm = BackendMeta{
		DisplayName: "Claude",
		Version:     "3.5",
		Description: "Anthropic Claude",
		URL:         "https://claude.ai",
		BinaryName:  "claude",
	}
	if bm.DisplayName != "Claude" || bm.Version != "3.5" ||
		bm.Description != "Anthropic Claude" || bm.URL != "https://claude.ai" ||
		bm.BinaryName != "claude" {
		t.Error("expected set fields to match")
	}
}

func TestBackendOption_Fields(t *testing.T) {
	var bo BackendOption
	if bo.Key != "" || bo.Description != "" || bo.Default != "" || bo.CurrentValue != "" {
		t.Error("expected zero-value strings to be empty")
	}

	bo = BackendOption{
		Key:          "model",
		Description:  "Model to use",
		Default:      "gpt-4",
		CurrentValue: "gpt-4o",
	}
	if bo.Key != "model" || bo.Description != "Model to use" ||
		bo.Default != "gpt-4" || bo.CurrentValue != "gpt-4o" {
		t.Error("expected set fields to match")
	}
}
