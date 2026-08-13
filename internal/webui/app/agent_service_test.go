package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func requireServiceError(t *testing.T, err error, wantKind apperrors.ErrorKind) *apperrors.ServiceError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with kind %s, got nil", wantKind)
	}
	var serviceError *apperrors.ServiceError
	if !errors.As(err, &serviceError) {
		t.Fatalf("expected *apperrors.ServiceError, got %T: %v", err, err)
	}
	if serviceError.Kind != wantKind {
		t.Fatalf("ServiceError.Kind = %q, want %q", serviceError.Kind, wantKind)
	}
	return serviceError
}

func TestAgentServiceValidateAgentName(t *testing.T) {
	for _, name := range []string{"", "agent one", "agent/one", "../etc/passwd", "agent@foo!"} {
		if err := agentcoord.ValidateAgentName(name); err == nil {
			t.Errorf("ValidateAgentName(%q) succeeded", name)
		}
	}
	for _, name := range []string{"alpha", "test-agent", "agent_1", "ABC", "a-b_c-123", "agent.one"} {
		if err := agentcoord.ValidateAgentName(name); err != nil {
			t.Errorf("ValidateAgentName(%q) error = %v", name, err)
		}
	}
}

func TestAgentServiceGetTerminalInfo(t *testing.T) {
	ctx := context.Background()
	t.Run("nil manager", func(t *testing.T) {
		service := agentcoord.NewAgentService(nil, nil)
		_, err := service.GetTerminalInfo(ctx, "ws", "agent1")
		requireServiceError(t, err, apperrors.KindUnavailable)
	})
	t.Run("archive mode without session", func(t *testing.T) {
		manager, err := terminal.NewAgentTmuxManager(0)
		if err == terminal.ErrTmuxNotFound {
			t.Skip("tmux not installed")
		}
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Shutdown()
		service := agentcoord.NewAgentService(manager, nil)
		result, err := service.GetTerminalInfo(ctx, "ws", "nonexistent-agent")
		if err != nil || result.Mode != agentcoord.AgentTerminalModeArchive || result.Agent != "nonexistent-agent" {
			t.Fatalf("GetTerminalInfo() = %#v, %v", result, err)
		}
	})
}

func TestAgentServiceGenerateTerminalToken(t *testing.T) {
	ctx := context.Background()
	service := agentcoord.NewAgentService(nil, nil)
	if _, err := service.GenerateTerminalToken(ctx, "test-ws", "agent1", "user1"); err == nil {
		t.Fatal("GenerateTerminalToken() succeeded without auth")
	}
	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Stop()
	service = agentcoord.NewAgentService(nil, auth)
	if token, err := service.GenerateTerminalToken(ctx, "test-ws", "agent1", "user1"); err != nil || token == "" {
		t.Fatalf("GenerateTerminalToken() = %q, %v", token, err)
	}
}

func TestAgentServiceGetLogBounds(t *testing.T) {
	ctx := context.Background()
	service := agentcoord.NewAgentService(nil, nil)
	if _, err := service.GetLog(ctx, "ws", "no-such-agent", 100, 0); err == nil {
		t.Fatal("GetLog() succeeded for missing log")
	}
	for _, test := range []struct {
		workspace string
		agent     string
		lines     int
		written   int
		want      int
	}{
		{workspace: "test-clamp-ws", agent: "clamp-agent", lines: 0, written: 300, want: 200},
		{workspace: "test-clampmax-ws", agent: "clampmax-agent", lines: 15000, written: 10100, want: 10000},
	} {
		t.Run(test.agent, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("LOOM_CONFIG_DIR", configDir)
			t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")
			logDir := filepath.Join(configDir, ".loom", "logs", test.workspace, "agents")
			if err := os.MkdirAll(logDir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := make([]byte, 0, test.written*5)
			for range test.written {
				content = append(content, "line\n"...)
			}
			if err := os.WriteFile(filepath.Join(logDir, test.agent+".log"), content, 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := service.GetLog(ctx, test.workspace, test.agent, test.lines, 0)
			if err != nil || len(result.Lines) != test.want {
				t.Fatalf("GetLog() lines = %d, %v; want %d", len(result.Lines), err, test.want)
			}
		})
	}
}
