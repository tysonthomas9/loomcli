package svcimpl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestAgentServiceTerminalTokenAndUnavailableBranches(t *testing.T) {
	ctx := context.Background()
	svc := NewAgentService(&fakeGitOps{}, nil, nil, nil)
	if got := agentLogTokenScope("nova"); got != "agent:nova:logs" {
		t.Fatalf("agentLogTokenScope = %q", got)
	}
	if _, err := svc.GetTerminalInfo(ctx, "WS", "bad/name"); err == nil {
		t.Fatal("GetTerminalInfo invalid name returned nil error")
	}
	if _, err := svc.GetTerminalInfo(ctx, "WS", "nova"); err == nil {
		t.Fatal("GetTerminalInfo without terminal manager returned nil error")
	}
	if _, err := svc.GenerateTerminalToken(ctx, "WS", "bad/name", "user-1"); err == nil {
		t.Fatal("GenerateTerminalToken invalid name returned nil error")
	}
	if _, err := svc.GenerateTerminalToken(ctx, "WS", "nova", "user-1"); err == nil {
		t.Fatal("GenerateTerminalToken without auth returned nil error")
	}

	auth, err := realtime.NewTerminalAuth()
	if err != nil {
		t.Fatalf("NewTerminalAuth: %v", err)
	}
	t.Cleanup(auth.Stop)
	svc = NewAgentService(&fakeGitOps{}, nil, auth, nil)
	token, err := svc.GenerateTerminalToken(ctx, "WS", "nova", "user-1")
	if err != nil {
		t.Fatalf("GenerateTerminalToken: %v", err)
	}
	userID, err := auth.ValidateToken(token, agentLogTokenScope("nova"), "WS")
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("token userID = %q", userID)
	}
}

func TestAgentServiceGetLogReadsWorkspaceAgentLog(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", runtimeDir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	svc := NewAgentService(&fakeGitOps{}, nil, nil, nil)
	if _, err := svc.GetLog(context.Background(), "WS", "bad/name", 10, 0); err == nil {
		t.Fatal("GetLog invalid name returned nil error")
	}
	if _, err := svc.GetLog(context.Background(), "WS", "nova", 10, 0); err == nil {
		t.Fatal("GetLog missing file returned nil error")
	}

	logPath, err := webuilog.GetAgentLogPath("WS", "nova")
	if err != nil {
		t.Fatalf("GetAgentLogPath: %v", err)
	}
	if !strings.HasPrefix(logPath, filepath.Join(runtimeDir, ".loom", "logs")) {
		t.Fatalf("logPath = %q, want under runtime dir", logPath)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("one\n two\nthree\n"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	result, err := svc.GetLog(context.Background(), "WS", "nova", 2, 0)
	if err != nil {
		t.Fatalf("GetLog: %v", err)
	}
	if result.StartLine != 2 || result.LineCount != 3 || len(result.Lines) != 2 {
		t.Fatalf("log result = %+v", result)
	}
	if result.Lines[0] != " two" || result.Lines[1] != "three" {
		t.Fatalf("log lines = %#v", result.Lines)
	}

	result, err = svc.GetLog(context.Background(), "WS", "nova", 0, 0)
	if err != nil {
		t.Fatalf("GetLog default lines: %v", err)
	}
	if result.StartLine != 1 || result.LineCount != 3 || len(result.Lines) != 3 {
		t.Fatalf("default log result = %+v", result)
	}

	result, err = svc.GetLog(context.Background(), "WS", "nova", webuilog.LogReadMaxLines+10, 0)
	if err != nil {
		t.Fatalf("GetLog clamped lines: %v", err)
	}
	if result.LineCount != 3 || len(result.Lines) != 3 {
		t.Fatalf("clamped log result = %+v", result)
	}
}

func TestAgentServiceGetTerminalInfoArchiveMode(t *testing.T) {
	mgr, err := terminal.NewAgentTmuxManager(2)
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown() })

	svc := NewAgentService(&fakeGitOps{}, mgr, nil, nil)
	result, err := svc.GetTerminalInfo(context.Background(), "WS", "nova")
	if err != nil {
		t.Fatalf("GetTerminalInfo: %v", err)
	}
	if result.Agent != "nova" || result.Mode == "" {
		t.Fatalf("terminal result = %+v", result)
	}
}
