package backends

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// writeProbeScript drops an executable /bin/sh fixture in a temp dir and
// returns its path, for use as a fake "<binary> --version" target.
func writeProbeScript(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("probe fixtures are /bin/sh scripts")
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write probe fixture: %v", err)
	}
	return path
}

// TestDetectBinaryVersionGivesUpOnAHungBinary pins the bound that makes
// `loom workspace ops ensure-runtime` interruptible. Nothing between the
// ops deadline and this subprocess carries a context, so an unbounded
// --version wedges the command outright rather than failing it.
//
// The real bound is VersionProbeTimeout (20s); the test shrinks it so it
// does not have to spend 20s proving the point.
func TestDetectBinaryVersionGivesUpOnAHungBinary(t *testing.T) {
	// exec, so the kill lands on sleep itself rather than on a shell
	// whose surviving child would hold the stdout pipe open.
	script := writeProbeScript(t, "hung-cli", "exec sleep 300")

	prev := VersionProbeTimeout
	VersionProbeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { VersionProbeTimeout = prev })

	start := time.Now()
	got := detectBinaryVersion(script)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("detectBinaryVersion = %q, want \"\" — a timeout must look like any other probe failure", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("detectBinaryVersion blocked for %s with a %s bound; the timeout did not fire", elapsed, VersionProbeTimeout)
	}
}

// TestDetectBinaryVersionReadsFirstLine guards the happy path the bound
// must not disturb: callers still get the first line of --version output.
func TestDetectBinaryVersionReadsFirstLine(t *testing.T) {
	script := writeProbeScript(t, "chatty-cli", "echo '  1.2.3 (build 7)  '\necho 'trailing noise'")

	if got := detectBinaryVersion(script); got != "1.2.3 (build 7)" {
		t.Fatalf("detectBinaryVersion = %q, want %q", got, "1.2.3 (build 7)")
	}
}

func TestAdmissionHealthCheckSkipsVersionProbe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "version-probed")
	binary := writeProbeScript(t, "gemini", ": > "+marker+"\necho 9.9.9")
	t.Setenv("PATH", filepath.Dir(binary))
	t.Setenv("GEMINI_API_KEY", "test-key")

	admission, ok := CheckBackendHealthForAdmission(context.Background(), "gemini")
	if !ok {
		t.Fatal("CheckBackendHealthForAdmission(gemini) = false")
	}
	if !admission.Healthy || !admission.Installed || !admission.APIKeySet || admission.Version != "" {
		t.Fatalf("admission health = %+v, want ready facts without version", admission)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("admission health spawned --version; marker stat error = %v", err)
	}

	full, ok := CheckBackendHealth("gemini")
	if !ok || full.Version != "9.9.9" {
		t.Fatalf("full health = %+v, %v; want version 9.9.9", full, ok)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("full health did not run version probe: %v", err)
	}
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
	if caps.HasToolControl {
		// Tool control is table-driven by backend name (SupportsToolControl),
		// not an implementable interface; a fake named anything but "claude"
		// reports false regardless of what it implements.
		t.Error("expected HasToolControl=false for a non-claude fake")
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
	if caps.Streaming != nil || caps.Sessions != nil ||
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
	if caps.Streaming != nil || caps.Sessions != nil ||
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
