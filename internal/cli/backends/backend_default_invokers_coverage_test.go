package backends

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestDefaultBackendInvokersRunFakeCLIs(t *testing.T) {
	binDir := t.TempDir()
	workDir := t.TempDir()
	writeFakeBackendCLI(t, binDir, "claude", "claude 1.0.0", []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"claude ok"}]}}`,
		`{"type":"message_delta","usage":{"input_tokens":2,"output_tokens":3}}`,
	}, 0)
	writeFakeBackendCLI(t, binDir, "codex", "codex 1.0.0", []string{
		`{"type":"turn.completed","usage":{"input_tokens":5,"output_tokens":7}}`,
	}, 0)
	writeFakeBackendCLI(t, binDir, "cursor", "cursor 1.0.0", []string{
		`{"usage":{"input_tokens":11,"output_tokens":13}}`,
	}, 0)
	writeFakeBackendCLI(t, binDir, "gemini", "gemini 1.0.0", []string{
		`{"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":19}}`,
	}, 0)
	writeFakeBackendCLI(t, binDir, "opencode", "opencode 1.0.0", []string{
		`{"usage":{"input_tokens":23,"output_tokens":29}}`,
	}, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "anthropic")
	t.Setenv("OPENAI_API_KEY", "openai")
	t.Setenv("CURSOR_API_KEY", "cursor")
	t.Setenv("GEMINI_API_KEY", "gemini")
	t.Setenv("LOOM_OPENCODE_MODEL", "test/model")

	err := captureBackendStdout(t, func() error {
		if err := defaultClaudeInvoker(workDir, "interactive prompt", "nova"); err != nil {
			return err
		}
		if err := defaultClaudeNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), usage.NewCollector("claude", "nova")); err != nil {
			return err
		}
		if err := defaultCodexInvoker(workDir, "interactive prompt", "nova"); err != nil {
			return err
		}
		if err := defaultCodexNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), usage.NewCollector("codex", "nova")); err != nil {
			return err
		}
		if err := defaultCursorInvoker(workDir, "interactive prompt", "nova"); err != nil {
			return err
		}
		if err := defaultCursorNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), usage.NewCollector("cursor", "nova")); err != nil {
			return err
		}
		if err := defaultGeminiInvoker(workDir, "interactive prompt", "nova"); err != nil {
			return err
		}
		if err := defaultGeminiNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), usage.NewCollector("gemini", "nova")); err != nil {
			return err
		}
		if err := defaultOpenCodeInvoker(workDir, "interactive prompt", "nova"); err != nil {
			return err
		}
		return defaultOpenCodeNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), usage.NewCollector("opencode", "nova"))
	})
	if err != nil {
		t.Fatalf("default invoker returned error: %v", err)
	}

	checks := []struct {
		name    string
		status  HealthStatus
		version string
	}{
		{"claude", (&ClaudeBackend{}).HealthCheck(), "claude 1.0.0"},
		{"codex", (&CodexBackend{}).HealthCheck(), "codex 1.0.0"},
		{"cursor", (&CursorBackend{}).HealthCheck(), "cursor 1.0.0"},
		{"gemini", (&GeminiBackend{}).HealthCheck(), "gemini 1.0.0"},
		{"opencode", (&OpenCodeBackend{}).HealthCheck(), "opencode 1.0.0"},
	}
	for _, check := range checks {
		if !check.status.Healthy || !check.status.Installed || check.status.Version != check.version {
			t.Fatalf("%s health = %#v", check.name, check.status)
		}
	}

	cmd := buildOpenCodeInteractiveCmd(workDir, "prompt", "nova")
	if !containsDefaultInvokerArg(cmd.Args, "--model") || !containsDefaultInvokerArg(cmd.Args, "test/model") {
		t.Fatalf("opencode model args missing from %v", cmd.Args)
	}
}

func TestDefaultBackendInvokerFailuresWrapOutput(t *testing.T) {
	binDir := t.TempDir()
	workDir := t.TempDir()
	writeFakeBackendCLI(t, binDir, "codex", "codex 1.0.0", []string{`{"type":"error","message":"rate limit"}`}, 7)
	writeFakeBackendCLI(t, binDir, "opencode", "opencode 1.0.0", []string{
		`{"type":"error","error":{"data":{"message":"model missing"}}}`,
	}, 0)
	t.Setenv("PATH", binDir)

	err := captureBackendStdout(t, func() error {
		return defaultCodexNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), nil)
	})
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("codex error = %T, want *InvocationError", err)
	}
	if invErr.ExitCode != 7 || !strings.Contains(invErr.OutputTail, "rate limit") {
		t.Fatalf("codex invocation error = %#v", invErr)
	}

	fakeOpenCode := &fakeWrapperRun{
		stdoutBody: `{"type":"error","error":{"data":{"message":"model missing"}}}` + "\n",
		result:     wrapper.Result{Status: wrapper.StatusIdle},
	}
	installWrapperRunMock(t, fakeOpenCode.Run)

	err = captureBackendStdout(t, func() error {
		return defaultOpenCodeNonInteractiveInvoker(workDir, "prompt", "nova", make(chan struct{}), nil)
	})
	if !errors.As(err, &invErr) {
		t.Fatalf("opencode error = %T, want *InvocationError", err)
	}
	if invErr.ExitCode != 1 || !strings.Contains(invErr.OutputTail, "model missing") {
		t.Fatalf("opencode invocation error = %#v", invErr)
	}
}

func writeFakeBackendCLI(t *testing.T, dir, name, version string, lines []string, exitCode int) {
	t.Helper()
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("if [ \"${1:-}\" = \"--version\" ]; then echo '")
	script.WriteString(version)
	script.WriteString("'; exit 0; fi\n")
	for _, line := range lines {
		script.WriteString("printf '%s\\n' ")
		script.WriteString(shellQuote(line))
		script.WriteString("\n")
	}
	script.WriteString("exit ")
	script.WriteString(strconv.Itoa(exitCode))
	script.WriteString("\n")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script.String()), 0700); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func captureBackendStdout(t *testing.T, fn func() error) error {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	runErr := fn()
	_ = w.Close()
	os.Stdout = oldStdout
	<-done
	_ = r.Close()
	return runErr
}

func containsDefaultInvokerArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
