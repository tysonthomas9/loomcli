package backends

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// mockHealthyBackend implements Backend + MetadataProvider + HealthCheckableBackend.
type mockHealthyBackend struct {
	mockBackend
}

func (m *mockHealthyBackend) Meta() BackendMeta {
	return BackendMeta{
		DisplayName: "MockHealthy",
		Version:     "1.0.0",
		Description: "A healthy mock backend",
		URL:         "https://example.com/healthy",
		BinaryName:  "mock-healthy",
	}
}

func (m *mockHealthyBackend) HealthCheck() HealthStatus {
	return HealthStatus{
		Healthy:   true,
		Installed: true,
		Version:   "1.0.0",
		APIKeySet: true,
		Message:   "ready",
	}
}

// mockUnhealthyBackend implements Backend + MetadataProvider + HealthCheckableBackend.
type mockUnhealthyBackend struct {
	mockBackend
}

func (m *mockUnhealthyBackend) Meta() BackendMeta {
	return BackendMeta{
		DisplayName: "MockUnhealthy",
		Version:     "",
		Description: "An unhealthy mock backend",
		BinaryName:  "mock-unhealthy",
	}
}

func (m *mockUnhealthyBackend) HealthCheck() HealthStatus {
	return HealthStatus{
		Healthy:   false,
		Installed: false,
		Version:   "",
		APIKeySet: false,
		Message:   "binary not found",
	}
}

// mockConfigurableBackend implements Backend + ConfigurableBackend.
type mockConfigurableBackend struct {
	mockBackend
}

func (m *mockConfigurableBackend) Options() []BackendOption {
	return []BackendOption{
		{Key: "model", Description: "Model to use", Default: "gpt-4", CurrentValue: "gpt-4o"},
	}
}

func (m *mockConfigurableBackend) SetOption(key, value string) error { return nil }
func (m *mockConfigurableBackend) GetOption(key string) (string, error) {
	return "gpt-4o", nil
}

// captureOutput captures stdout during fn execution.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read pipe: %v", err)
	}
	r.Close()
	os.Stdout = origStdout
	return buf.String()
}

func executeBackendCommand(t *testing.T, args ...string) (stdout, stderr string, runErr error) {
	t.Helper()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	previousStdout, previousStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWrite, stderrWrite
	root := &cobra.Command{Use: "loom"}
	root.AddGroup(&cobra.Group{ID: "config", Title: "Configuration"})
	root.AddCommand(backendCmd)
	root.SetArgs(args)
	root.SetOut(stdoutWrite)
	root.SetErr(stderrWrite)
	backendHealthCmd.SilenceErrors, backendHealthCmd.SilenceUsage = false, false
	backendInfoCmd.SilenceErrors, backendInfoCmd.SilenceUsage = false, false
	runErr = root.Execute()
	if runErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, runErr)
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	os.Stdout, os.Stderr = previousStdout, previousStderr
	root.RemoveCommand(backendCmd)
	backendHealthCmd.SilenceErrors, backendHealthCmd.SilenceUsage = false, false
	backendInfoCmd.SilenceErrors, backendInfoCmd.SilenceUsage = false, false
	stdoutBytes, err := io.ReadAll(stdoutRead)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrRead)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(stdoutBytes), string(stderrBytes), runErr
}

// --- GetBackendByName ---

func TestGetBackendByName_Found(t *testing.T) {
	resetBackendState(t)
	m := &mockBackend{name: "testbe"}
	RegisterBackend(m)

	got, ok := GetBackendByName("testbe")
	if !ok {
		t.Fatal("expected backend to be found")
	}
	if got != m {
		t.Fatal("returned backend does not match registered one")
	}
}

func TestGetBackendByName_NotFound(t *testing.T) {
	resetBackendState(t)

	_, ok := GetBackendByName("nonexistent")
	if ok {
		t.Fatal("expected backend not to be found")
	}
}

// --- backend list ---

func TestBackendListOutput(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "alpha"}})
	RegisterBackend(&mockUnhealthyBackend{mockBackend: mockBackend{name: "beta"}})

	backendMu.Lock()
	*activeBackend = "alpha"
	backendMu.Unlock()

	out := captureOutput(t, func() {
		runBackendList(nil, nil)
	})

	// Active backend should be marked with *
	if !strings.Contains(out, "* alpha") {
		t.Errorf("expected active marker for alpha, got:\n%s", out)
	}
	// Non-active should not be marked
	if !strings.Contains(out, "  beta") {
		t.Errorf("expected beta without active marker, got:\n%s", out)
	}
	// Display names should appear
	if !strings.Contains(out, "MockHealthy") {
		t.Errorf("expected display name MockHealthy, got:\n%s", out)
	}
	if !strings.Contains(out, "MockUnhealthy") {
		t.Errorf("expected display name MockUnhealthy, got:\n%s", out)
	}
}

func TestBackendListJSON(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "alpha"}})
	RegisterBackend(&mockBackend{name: "minimal"})

	backendMu.Lock()
	*activeBackend = "alpha"
	backendMu.Unlock()

	origFlag := backendListJSON
	backendListJSON = true
	t.Cleanup(func() { backendListJSON = origFlag })

	out := captureOutput(t, func() {
		runBackendList(nil, nil)
	})

	var entries []backendListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find alpha entry
	var alpha backendListEntry
	for _, e := range entries {
		if e.Name == "alpha" {
			alpha = e
			break
		}
	}
	if !alpha.Active {
		t.Error("expected alpha to be active")
	}
	if alpha.DisplayName != "MockHealthy" {
		t.Errorf("expected display name MockHealthy, got %q", alpha.DisplayName)
	}
	if !alpha.Installed {
		t.Error("expected alpha to be installed")
	}
}

func TestBackendListEmpty(t *testing.T) {
	resetBackendState(t)

	out := captureOutput(t, func() {
		runBackendList(nil, nil)
	})

	if !strings.Contains(out, "No backends registered") {
		t.Errorf("expected empty message, got:\n%s", out)
	}
}

// --- backend health ---

func TestBackendHealthOutput(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "good"}})

	out := captureOutput(t, func() {
		if err := runBackendHealth(nil, nil); err != nil {
			t.Fatalf("runBackendHealth: %v", err)
		}
	})

	if !strings.Contains(out, "good") {
		t.Errorf("expected backend name 'good', got:\n%s", out)
	}
	if !strings.Contains(out, "ready") {
		t.Errorf("expected 'ready' message, got:\n%s", out)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("expected checkmark for healthy backend, got:\n%s", out)
	}
}

func TestBackendHealthJSON(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "good"}})

	origFlag := backendHealthJSON
	backendHealthJSON = true
	t.Cleanup(func() { backendHealthJSON = origFlag })

	out := captureOutput(t, func() {
		if err := runBackendHealth(nil, nil); err != nil {
			t.Fatalf("runBackendHealth: %v", err)
		}
	})

	var entries []backendHealthEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Healthy {
		t.Error("expected healthy=true")
	}
	if entries[0].Message != "ready" {
		t.Errorf("expected message 'ready', got %q", entries[0].Message)
	}
}

func TestBackendHealthUsesCanonicalProjectionAndCodedExit(t *testing.T) {
	resetBackendState(t)
	RegisterBackend(&mockUnhealthyBackend{mockBackend: mockBackend{name: "bad"}})

	row := buildHealthEntry("bad")
	status, ok := CheckBackendHealth("bad")
	if !ok {
		t.Fatal("CheckBackendHealth(bad) = false")
	}
	info := buildInfoOutput("bad", InspectCapabilities(mustBackend(t, "bad")))
	if info.Health == nil || row.Healthy != status.Healthy || row.Installed != status.Installed ||
		row.APIKeySet != status.APIKeySet || row.Version != status.Version || row.Message != status.Message ||
		*info.Health != status {
		t.Fatalf("health projections drifted: row=%+v info=%+v canonical=%+v", row, info.Health, status)
	}

	stdout, stderr, err := executeBackendCommand(t, "backend", "health")
	if err == nil || cli.CommandExitCode(err) != 1 || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("runBackendHealth error = %v, want coded exit 1", err)
	}
	if !strings.Contains(stdout, "bad") || !strings.Contains(stdout, "binary not found") {
		t.Fatalf("stdout = %q, want unchanged health table", stdout)
	}
	if stderr != "one or more backends are unhealthy\n" {
		t.Fatalf("stderr = %q, want one main error line", stderr)
	}
}

func TestBackendInfoUnknownUsesCodedExit(t *testing.T) {
	resetBackendState(t)
	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "good"}})
	stdout, stderr, err := executeBackendCommand(t, "backend", "info", "missing")
	if err == nil || cli.CommandExitCode(err) != 1 || !strings.Contains(err.Error(), `unknown backend "missing"`) {
		t.Fatalf("runBackendInfo error = %v, want coded exit 1", err)
	}
	var coded interface{ CLIExitCode() int }
	if !errors.As(err, &coded) {
		t.Fatalf("runBackendInfo error = %T, want coded error", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "unknown backend \"missing\"; available: good\n" {
		t.Fatalf("stderr = %q, want one main error line", stderr)
	}
}

func mustBackend(t *testing.T, name string) cli.Backend {
	t.Helper()
	backend, ok := cli.GetBackendByName(name)
	if !ok {
		t.Fatalf("backend %q not registered", name)
	}
	return backend
}

func TestBackendHealthEmpty(t *testing.T) {
	resetBackendState(t)

	out := captureOutput(t, func() {
		if err := runBackendHealth(nil, nil); err != nil {
			t.Fatalf("runBackendHealth: %v", err)
		}
	})

	if !strings.Contains(out, "No backends registered") {
		t.Errorf("expected empty message, got:\n%s", out)
	}
}

// --- backend info ---

func TestBackendInfoValidName(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "alpha"}})

	origFlag := backendInfoJSON
	backendInfoJSON = false
	t.Cleanup(func() { backendInfoJSON = origFlag })

	out := captureOutput(t, func() {
		if err := runBackendInfo(nil, []string{"alpha"}); err != nil {
			t.Fatalf("runBackendInfo: %v", err)
		}
	})

	if !strings.Contains(out, "Backend: alpha") {
		t.Errorf("expected 'Backend: alpha', got:\n%s", out)
	}
	if !strings.Contains(out, "MockHealthy") {
		t.Errorf("expected display name, got:\n%s", out)
	}
	if !strings.Contains(out, "Metadata:") {
		t.Errorf("expected Metadata section, got:\n%s", out)
	}
	if !strings.Contains(out, "Health:") {
		t.Errorf("expected Health section, got:\n%s", out)
	}
	if !strings.Contains(out, "Capabilities:") {
		t.Errorf("expected Capabilities section, got:\n%s", out)
	}
}

func TestBackendInfoJSON(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockHealthyBackend{mockBackend: mockBackend{name: "alpha"}})

	origFlag := backendInfoJSON
	backendInfoJSON = true
	t.Cleanup(func() { backendInfoJSON = origFlag })

	out := captureOutput(t, func() {
		if err := runBackendInfo(nil, []string{"alpha"}); err != nil {
			t.Fatalf("runBackendInfo: %v", err)
		}
	})

	var info backendInfoOutput
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if info.Name != "alpha" {
		t.Errorf("expected name 'alpha', got %q", info.Name)
	}
	if info.Meta == nil {
		t.Fatal("expected meta to be non-nil")
	}
	if info.Meta.DisplayName != "MockHealthy" {
		t.Errorf("expected display name MockHealthy, got %q", info.Meta.DisplayName)
	}
	if info.Health == nil {
		t.Fatal("expected health to be non-nil")
	}
	if !info.Health.Healthy {
		t.Error("expected healthy=true")
	}
	if info.Capabilities == nil {
		t.Fatal("expected capabilities to be non-nil")
	}
	if !info.Capabilities.HealthChk {
		t.Error("expected health_check capability=true")
	}
	if !info.Capabilities.Metadata {
		t.Error("expected metadata capability=true")
	}
}

func TestBackendInfoCapabilities(t *testing.T) {
	resetBackendState(t)

	// mockBackend has no optional interfaces
	RegisterBackend(&mockBackend{name: "bare"})

	origFlag := backendInfoJSON
	backendInfoJSON = true
	t.Cleanup(func() { backendInfoJSON = origFlag })

	out := captureOutput(t, func() {
		if err := runBackendInfo(nil, []string{"bare"}); err != nil {
			t.Fatalf("runBackendInfo: %v", err)
		}
	})

	var info backendInfoOutput
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if info.Capabilities.Streaming {
		t.Error("expected streaming=false for bare backend")
	}
	if info.Capabilities.Sessions {
		t.Error("expected sessions=false for bare backend")
	}
	if info.Capabilities.HealthChk {
		t.Error("expected health_check=false for bare backend")
	}
	if info.Capabilities.Metadata {
		t.Error("expected metadata=false for bare backend")
	}
	if info.Meta != nil {
		t.Error("expected meta to be nil for bare backend")
	}
	if info.Health != nil {
		t.Error("expected health to be nil for bare backend")
	}
}

func TestBackendInfoWithConfig(t *testing.T) {
	resetBackendState(t)

	RegisterBackend(&mockConfigurableBackend{mockBackend: mockBackend{name: "configbe"}})

	origFlag := backendInfoJSON
	backendInfoJSON = true
	t.Cleanup(func() { backendInfoJSON = origFlag })

	out := captureOutput(t, func() {
		if err := runBackendInfo(nil, []string{"configbe"}); err != nil {
			t.Fatalf("runBackendInfo: %v", err)
		}
	})

	var info backendInfoOutput
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if len(info.Config) != 1 {
		t.Fatalf("expected 1 config option, got %d", len(info.Config))
	}
	if info.Config[0].Key != "model" {
		t.Errorf("expected config key 'model', got %q", info.Config[0].Key)
	}
	if info.Config[0].CurrentValue != "gpt-4o" {
		t.Errorf("expected current value 'gpt-4o', got %q", info.Config[0].CurrentValue)
	}
}

// --- helper tests ---

func TestBoolSymbol(t *testing.T) {
	if got := boolSymbol(true); got != "✓" {
		t.Errorf("boolSymbol(true) = %q, want ✓", got)
	}
	if got := boolSymbol(false); got != "✗" {
		t.Errorf("boolSymbol(false) = %q, want ✗", got)
	}
}
