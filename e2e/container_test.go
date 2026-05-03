//go:build container

package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

var imageName string

func TestMain(m *testing.M) {
	// Check docker is available.
	if err := exec.Command("docker", "version").Run(); err != nil {
		fmt.Println("SKIP: docker not available")
		os.Exit(0)
	}

	// Build image with a unique tag to avoid collisions.
	imageName = fmt.Sprintf("loomcli-e2e-test-%d", os.Getpid())

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find repo root: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Building Docker image %s ...\n", imageName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "docker", "build", "-f", "e2e/Dockerfile", "-t", imageName, ".")
	build.Dir = root
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "docker build failed: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	// Cleanup image (best-effort) unless KEEP_IMAGE is set.
	if os.Getenv("KEEP_IMAGE") == "" {
		_ = exec.Command("docker", "rmi", imageName).Run()
	}

	os.Exit(code)
}

// repoRoot finds the repository root by walking up from cwd looking for go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

// dockerRun executes "docker run --rm <env> <image> <cmd...>" and returns
// the combined output, exit code, and any exec error.
func dockerRun(ctx context.Context, env map[string]string, cmd ...string) (output string, exitCode int, err error) {
	args := []string{"run", "--rm"}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, imageName)
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, "docker", args...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf

	err = c.Run()
	output = buf.String()

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return output, exitErr.ExitCode(), nil
		}
		return output, -1, err
	}
	return output, 0, nil
}

// requireExitCode asserts the exit code matches expected.
func requireExitCode(t *testing.T, expected, actual int, output string) {
	t.Helper()
	if expected != actual {
		t.Fatalf("expected exit code %d, got %d\nOutput:\n%s", expected, actual, output)
	}
}

// assertOutputContains checks output contains a substring.
func assertOutputContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, output)
	}
}

// assertOutputNotContains checks output does NOT contain a substring.
func assertOutputNotContains(t *testing.T, output, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("expected output to NOT contain %q, got:\n%s", substr, output)
	}
}

func TestContainer_SmokeTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	output, exitCode, err := dockerRun(ctx, nil, "verify_todo.sh")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
	assertOutputContains(t, output, "passed")
}

func TestContainer_FullTestSuite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()

	output, exitCode, err := dockerRun(ctx, nil, "run_test.sh")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
	assertOutputContains(t, output, "All phases passed")
}

func TestContainer_BackendMatrix(t *testing.T) {
	backends := []string{"claude", "codex", "opencode"}
	for _, be := range backends {
		t.Run(be, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			env := map[string]string{"LOOM_BACKEND": be}
			output, exitCode, err := dockerRun(ctx, env, "run_test.sh", "--phase", "e2e", "--backend", be)
			if err != nil {
				t.Fatalf("docker run failed: %v", err)
			}
			requireExitCode(t, 0, exitCode, output)
			assertOutputContains(t, output, be)
		})
	}
}

func TestContainer_StubExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := map[string]string{"STUB_CLAUDE_EXIT_CODE": "1"}
	output, exitCode, err := dockerRun(ctx, env,
		"claude", "-p", "--output-format", "stream-json", "--dangerously-skip-permissions")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 1, exitCode, output)
}

func TestContainer_StubCustomResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := map[string]string{"STUB_CLAUDE_RESPONSE": "custom_test_response"}
	output, exitCode, err := dockerRun(ctx, env,
		"claude", "-p", "--output-format", "stream-json", "--dangerously-skip-permissions")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
	assertOutputContains(t, output, "custom_test_response")
}

func TestContainer_StubDelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := map[string]string{"STUB_CLAUDE_DELAY": "2"}
	start := time.Now()
	output, exitCode, err := dockerRun(ctx, env,
		"claude", "--dangerously-skip-permissions", "hello")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
	if elapsed < 2*time.Second {
		t.Errorf("expected execution to take >= 2s, took %v", elapsed)
	}
}

func TestContainer_BinariesExist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, exitCode, err := dockerRun(ctx, nil,
		"sh", "-c", "which loom && which claude && which codex && which opencode && which tmux && which git && which jq")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
}

func TestContainer_DefaultBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, exitCode, err := dockerRun(ctx, nil, "sh", "-c", "echo $LOOM_BACKEND")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 0, exitCode, output)
	assertOutputContains(t, output, "claude")
}

func TestContainer_ErrorPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, exitCode, err := dockerRun(ctx, nil, "sh", "-c", "exit 42")
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	requireExitCode(t, 42, exitCode, output)
}
