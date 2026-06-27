package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type daemonGetterStub struct{ backend string }

func (s daemonGetterStub) Daemon() store.DaemonProfileStore {
	return daemonProfileStoreStub(s)
}

type daemonProfileStoreStub struct{ backend string }

func (s daemonProfileStoreStub) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return &domain.DaemonProfile{AgentBackend: s.backend}, nil
}

func (s daemonProfileStoreStub) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return &domain.DaemonProfile{AgentBackend: s.backend}, nil
}

func TestRunPreflightCheckJSONHealthyWorkspaceBackend(t *testing.T) {
	restore := runtimepreflight.SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		if name != "gemini" {
			t.Fatalf("checked backend = %q, want gemini", name)
		}
		return backends.HealthStatus{
			Healthy:   true,
			Installed: true,
			Version:   "gemini 1.2.3",
			APIKeySet: true,
			Message:   "ready",
		}, true
	})
	defer restore()

	var buf bytes.Buffer
	err := runPreflightCheck(context.Background(), daemonGetterStub{backend: "gemini"}, "TEST", "", true, &buf, nil)
	if err != nil {
		t.Fatalf("runPreflightCheck error: %v", err)
	}

	var out preflightOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if !out.Ready || out.Workspace != "TEST" || out.Backend != "gemini" {
		t.Fatalf("output = %+v, want ready TEST/gemini", out)
	}
	if out.Health.Version != "gemini 1.2.3" {
		t.Fatalf("version = %q, want gemini 1.2.3", out.Health.Version)
	}
	if out.ErrorClass != "" || out.Error != "" {
		t.Fatalf("healthy output has error fields: %+v", out)
	}
}

func TestRunPreflightCheckJSONFailureIncludesErrorClass(t *testing.T) {
	restore := runtimepreflight.SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		if name != "codex" {
			t.Fatalf("checked backend = %q, want codex", name)
		}
		return backends.HealthStatus{
			Healthy:   false,
			Installed: true,
			APIKeySet: false,
			Message:   "OPENAI_API_KEY not set",
		}, true
	})
	defer restore()

	var buf bytes.Buffer
	err := runPreflightCheck(context.Background(), daemonGetterStub{backend: "codex"}, "TEST", "", true, &buf, nil)
	if err == nil {
		t.Fatal("runPreflightCheck error = nil, want auth failure")
	}
	if !strings.Contains(err.Error(), runtimepreflight.ErrorClassBackendAuthMissing) {
		t.Fatalf("error = %v, want auth-missing class", err)
	}

	var out preflightOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Ready {
		t.Fatalf("Ready = true, want false")
	}
	if out.ErrorClass != runtimepreflight.ErrorClassBackendAuthMissing {
		t.Fatalf("ErrorClass = %q, want %q", out.ErrorClass, runtimepreflight.ErrorClassBackendAuthMissing)
	}
	if !strings.Contains(out.Error, "OPENAI_API_KEY not set") {
		t.Fatalf("Error = %q, want health detail", out.Error)
	}
}

func TestRunPreflightCheckUsesBackendOverride(t *testing.T) {
	var checked string
	restore := runtimepreflight.SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		checked = name
		return backends.HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"}, true
	})
	defer restore()

	var buf bytes.Buffer
	err := runPreflightCheck(context.Background(), daemonGetterStub{backend: "codex"}, "TEST", "claude", false, &buf, nil)
	if err != nil {
		t.Fatalf("runPreflightCheck error: %v", err)
	}
	if checked != "claude" {
		t.Fatalf("checked backend = %q, want override claude", checked)
	}
	if !strings.Contains(buf.String(), "Backend: claude") {
		t.Fatalf("human output = %q, want override backend", buf.String())
	}
}

func TestBackendOverrideReadsChangedBackendFlagOnly(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "codex", "backend")
	if got := backendOverride(cmd); got != "" {
		t.Fatalf("unchanged flag override = %q, want empty", got)
	}
	if err := cmd.Flags().Set("backend", "  gemini  "); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	if got := backendOverride(cmd); got != "gemini" {
		t.Fatalf("changed flag override = %q, want gemini", got)
	}
}
