package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

func TestCodexStubRunsAControlledLeadWithTheLiteralPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("LOOM_AGENT_NAME", "custom-prompt-agent")
	t.Setenv("STUB_CODEX_EXIT_CODE", "0")
	argvLog := filepath.Join(tmp, "codex-argv.log")
	t.Setenv("STUB_ARGV_LOG", argvLog)
	codexPath := filepath.Join(repoRoot(t), "e2e", "stubs", "codex")

	const prompt = "Refer to {{ .AgentName }} and {{.Role}}. Unclosed: {{ if .SafetyBlock }}"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdinReader, stdinWriter := io.Pipe()
	defer func() { _ = stdinReader.Close() }()
	defer func() { _ = stdinWriter.Close() }()

	var output synchronizedBuffer
	done := make(chan error, 1)
	go func() {
		done <- leadcontrol.RunCodexLeadRuntime(ctx, leadcontrol.CodexLeadRuntimeConfig{
			Store:     memstore.New(),
			Workspace: "E2E-STUB",
			LeadName:  "custom-prompt-agent",
			SessionID: "controlled-runtime",
			WorkDir:   tmp,
			Prompt:    prompt,
			CodexPath: codexPath,
			Stdin:     stdinReader,
			Stdout:    &output,
			Stderr:    &output,
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for !(strings.Contains(output.String(), "Launching controlled Codex lead session...") &&
		strings.Contains(output.String(), "Controlled Codex stub connected: custom-prompt-agent")) {
		select {
		case err := <-done:
			t.Fatalf("controlled runtime exited before becoming ready: %v\noutput:\n%s", err, output.String())
		case <-deadline.C:
			t.Fatalf("controlled runtime never became ready\noutput:\n%s", output.String())
		case <-ticker.C:
		}
	}

	select {
	case err := <-done:
		t.Fatalf("controlled runtime exited instead of remaining interactive: %v\noutput:\n%s", err, output.String())
	case <-time.After(150 * time.Millisecond):
	}

	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("close controlled runtime input: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("controlled runtime returned an error after input closed: %v\noutput:\n%s", err, output.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("controlled runtime did not stop after input closed")
	}

	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read codex argv log: %v", err)
	}
	var sawAppServer, sawRemotePrompt bool
	for _, record := range parseNULArgvRecords(raw) {
		if len(record) < 3 {
			continue
		}
		argv := record[2:]
		if len(argv) >= 3 && argv[0] == "app-server" && argv[1] == "--listen" {
			sawAppServer = true
		}
		if len(argv) > 0 && argv[0] == "--remote" && argv[len(argv)-1] == prompt {
			sawRemotePrompt = true
		}
	}
	if !sawAppServer {
		t.Fatalf("codex stub did not record the controlled app-server launch: %q", raw)
	}
	if !sawRemotePrompt {
		t.Fatalf("codex stub did not receive the literal custom prompt through the remote TUI: %q", raw)
	}
	if !strings.Contains(output.String(), "Controlled Codex prompt contract: prefix-sha256=b8078611e09da1b89c51c636f9e19b638bfaf0e6c159f3a2f0862e4f5f0bd77e safety-blocks=0") {
		t.Fatalf("codex stub did not expose the measured prompt contract\noutput:\n%s", output.String())
	}
}

func TestCodexStubRemoteRequiresAReachableAppServer(t *testing.T) {
	t.Setenv("STUB_ARGV_LOG", "")
	t.Setenv("STUB_CODEX_EXIT_CODE", "0")
	cmd := exec.Command(
		filepath.Join(repoRoot(t), "e2e", "stubs", "codex"),
		"--remote", "ws://127.0.0.1:1", "prompt",
	) //nolint:norawexec,gosec // executes this repo's deterministic test stub.
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("remote stub reported success without a reachable app server; output:\n%s", out)
	}
	if strings.Contains(string(out), "Controlled Codex stub connected") {
		t.Fatalf("remote stub emitted its connection marker before connecting; output:\n%s", out)
	}
}

func TestCodexStubExitCodeFailsControlledRuntimeBeforeBootstrap(t *testing.T) {
	t.Setenv("STUB_ARGV_LOG", "")
	t.Setenv("STUB_CODEX_EXIT_CODE", "23")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		filepath.Join(repoRoot(t), "e2e", "stubs", "codex"),
		"app-server", "--listen", "ws://127.0.0.1:0",
	) //nolint:norawexec,gosec // executes this repo's deterministic test stub.
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("controlled stub exit = %v, want exit code 23", err)
	}
}

func TestCodexStubEpicRunnerPropagatesDesignLookupFailure(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}

	writeExecutable(t, filepath.Join(binDir, "loom"), `#!/bin/sh
if [ "$1" = "data" ] && [ "$2" = "--output" ] && [ "$3" = "json" ] && [ "$4" = "show" ]; then
  printf '{"design":"STUB_CODEX_WRITE=masked.txt"}\n'
  exit 42
fi
exit 0
`)
	writeExecutable(t, filepath.Join(binDir, "jq"), `#!/bin/sh
if [ "$1" = "-nc" ]; then
  printf '{"status":"completed","output":"ok"}\n'
  exit 0
fi
cat >/dev/null
exit 0
`)
	writeExecutable(t, filepath.Join(binDir, "make"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "git"), "#!/bin/sh\nexit 0\n")

	cmd := exec.Command(filepath.Join(repoRoot(t), "e2e", "stubs", "codex"), "exec", "--json") //nolint:norawexec,gosec // executes this repo's deterministic test stub.
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("pre-assigned task: E2E-2\n")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_CODEX_EPIC_RUNNER=1",
		"LOOM_ASSIGNED_TASK_ID=E2E-2",
		"LOOM_AGENT_NAME=worker-e2e-2-a1",
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("codex stub exited successfully; masked failed design lookup. output:\n%s", out)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func parseNULArgvRecords(raw []byte) [][]string {
	chunks := bytes.Split(raw, []byte{0, 0})
	records := make([][]string, 0, len(chunks))
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		fields := bytes.Split(chunk, []byte{0})
		record := make([]string, 0, len(fields))
		for _, field := range fields {
			record = append(record, string(field))
		}
		records = append(records, record)
	}
	return records
}
