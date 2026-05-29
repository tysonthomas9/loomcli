package tsfirst

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestRunLocalConnectEchoPersistsTranscript(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=test\n# ignored\nNODE_ENV=test\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "default",
		EnvFile:  envPath,
		Message:  "say hello",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.Agent != "hello-world" || result.Instance != "local" || result.Session != "default" {
		t.Fatalf("result identity = %+v, want hello-world/local/default", result)
	}
	if result.Response != "echo: say hello" {
		t.Fatalf("response = %q, want echo response", result.Response)
	}
	if !strings.Contains(strings.Join(result.Env, ","), "ANTHROPIC_API_KEY") || !strings.Contains(strings.Join(result.Env, ","), "NODE_ENV") {
		t.Fatalf("env allowlist = %+v, want env file keys", result.Env)
	}
	if result.TranscriptPath == "" {
		t.Fatalf("transcript path is empty")
	}
	data, err := os.ReadFile(result.TranscriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("transcript lines = %d, want 1: %s", len(lines), data)
	}
	var turn localTurn
	if err := json.Unmarshal([]byte(lines[0]), &turn); err != nil {
		t.Fatalf("parse transcript turn: %v", err)
	}
	if turn.Message != "say hello" || turn.Response != "echo: say hello" || turn.DefinitionVersion == "" || turn.PromptHash == "" {
		t.Fatalf("turn = %+v, want persisted local connect turn", turn)
	}
}

func TestRunLocalConnectIncludesPriorSessionHistory(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}

	if _, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "support",
		Message:  "first",
	}); err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "support",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].Message != "first" || turns[1].Message != "second" {
		t.Fatalf("turns = %+v, want two persisted turns in order", turns)
	}
}

func TestRunLocalConnectLoadsEnvFileForBackend(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(envCheckBackend{})
	root := t.TempDir()
	writeAgent(t, root, "env-agent", "envcheck")
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("LOCAL_CONNECT_TOKEN=\"loaded-from-file\"\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	t.Setenv("LOCAL_CONNECT_TOKEN", "")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "env-agent",
		Instance: "local",
		Session:  "default",
		EnvFile:  envPath,
		Message:  "check env",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.Backend != "envcheck" {
		t.Fatalf("backend = %q, want envcheck", result.Backend)
	}
	if got := os.Getenv("LOCAL_CONNECT_TOKEN"); got != "" {
		t.Fatalf("LOCAL_CONNECT_TOKEN after run = %q, want restored empty value", got)
	}
}

func TestRunInteractiveConnectProcessesPromptLines(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	var out bytes.Buffer
	if err := runInteractiveConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "interactive",
	}, strings.NewReader("first\n\nsecond\n/quit\nignored\n"), &out); err != nil {
		t.Fatalf("runInteractiveConnect() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Enter one prompt per line") || !strings.Contains(got, "hello-world: echo: first") || !strings.Contains(got, "hello-world: echo: second") {
		t.Fatalf("interactive output = %q, want prompt loop and two echo responses", got)
	}
	turns, err := readLocalTurns(localTranscriptPath(root, "hello-world", "local", "interactive"))
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].Message != "first" || turns[1].Message != "second" {
		t.Fatalf("turns = %+v, want two persisted interactive turns before /quit", turns)
	}
}

type envCheckBackend struct{}

func (envCheckBackend) Name() string { return "envcheck" }

func (envCheckBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (envCheckBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	if got := os.Getenv("LOCAL_CONNECT_TOKEN"); got != "loaded-from-file" {
		return fmt.Errorf("LOCAL_CONNECT_TOKEN = %q, want loaded-from-file", got)
	}
	return nil
}

func writeAgent(t *testing.T, root, name, backend string) {
	t.Helper()
	path := filepath.Join(root, ".loom", "agents", name+".ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	src := fmt.Sprintf(`import { createAgent, runtime } from '@loom/runtime';

export default createAgent({
  name: %q,
  backend: %q,
  model: 'local/test',
  runtime: runtime.local({ repos: ['.'], env: ['LOCAL_CONNECT_TOKEN'] }),
  instructions: 'Check local connect env.',
});
`, name, backend)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
}
