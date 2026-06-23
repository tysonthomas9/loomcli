package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BundledRunnerOptions configures a one-shot, in-place invocation of a bundled
// builtin task runner (Phase U). Unlike the full HostBridgeTaskExecutor path, it does
// NO worktree provisioning or patch-back finalize — it runs the runner against an
// existing worktree and returns its raw result — so a host that already owns the
// worktree (the daemon execution leaf) can delegate the backend run to the TS runner.
type BundledRunnerOptions struct {
	// ServerPath is the materialized bundle server.mjs (see workflows.MaterializeBuiltinBundle).
	ServerPath string
	// Entrypoint is the runner name within the bundle (default: local-task-runner).
	Entrypoint string
	// Worktree is the directory the runner executes in.
	Worktree string
	// Backend selects the agent CLI (codex/claude/cursor/opencode/gemini).
	Backend string
	// RequestJSON is the task-runner request payload. Its `lease_token` must equal
	// LeaseToken below (the launcher rejects a mismatch).
	RequestJSON string
	// LeaseToken is the task-run lease token; gated against the request's lease_token.
	LeaseToken string
	// StreamStderr tees the backend's live output to Stderr per turn (watchdog feed).
	StreamStderr bool
	// Stderr receives the runner's live diagnostic output; defaults to os.Stderr.
	Stderr io.Writer
}

// RunBundledLocalTaskRunner runs the bundled local-task-runner against an existing
// worktree and returns its raw result JSON (transcript_entries + top-level usage +
// patch, etc.). It reuses the same Node launcher the driver host-bridge uses, so the
// daemon leaf and the driver share ONE execution path.
func RunBundledLocalTaskRunner(ctx context.Context, opts BundledRunnerOptions) (json.RawMessage, error) {
	if strings.TrimSpace(opts.ServerPath) == "" {
		return nil, fmt.Errorf("bundled runner: ServerPath is required")
	}
	entrypoint := strings.TrimSpace(opts.Entrypoint)
	if entrypoint == "" {
		entrypoint = LocalTaskRunnerEntrypoint
	}
	requestJSON := opts.RequestJSON
	if strings.TrimSpace(requestJSON) == "" {
		requestJSON = "{}"
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	launcherPath, cleanup, err := writeFlueTaskRunnerLauncher()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "node", launcherPath) //nolint:gosec // fixed local runtime for the bundled Flue runner.
	if wt := strings.TrimSpace(opts.Worktree); wt != "" {
		cmd.Dir = wt
	}
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"LOOM_TASK_RUNNER_SERVER_PATH="+opts.ServerPath,
		"LOOM_TASK_RUNNER_BUNDLE_ROOT="+filepath.Dir(opts.ServerPath),
		"LOOM_TASK_RUNNER_ENTRYPOINT="+entrypoint,
		"LOOM_TASK_RUNNER_KIND="+RunnerKindFlueWorkflow,
		"LOOM_TASK_RUN_REQUEST_JSON="+requestJSON,
		"LOOM_TASK_RUN_LEASE_TOKEN="+opts.LeaseToken,
	)
	if wt := strings.TrimSpace(opts.Worktree); wt != "" {
		env = append(env, "LOOM_WORKTREE_PATH="+wt)
	}
	if be := strings.TrimSpace(opts.Backend); be != "" {
		env = append(env, "LOOM_TASK_RUNNER_BACKEND="+be)
	}
	if opts.StreamStderr {
		env = append(env, "LOOM_TASK_RUNNER_STREAM_STDERR=1")
	}
	cmd.Env = env
	cmd.Stdin = bytes.NewReader([]byte(requestJSON))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = stderr // live runner/backend output -> caller's stderr (watchdog feed)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bundled local task runner failed: %w", err)
	}
	payload, err := lastJSONLine(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	return json.RawMessage(append([]byte{}, payload...)), nil
}
