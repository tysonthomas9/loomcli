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

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
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
	patch, baseRef, err := applyTaskRunnerResult(raw, collector)
	if err != nil {
		return err
	}
	// Phase-U fix: the default local TS leaf runs the agent in an ISOLATED worktree and returns the
	// change as a patch+base_ref, leaving the daemon's HOST worktree clean. The Go leaf commits in
	// place, so the worker's session finalize (`git diff beforeRef..HEAD`) captures its change; the TS
	// leaf must apply that patch back onto the host worktree and commit, or finalize records
	// files_changed=0 and serve surfaces no diff. (Daytona delivers via its own PR/sandbox path —
	// it returns no top-level patch, so this is a no-op for the Daytona entrypoint.)
	if entrypoint == driver.LocalTaskRunnerEntrypoint {
		applyLeafPatchBack(ctx, workDir, baseRef, patch, taskRunID)
	}
	return nil
}

// applyTaskRunnerResult decodes the bundled task-runner's result, feeds usage into
// the daemon collector, mirrors the transcript onto the session, and maps a
// non-completed run to an error. It also returns the runner's produced patch +
// base_ref (empty for PR/stacked delivery) so the caller can patch it back onto the
// host worktree (see applyLeafPatchBack).
func applyTaskRunnerResult(raw json.RawMessage, collector *usage.Collector) (patch string, baseRef string, err error) {
	var result struct {
		Status            string            `json:"status"`
		ErrorMessage      string            `json:"errorMessage"`
		InputTokens       int64             `json:"input_tokens"`
		OutputTokens      int64             `json:"output_tokens"`
		CacheReadTokens   int64             `json:"cache_read_tokens"`
		CacheWriteTokens  int64             `json:"cache_write_tokens"`
		TranscriptEntries []json.RawMessage `json:"transcript_entries"`
		Patch             string            `json:"patch"`
		BaseRef           string            `json:"base_ref"`
		PatchBaseRef      string            `json:"patch_base_ref"`
	}
	if jerr := json.Unmarshal(raw, &result); jerr != nil {
		return "", "", fmt.Errorf("ts-runtime: decode runner result: %w", jerr)
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
		return "", "", fmt.Errorf("ts-runtime run did not complete: %s", msg)
	}
	br := result.BaseRef
	if strings.TrimSpace(br) == "" {
		br = result.PatchBaseRef
	}
	return result.Patch, br, nil
}

// applyLeafPatchBack lands the local TS runner's produced patch onto the daemon's host
// worktree and commits it, so the worker's session finalize (`git diff beforeRef..HEAD`)
// records the change exactly as the Go leaf does (the agent committed in place there).
// Best-effort + loud: the run already "completed", so a patch-back failure is a delivery
// gap to surface on stderr, not a reason to fail the agent — and the resulting empty diff
// will fail any downstream parity check rather than passing silently.
func applyLeafPatchBack(ctx context.Context, workDir, baseRef, patch, taskID string) {
	if strings.TrimSpace(patch) == "" {
		return // no change produced (or PR/stacked delivery) — nothing to patch back
	}
	if strings.TrimSpace(baseRef) == "" {
		fmt.Fprintln(os.Stderr, "[ts-leaf] runner returned a patch but no base_ref; host worktree change not recorded")
		return
	}
	res, err := driver.ApplyPatchBack(ctx, driver.PatchBackOptions{
		WorktreePath: workDir,
		BaseRef:      baseRef,
		Patch:        []byte(patch),
		// Stage only the patch's files (so the commit is the agent's change, not the monitor's
		// .agent.lock churn) and drop the monitor bookkeeping that the runner captured and that
		// exists in the host worktree too (it would otherwise conflict on apply).
		Index:   true,
		Exclude: []string{".agent.lock", ".agent.lock.flock"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ts-leaf] patch-back error: %v\n", err)
		return
	}
	if !res.Applied {
		fmt.Fprintf(os.Stderr, "[ts-leaf] patch-back not applied (status=%s): %s\n", res.Status, res.ErrorMessage)
		return
	}
	if err := driver.CommitWorktree(ctx, workDir, "loom: ts-leaf "+taskID); err != nil {
		fmt.Fprintf(os.Stderr, "[ts-leaf] commit after patch-back failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[ts-leaf] applied runner patch to host worktree and committed (base %s)\n", strings.TrimSpace(baseRef))
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
	// Resolve the session via the active-session runtime env (set at worker startup
	// from the supervisor-assigned LOOM_SESSION_ID, backend_session_env.go) — the
	// same handle the Go leaf's hook dispatch uses — falling back to the env + the
	// workspace runtime dir. Route the entries through the canonical
	// SyncNativeTranscript (gitleaks/entropy redaction + owned-session-dir placement)
	// rather than a raw write, so the TS leaf captures its transcript at redaction
	// parity with the Go leaf. serve reads it back through the canonical fallback in
	// sessions.LoadNativeEvents, since these entries are already in transcript.Event
	// form (the daemon TS-leaf surfacing fix lives on the read side, not here).
	runtimeDir, sid := backends.GetActiveSessionRuntimeEnv()
	if sid == "" {
		runtimeDir, sid = cli.GetWorkspaceRuntimeDir(), os.Getenv("LOOM_SESSION_ID")
	}
	if sid == "" || runtimeDir == "" {
		return
	}
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		return
	}
	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(e)
		buf.WriteByte('\n')
	}
	// Stage to a temp file and route through the canonical SyncNativeTranscript
	// (redaction + owned-session-dir placement) instead of a raw NativeTranscriptPath
	// write, so serve resolves it via session ownership (LoadMetadata →
	// LoadNativeEvents) — identical to the Go leaf's hook-dispatched capture.
	tmp, err := os.CreateTemp("", "loom-ts-leaf-transcript-*.jsonl")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, werr := tmp.Write(buf.Bytes()); werr != nil {
		_ = tmp.Close()
		return
	}
	if cerr := tmp.Close(); cerr != nil {
		return
	}
	_ = store.SyncNativeTranscript(sid, tmp.Name())
}
