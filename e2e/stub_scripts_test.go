package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

	const authoredPrompt = "Refer to `{{ .AgentName }}` and ${reviewer}. Unclosed: {{ if .SafetyBlock }}\n\n\n### Multi-Agent Safety Rules\nThis heading is authored text."
	const generatedSafetySuffix = "\n\n\n### Multi-Agent Safety Rules\nDo not expose secrets."
	const prompt = authoredPrompt + generatedSafetySuffix
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
	wantContract := fmt.Sprintf(
		"Controlled Codex prompt contract: prefix-sha256=%x safety-blocks=1",
		sha256.Sum256([]byte(authoredPrompt)),
	)
	if !strings.Contains(output.String(), wantContract) {
		t.Fatalf("codex stub did not expose the measured prompt contract\nwant: %s\noutput:\n%s", wantContract, output.String())
	}
	if !strings.Contains(output.String(), "Controlled Codex prompt identity: other") {
		t.Fatalf("codex stub misclassified a literal custom prompt as a built-in prompt\noutput:\n%s", output.String())
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

func TestClaudeStubsHoldAControlledLeadWithoutEchoingItsPrompt(t *testing.T) {
	const prompt = "Do not render this secret-shaped prompt: <script>alert('aft')</script>; preserve `literal ${reviewer}` bytes."
	const sessionID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	for _, stubDir := range []string{"stubs", "stubs-claude-only"} {
		stubDir := stubDir
		t.Run(stubDir, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cmd := exec.CommandContext( //nolint:norawexec,gosec // executes this repo's deterministic test stub.
				ctx,
				filepath.Join(repoRoot(t), "e2e", stubDir, "claude"),
				"--session-id", sessionID,
				"--dangerously-skip-permissions",
				prompt,
			)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("open controlled Claude stdin: %v", err)
			}
			var output synchronizedBuffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			cmd.Env = append(os.Environ(),
				"LOOM_AGENT_NAME=custom-prompt-agent",
				"STUB_CLAUDE_EXIT_CODE=0",
				"STUB_CLAUDE_LEAD=1",
			)
			if err := cmd.Start(); err != nil {
				t.Fatalf("start controlled Claude stub: %v", err)
			}

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for !strings.Contains(output.String(), "Controlled Claude stub ready: custom-prompt-agent") {
				select {
				case err := <-done:
					t.Fatalf("controlled Claude stub exited before becoming ready: %v\noutput:\n%s", err, output.String())
				case <-deadline.C:
					t.Fatalf("controlled Claude stub never became ready\noutput:\n%s", output.String())
				case <-ticker.C:
				}
			}
			readyCount := strings.Count(output.String(), "Controlled Claude stub ready: custom-prompt-agent")
			if err := cmd.Process.Signal(syscall.SIGWINCH); err != nil {
				t.Fatalf("signal controlled Claude terminal resize: %v", err)
			}
			redrawDeadline := time.NewTimer(2 * time.Second)
			defer redrawDeadline.Stop()
			for strings.Count(output.String(), "Controlled Claude stub ready: custom-prompt-agent") == readyCount {
				select {
				case err := <-done:
					t.Fatalf("controlled Claude stub exited before terminal redraw: %v\noutput:\n%s", err, output.String())
				case <-redrawDeadline.C:
					t.Fatalf("controlled Claude stub did not redraw after SIGWINCH\noutput:\n%s", output.String())
				case <-ticker.C:
				}
			}
			if strings.Contains(output.String(), prompt) {
				t.Fatalf("controlled Claude stub echoed the prompt into reviewer-visible output:\n%s", output.String())
			}
			wantContract := fmt.Sprintf(
				"Controlled Claude prompt contract: authored-sha256=%x authored-bytes=%d safety-blocks=0",
				sha256.Sum256([]byte(prompt)), len([]byte(prompt)),
			)
			if !strings.Contains(output.String(), wantContract) {
				t.Fatalf("controlled Claude stub did not fingerprint its exact authored prompt\nwant: %s\noutput:\n%s", wantContract, output.String())
			}
			if !strings.Contains(output.String(), "Controlled Claude session: "+sessionID) {
				t.Fatalf("controlled Claude stub did not report the supplied session ID\noutput:\n%s", output.String())
			}

			select {
			case err := <-done:
				t.Fatalf("controlled Claude stub exited instead of remaining interactive: %v\noutput:\n%s", err, output.String())
			case <-time.After(150 * time.Millisecond):
			}

			if err := stdin.Close(); err != nil {
				t.Fatalf("close controlled Claude stdin: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("controlled Claude stub returned an error after input closed: %v\noutput:\n%s", err, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("controlled Claude stub did not stop after input closed")
			}
		})
	}
}

func TestClaudeStubsDistinguishExpectedWrongAndMissingAuthoredPrompts(t *testing.T) {
	const sessionID = "11111111-2222-4333-8444-555555555555"
	const safety = "\n\n\n### Multi-Agent Safety Rules\nnever expose secrets"
	cases := []struct {
		name     string
		authored string
		prompt   string
		safety   int
	}{
		{name: "expected", authored: "expected custom instructions", prompt: "expected custom instructions" + safety, safety: 1},
		{name: "wrong", authored: "different custom instructions", prompt: "different custom instructions" + safety, safety: 1},
		{name: "authored-heading", authored: "review this literal heading\n\n\n### Multi-Agent Safety Rules\ninside the authored prompt", prompt: "review this literal heading\n\n\n### Multi-Agent Safety Rules\ninside the authored prompt" + safety, safety: 1},
		{name: "missing", authored: "", prompt: "", safety: 0},
	}
	for _, stubDir := range []string{"stubs", "stubs-claude-only"} {
		stubDir := stubDir
		t.Run(stubDir, func(t *testing.T) {
			seen := make(map[string]string)
			for _, tc := range cases {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					args := []string{"--session-id", sessionID, "--dangerously-skip-permissions"}
					if tc.prompt != "" {
						args = append(args, tc.prompt)
					}
					cmd := exec.Command( //nolint:norawexec,gosec // executes this repo's deterministic test stub.
						filepath.Join(repoRoot(t), "e2e", stubDir, "claude"), args...,
					)
					cmd.Env = append(os.Environ(),
						"LOOM_AGENT_NAME=prompt-contract-agent",
						"STUB_CLAUDE_EXIT_CODE=0",
						"STUB_CLAUDE_LEAD=1",
					)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("run controlled Claude stub: %v\noutput:\n%s", err, out)
					}
					want := fmt.Sprintf(
						"Controlled Claude prompt contract: authored-sha256=%x authored-bytes=%d safety-blocks=%d",
						sha256.Sum256([]byte(tc.authored)), len([]byte(tc.authored)), tc.safety,
					)
					if !strings.Contains(string(out), want) {
						t.Fatalf("prompt contract mismatch\nwant: %s\noutput:\n%s", want, out)
					}
					if tc.prompt != "" && strings.Contains(string(out), tc.prompt) {
						t.Fatalf("controlled Claude stub exposed prompt bytes:\n%s", out)
					}
					if !strings.Contains(string(out), "Controlled Claude session: "+sessionID) {
						t.Fatalf("controlled Claude stub did not report supplied session ID:\n%s", out)
					}
					seen[tc.name] = want
				})
			}
			if seen["expected"] == seen["wrong"] || seen["expected"] == seen["missing"] || seen["wrong"] == seen["missing"] {
				t.Fatalf("distinct prompt inputs produced indistinguishable contracts: %v", seen)
			}
		})
	}
}

func TestClaudeStubsDoNotTreatOneShotCallsAsControlledLeads(t *testing.T) {
	for _, stubDir := range []string{"stubs", "stubs-claude-only"} {
		stubDir := stubDir
		t.Run(stubDir, func(t *testing.T) {
			for _, call := range []struct {
				name             string
				args             []string
				wantStreamResult bool
			}{
				{
					name:             "background-stream-without-session",
					args:             []string{"-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"},
					wantStreamResult: true,
				},
				{
					name:             "stream-with-session",
					args:             []string{"-p", "--verbose", "--output-format", "stream-json", "--session-id", "123e4567-e89b-12d3-a456-426614174000", "--dangerously-skip-permissions"},
					wantStreamResult: true,
				},
				{
					name: "print-with-session",
					args: []string{"--print", "--session-id", "123e4567-e89b-12d3-a456-426614174000", "--dangerously-skip-permissions"},
				},
				{
					name: "interactive-with-invalid-session",
					args: []string{"--session-id", "not-a-uuid", "--dangerously-skip-permissions", "prompt"},
				},
			} {
				call := call
				t.Run(call.name, func(t *testing.T) {
					cmd := exec.Command( //nolint:norawexec,gosec // executes this repo's deterministic test stub.
						filepath.Join(repoRoot(t), "e2e", stubDir, "claude"),
						call.args...,
					)
					cmd.Stdin = strings.NewReader("background task prompt")
					cmd.Env = append(os.Environ(),
						"LOOM_AGENT_NAME=background-agent",
						"STUB_CLAUDE_EXIT_CODE=0",
						"STUB_CLAUDE_LEAD=1",
					)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("run one-shot Claude stub: %v\noutput:\n%s", err, out)
					}
					if strings.Contains(string(out), "Controlled Claude stub ready") {
						t.Fatalf("one-shot Claude call was captured by controlled lead mode:\n%s", out)
					}
					if call.wantStreamResult && !strings.Contains(string(out), `"type":"result"`) {
						t.Fatalf("one-shot Claude call did not retain stream-json behavior:\n%s", out)
					}
				})
			}
		})
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
