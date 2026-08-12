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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/sandbox"
)

// DaytonaTaskRunnerEntrypoint is the bundled Daytona task runner entrypoint. It
// provisions a Daytona sandbox, clones the repo, and runs the agent inside it
// (host-side harness driving the sandbox). Selected for the daemon leaf via
// LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner.
const DaytonaTaskRunnerEntrypoint = "daytona-task-runner"

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
	// Prompt, when set, is delivered to the runner via LOOM_TASK_RUN_PROMPT so the
	// caller's exact prompt is used verbatim (e.g. the daemon leaf's role-specific
	// planning/task prompt) instead of the runner's generic buildPrompt.
	Prompt string
	// LeaseToken is the task-run lease token; gated against the request's lease_token.
	LeaseToken string
	// StreamStderr emits fixed backend-activity signals to Stderr (watchdog feed)
	// without exposing raw backend output, which may contain credentials.
	StreamStderr bool
	// Stderr receives the runner's live diagnostics/activity signals; defaults to os.Stderr.
	Stderr io.Writer
}

// RunBundledTaskRunner runs a bundled task runner (local-task-runner by default, or the
// daytona-task-runner via opts.Entrypoint) against an existing worktree and returns its raw
// result JSON (transcript_entries + top-level usage + patch, etc.). It reuses the same Node
// launcher the driver host-bridge uses, so the daemon leaf and the driver share ONE
// execution path.
func RunBundledTaskRunner(ctx context.Context, opts BundledRunnerOptions) (json.RawMessage, error) {
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

	cmd := exec.CommandContext(ctx, processNodePath(""), launcherPath) //nolint:gosec // resolved packaged/operator Node runtime; launcherPath is a temp file.
	if wt := strings.TrimSpace(opts.Worktree); wt != "" {
		cmd.Dir = wt
	}
	cmd.Env = buildLeafRunnerEnv(opts, entrypoint, requestJSON)
	cmd.Stdin = strings.NewReader(requestJSON)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = stderr // live runner diagnostics/activity signals -> caller's stderr (watchdog feed)
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

// buildLeafRunnerEnv assembles the environment for the bundled Node launcher: the host
// environment plus the LOOM_TASK_RUNNER_* wiring the runner reads. The host env is passed
// through as-is — the daemon supervisor already scrubs it via cli.FilteredEnv
// (allowlist/blocklist) before spawning this leaf, so this deliberately does NOT re-run the
// host-bridge's scopedSubprocessBaseEnv filtering.
func buildLeafRunnerEnv(opts BundledRunnerOptions, entrypoint, requestJSON string) []string {
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
	if strings.TrimSpace(opts.Prompt) != "" {
		env = append(env, "LOOM_TASK_RUN_PROMPT="+opts.Prompt)
	}
	return env
}

// LegacyDriverAuthEnvVar names the env switch that keeps the deprecated
// static-bearer auth surface flowing to workflow runtimes that already
// receive a run-scoped token: LOOM_DRIVER_API_TOKEN (node-wide shared bearer,
// cross-run authority) plus the LOOM_DRIVER_LEASE_ID/LOOM_DRIVER_FENCING_TOKEN
// identity vars used by header-quad auth. While enabled, workflow bundles
// built against the pre-token SDK keep authenticating; bundles on the
// token-aware SDK ignore the legacy vars and go token-only.
//
// Deprecated — removal path (§9.5 workflow env lockdown):
//  1. This release: default ON, so loom-dev keeps working at deploy. Rebuild
//     workflow bundles against the token-aware SDK, then set
//     LOOM_DRIVER_LEGACY_AUTH_ENV=0 on serve to lock the env down.
//  2. Next release: the default flips OFF (=1 stays as break-glass).
//  3. The release after: the switch and the legacy export are removed.
//
// Runs without a minted token (no RunTokenKey, e.g. CLI/ops executors) always
// get the legacy env regardless of this switch — no flag-day.
const LegacyDriverAuthEnvVar = "LOOM_DRIVER_LEGACY_AUTH_ENV"

// legacyDriverAuthEnv reports whether this run's workflow env carries the
// legacy static-bearer auth surface. Token-less runs always do (it is their
// only auth); token-carrying runs only while the deprecated
// LegacyDriverAuthEnvVar fallback stays enabled.
func legacyDriverAuthEnv(req RunRequest) bool {
	if strings.TrimSpace(req.RunToken) == "" {
		return true
	}
	return legacyDriverAuthEnvEnabled(os.Getenv(LegacyDriverAuthEnvVar))
}

// legacyDriverAuthEnvEnabled parses the switch value: default ON for one
// release; only an explicit false-y value locks the env down.
func legacyDriverAuthEnvEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

type NodeRunner struct {
	NodePath        string
	ExecTaskCommand []string
	// APIBaseURL, when set, is exported to the driver runtime as
	// LOOM_DRIVER_API_URL so the workflow SDK uses the driver-op HTTP API on
	// loom serve instead of spawning CLI subprocesses.
	APIBaseURL string
	// APIToken is the shared driver API bearer token forwarded as
	// LOOM_DRIVER_API_TOKEN when APIBaseURL is set — but only to runs on the
	// legacy auth surface (no run token, or the deprecated
	// LegacyDriverAuthEnvVar fallback still on). Token-carrying runs are
	// token-only: the static bearer never reaches their workflow env.
	APIToken string //nolint:gosec // G117: driver API bearer token intentionally forwarded to legacy runtimes.
	// Launcher launches the workflow-bundle runtime (SB1 sandbox seam).
	// Nil means the default local node-process launcher — today's
	// flue-local behavior, unchanged.
	Launcher SandboxLauncher
}

func (r NodeRunner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	node := processNodePath(r.NodePath)
	payload := req.Run.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return RunResult{}, fmt.Errorf("driver payload is invalid JSON: %w", domain.ErrInvalid)
	}
	if req.ServerPath == "" {
		return RunResult{}, fmt.Errorf("native Flue server path required: %w", domain.ErrInvalid)
	}
	return r.runBuiltFlueServer(ctx, req, node, payload)
}

func (r NodeRunner) runBuiltFlueServer(ctx context.Context, req RunRequest, node string, input []byte) (RunResult, error) {
	env, err := r.runtimeEnv(req, input)
	if err != nil {
		return RunResult{}, err
	}
	launcher := r.Launcher
	if launcher == nil {
		launcher = processLauncher{NodePath: node}
	}
	// SB3 trust placement policy: an untrusted bundle never launches outside
	// an isolating sandbox — the run fails sandbox_required with no process
	// spawned, never a silent fallback.
	if refusal, refused := sandbox.RefuseUntrustedPlacement(req, launcher); refused {
		return refusal, nil
	}
	process, err := launcher.Launch(ctx, LaunchSpec{
		BundleRoot: req.BundleRoot,
		ServerPath: req.ServerPath,
		WorkDir:    req.BundleRoot,
		Env:        env,
		Manifest:   req.Manifest,
		TrustLevel: req.TrustLevel,
	})
	if err != nil {
		return RunResult{}, err
	}
	exit, waitErr := process.Wait()
	result := flueRuntimeResult(ctx, req, exit.Stdout, exit.Stderr, waitErr)
	sandbox.RecordSandboxPlacement(&result, process.Placement())
	sandbox.RecordTrustPlacementDecision(&result, req.TrustLevel, sandbox.LauncherPlacementProvider(launcher))
	return result, nil
}

// runtimeEnv assembles the complete workflow runtime environment: the
// identity/auth env from flueRuntimeEnv plus the driver-op API endpoint. The
// node-wide static bearer is cross-run authority, so token-carrying runs drop
// it (workflow calls are token-only per the step-9 locked decision) unless
// the deprecated LegacyDriverAuthEnvVar fallback is still on.
func (r NodeRunner) runtimeEnv(req RunRequest, input []byte) ([]string, error) {
	execTaskCommand, err := r.execTaskCommand()
	if err != nil {
		return nil, err
	}
	env, err := flueRuntimeEnv(req, input, execTaskCommand)
	if err != nil {
		return nil, err
	}
	apiBaseURL := strings.TrimSpace(r.APIBaseURL)
	if apiBaseURL == "" {
		return env, nil
	}
	env = append(env, "LOOM_DRIVER_API_URL="+apiBaseURL)
	if apiToken := strings.TrimSpace(r.APIToken); apiToken != "" && legacyDriverAuthEnv(req) {
		env = append(env, "LOOM_DRIVER_API_TOKEN="+apiToken)
	}
	return env, nil
}

func flueRuntimeEnv(req RunRequest, input []byte, execTaskCommand []string) ([]string, error) {
	env := driverRuntimeBaseEnv(os.Environ())
	env = append(env,
		"LOOM_DRIVER_WORKSPACE="+req.Run.WorkspaceKey,
		"LOOM_DRIVER_RUN_ID="+req.Run.RunID,
		"LOOM_DRIVER_NODE_ID="+req.Run.NodeID,
	)
	// Lease identity doubles as auth material under header-quad auth, so a
	// token-carrying run keeps it out of the workflow env (§9.5 lockdown:
	// blast radius = one run x one lease TTL) unless the deprecated
	// LegacyDriverAuthEnvVar fallback is still on.
	if legacyDriverAuthEnv(req) {
		env = append(env,
			"LOOM_DRIVER_LEASE_ID="+req.Run.LeaseID,
			fmt.Sprintf("LOOM_DRIVER_FENCING_TOKEN=%d", req.Run.FencingToken),
		)
	}
	env = append(env,
		"LOOM_FLUE_SERVER_PATH="+req.ServerPath,
		"LOOM_FLUE_BUNDLE_ROOT="+req.BundleRoot,
		"LOOM_FLUE_WORKFLOW_NAME="+workflowName(req),
		"LOOM_FLUE_INVOKE_PAYLOAD="+string(input),
	)
	// Run-scoped bearer token (LOOM_RUN_TOKEN), minted at claim time. The
	// parent-env filter strips any inherited *TOKEN* variable, so the only
	// token a workflow process ever sees is the one minted for its own run.
	if token := strings.TrimSpace(req.RunToken); token != "" {
		env = append(env, "LOOM_RUN_TOKEN="+token)
	}
	if len(execTaskCommand) > 0 {
		encoded, err := json.Marshal(execTaskCommand)
		if err != nil {
			return nil, fmt.Errorf("encode exec-task command: %w", err)
		}
		env = append(env, "LOOM_DRIVER_EXEC_TASK_CMD_JSON="+string(encoded))
	}
	return env, nil
}

func flueRuntimeResult(ctx context.Context, req RunRequest, stdout, stderr string, runErr error) RunResult {
	if runErr != nil {
		return failedFlueRuntimeResult(ctx, req, stdout, stderr, runErr)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		return invalidDriverResult(req, "Flue workflow returned no result", stdout, stderr)
	}
	lines := strings.Split(out, "\n")
	var payload struct {
		Status     domain.DriverRunStatus `json:"status"`
		Summary    string                 `json:"summary"`
		ErrorClass string                 `json:"errorClass"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		return invalidDriverResult(req, fmt.Sprintf("decode Flue runtime result: %v", err), stdout, stderr)
	}
	result := RunResult{Status: payload.Status, Summary: payload.Summary, ErrorClass: payload.ErrorClass, Output: flueRunOutput(req, stdout, stderr)}
	if result.Status == domain.DriverRunFailed {
		result.Summary = flueFailedSummary(result.Summary, stderr)
	}
	return requireExplicitTerminalRunResult(result)
}

func failedFlueRuntimeResult(ctx context.Context, req RunRequest, stdout, stderr string, runErr error) RunResult {
	if ctx.Err() != nil {
		return RunResult{
			Status:     domain.DriverRunCancelled,
			Summary:    "Flue local runner cancelled",
			ErrorClass: "driver_cancelled",
			Output:     flueRunOutput(req, stdout, stderr),
		}
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = runErr.Error()
	}
	return RunResult{Status: domain.DriverRunFailed, Summary: msg, ErrorClass: "driver_runtime", Output: flueRunOutput(req, stdout, stderr)}
}

func flueFailedSummary(summary, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		return summary
	}
	if summary != "" {
		return summary + ": " + detail
	}
	return detail
}

func requireExplicitTerminalRunResult(result RunResult) RunResult {
	if result.Status.IsTerminal() {
		return result
	}
	if result.Status == domain.DriverRunSuspendedAwaitingEvent {
		// A suspended report is the runner's clean exit after an await op
		// suspended the run server-side (AW9/AW11): not a terminal result and
		// not an error — settleClaimed acknowledges it without a Finish.
		return result
	}
	detail := "driver result missing terminal status"
	if result.Status != "" {
		detail = fmt.Sprintf("driver result status %q is not terminal", result.Status)
	}
	summary := detail
	if existing := strings.TrimSpace(result.Summary); existing != "" {
		summary += ": " + existing
	}
	result.Status = domain.DriverRunFailed
	result.Summary = summary
	result.ErrorClass = "invalid_driver_result"
	return result
}

func invalidDriverResult(req RunRequest, summary, stdout, stderr string) RunResult {
	return RunResult{
		Status:     domain.DriverRunFailed,
		Summary:    summary,
		ErrorClass: "invalid_driver_result",
		Output:     flueRunOutput(req, stdout, stderr),
	}
}

func flueRunOutput(req RunRequest, stdout, stderr string) map[string]string {
	output := map[string]string{
		"runtime":  RuntimeFlueNode,
		"logs_ref": "driver-run://" + req.Run.RunID + "/flue-local",
	}
	if req.Manifest["artifact_kind"] != "" {
		output["artifact_kind"] = req.Manifest["artifact_kind"]
	}
	if req.Manifest["workflow_name"] != "" {
		output["workflow_name"] = req.Manifest["workflow_name"]
	}
	if tail := textTail(stderr, 4096); tail != "" {
		output["flue_stderr_tail"] = tail
	}
	if tail := textTail(stdout, 4096); tail != "" {
		output["flue_stdout_tail"] = tail
	}
	if count := nonEmptyLineCount(stdout) + nonEmptyLineCount(stderr); count > 0 {
		output["flue_event_count"] = strconv.Itoa(count)
	}
	return output
}

func textTail(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	return value[len(value)-maxBytes:]
}

func nonEmptyLineCount(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r NodeRunner) execTaskCommand() ([]string, error) {
	if len(r.ExecTaskCommand) > 0 {
		return append([]string(nil), r.ExecTaskCommand...), nil
	}
	if os.Getenv("LOOM_DRIVER_EXEC_TASK_CMD_JSON") != "" || os.Getenv("LOOM_DRIVER_EXEC_TASK_CMD") != "" {
		return nil, nil
	}
	executable, err := os.Executable()
	if err == nil && executable != "" {
		return []string{executable}, nil
	}
	if os.Args[0] != "" {
		return []string{os.Args[0]}, nil
	}
	return nil, fmt.Errorf("resolve exec-task command: %w", err)
}

func workflowName(req RunRequest) string {
	if req.Manifest["workflow_name"] != "" {
		return req.Manifest["workflow_name"]
	}
	if req.Run != nil && req.Run.DriverID != "" {
		return req.Run.DriverID
	}
	return EntrypointRun
}

// processNodePath resolves the Node executable for host-process Flue runtimes.
// An explicit caller override wins. Packaged desktop builds place a pinned Node
// runtime under Contents/Resources/runtime, so prefer that copy before the
// legacy executable sibling and PATH fallbacks used by CLI/development installs.
func processNodePath(override string) string {
	executable, _ := os.Executable()
	return resolveProcessNodePath(override, executable)
}

func resolveProcessNodePath(override, executable string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	if executable = strings.TrimSpace(executable); executable != "" {
		executableDir := filepath.Dir(executable)
		for _, name := range []string{"node", "node.exe"} {
			candidates := []string{
				filepath.Clean(filepath.Join(executableDir, "..", "Resources", "runtime", name)),
				filepath.Join(executableDir, name),
			}
			for _, candidate := range candidates {
				if isProcessExecutable(candidate) {
					return candidate
				}
			}
		}
	}
	return "node"
}

func isProcessExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}
