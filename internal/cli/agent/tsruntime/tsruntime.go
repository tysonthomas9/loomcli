package tsruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

// Phase U — execution-leaf unification. When LOOM_DAEMON_LEAF=ts, the daemon's
// execution leaf runs the agent on the TS runtime: it delegates the backend run
// to the bundled TypeScript task-runner (the same runner the driver host-bridge
// uses) instead of the Go local-agent path, so both planes share ONE execution +
// telemetry path. The Go daemon SUPERVISOR is untouched — it still spawns
// `loom <role> --daemon-mode`; only the leaf's internal execution changes.
// Default-off: the Go path remains the default.

// enabled reports whether the daemon's execution leaf should run on the
// TS runtime (the bundled TypeScript task-runner) rather than the Go path.
func enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LOOM_DAEMON_LEAF")), "ts")
}

// Invoker returns a TS-runtime-delegating AgentInvoker when
// LOOM_DAEMON_LEAF=ts, wrapping the supplied fallback (the Go invoker) for the
// interactive path and as a safety net; otherwise it returns the fallback
// unchanged. Decorator style mirrors wrapAgentInvokerWithTracing.
func Invoker(fallback cli.AgentInvoker) cli.AgentInvoker {
	if enabled() {
		return agentInvoker{fallback: fallback}
	}
	return fallback
}

// taskRunnerEntrypoint selects which bundled task-runner the TS runtime delegates
// to. Default is the local-task-runner (local execution). Set
// LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner to run the task inside a Daytona
// sandbox (requires DAYTONA_API_KEY + a reachable repo URL); the same bundle ships
// both runners, so this is just an entrypoint switch.
func taskRunnerEntrypoint() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_DAEMON_LEAF_RUNNER")); v != "" {
		return v
	}
	return driver.LocalTaskRunnerEntrypoint
}

// daytonaRepoURL returns an explicit DAYTONA_REPO_URL, or the worktree's origin
// remote URL, for the Daytona runner to clone. Local filesystem paths are skipped:
// a cloud sandbox can only clone a network-reachable URL.
func daytonaRepoURL(workDir string) string {
	if v := strings.TrimSpace(os.Getenv("DAYTONA_REPO_URL")); v != "" {
		return v
	}
	out, err := exec.Command("git", "-C", workDir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if url == "" || strings.HasPrefix(url, "/") || strings.HasPrefix(url, "file:") {
		return ""
	}
	return url
}

// agentInvoker is the cli.AgentInvoker that runs a non-interactive
// daemon-leaf agent on the TS runtime (the bundled task-runner), deferring
// interactive runs to the Go fallback.
//
// Tracing note: Deps.Agent is already the tracing-wrapped registry invoker, and
// this type wraps THAT as its fallback — so the TS-runtime path itself runs
// OUTSIDE the per-invoke tracing span (only the Go fallback is traced). If the TS
// path needs its own span, wrap here or trace inside InvokeNonInteractive.
type agentInvoker struct{ fallback cli.AgentInvoker }

func (i agentInvoker) InvokeInteractive(workDir, prompt, agentName string) error {
	// Interactive runs are not part of the daemon leaf; defer to the Go path.
	return i.fallback.InvokeInteractive(workDir, prompt, agentName)
}

func (i agentInvoker) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	serverPath, err := taskRunnerBundleServerPath()
	if err != nil {
		return fmt.Errorf("ts-runtime: materialize bundle: %w", err)
	}
	backend := cli.GetBackendName()
	leaseToken := os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN")
	entrypoint := taskRunnerEntrypoint()
	taskRunID := strings.TrimSpace(os.Getenv("LOOM_TASK_RUN_ID"))
	if taskRunID == "" {
		taskRunID = "tr-" + agentName
	}
	input := map[string]any{}
	if entrypoint == driver.DaytonaTaskRunnerEntrypoint {
		// The Daytona runner clones a network-reachable git URL into the sandbox.
		// Prefer an explicit DAYTONA_REPO_URL; otherwise fall back to the worktree's
		// origin remote so a workspace with a reachable remote works out of the box.
		if repoURL := daytonaRepoURL(workDir); repoURL != "" {
			input["repoUrl"] = repoURL
		}
	}
	req := map[string]any{
		"task_run_id":       taskRunID,
		"task_id":           os.Getenv("LOOM_ASSIGNED_TASK_ID"),
		"backend":           backend,
		"workspace_key":     os.Getenv("LOOM_WORKSPACE"),
		"lease_token":       leaseToken,
		"runner_entrypoint": entrypoint,
		"input":             input,
	}
	reqJSON, _ := json.Marshal(req)

	ctx, cancel := contextFromShutdown(shutdown)
	defer cancel()

	raw, err := driver.RunBundledTaskRunner(ctx, driver.BundledRunnerOptions{
		ServerPath:   serverPath,
		Entrypoint:   entrypoint,
		Worktree:     workDir,
		Backend:      backend,
		Prompt:       prompt,
		RequestJSON:  string(reqJSON),
		LeaseToken:   leaseToken,
		StreamStderr: true,
		Stderr:       os.Stderr,
	})
	if err != nil {
		return err
	}
	return applyTaskRunnerResult(raw, collector)
}

// applyTaskRunnerResult decodes the bundled task-runner's result, feeds usage into
// the daemon collector, mirrors the transcript onto the session, and maps a
// non-completed run to an error.
func applyTaskRunnerResult(raw json.RawMessage, collector *usage.Collector) error {
	var result struct {
		Status            string            `json:"status"`
		ErrorMessage      string            `json:"errorMessage"`
		InputTokens       int64             `json:"input_tokens"`
		OutputTokens      int64             `json:"output_tokens"`
		CacheReadTokens   int64             `json:"cache_read_tokens"`
		CacheWriteTokens  int64             `json:"cache_write_tokens"`
		TranscriptEntries []json.RawMessage `json:"transcript_entries"`
	}
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		return fmt.Errorf("ts-runtime: decode runner result: %w", jerr)
	}

	// Feed the runner's usage into the daemon collector so the worker's own
	// finalize path records it exactly as the Go leaf does — keeping the TS leaf
	// at telemetry parity with the Go leaf.
	//
	// KNOWN PARITY GAP (pre-existing, affects the Go leaf identically): in daemon
	// mode the worker is reaped right after the agent signals `loom complete`, so
	// the worker's collector-aware finalizeAgentSession does not run; the
	// supervisor's collector-less session finalize (supervisor/session_finalize.go)
	// is what lands, leaving session-metadata tokens at 0 on BOTH leaves. The TS
	// leaf additionally returns usage on its result JSON, which positions a
	// follow-up to carry usage into the supervisor finalize.
	if collector != nil {
		collector.Accumulate("", result.InputTokens, result.OutputTokens, result.CacheReadTokens, result.CacheWriteTokens)
	}
	// Surface the transcript on the session (best-effort; the entries are canonical
	// transcript.Event, pinned by the Phase-U/U0 conformance test).
	writeTaskRunnerNativeTranscript(result.TranscriptEntries)

	if result.Status != "completed" {
		msg := result.ErrorMessage
		if msg == "" {
			msg = result.Status
		}
		return fmt.Errorf("ts-runtime run did not complete: %s", msg)
	}
	return nil
}

var (
	taskRunnerBundleOnce sync.Once
	taskRunnerServerPath string
	taskRunnerBundleErr  error
)

// taskRunnerBundleServerPath materializes the bundled task-runner once per process
// to a stable per-workspace path and returns its server.mjs.
func taskRunnerBundleServerPath() (string, error) {
	taskRunnerBundleOnce.Do(func() {
		dest := filepath.Join(cli.GetWorkspaceRuntimeDir(), "ts-runtime-bundle", "dist")
		taskRunnerServerPath, taskRunnerBundleErr = workflows.MaterializeBuiltinBundle("epic-runner", dest)
	})
	return taskRunnerServerPath, taskRunnerBundleErr
}

// contextFromShutdown bridges the daemon leaf's shutdown channel to a context the
// runner invocation can cancel on (Ctrl-C / supervisor yield).
func contextFromShutdown(shutdown <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if shutdown != nil {
		go func() {
			select {
			case <-shutdown:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}

func writeTaskRunnerNativeTranscript(entries []json.RawMessage) {
	if len(entries) == 0 {
		return
	}
	sid := os.Getenv("LOOM_SESSION_ID")
	if sid == "" {
		return
	}
	store, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return
	}
	path := store.NativeTranscriptPath(sid)
	if path == "" {
		return
	}
	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(e)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// Atomic write so a crash mid-write can't leave a truncated transcript (the
	// canonical SyncNativeTranscript path uses atomicfile too).
	_ = atomicfile.WriteFile(path, buf.Bytes(), 0o644)
}
