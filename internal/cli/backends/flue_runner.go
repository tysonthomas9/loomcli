package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/flue"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// envFlueSandbox selects where flue agents execute: "local" (host worktree,
// default) or "daytona" (a fresh remote Daytona sandbox per task, with the
// remote changes patch-synced back into the local worktree). See
// docs/design/flue-daytona-runtime-proposal.md (Phase 2).
const envFlueSandbox = "LOOM_FLUE_SANDBOX"

// resolveFlueSandbox returns the configured sandbox mode ("local"|"daytona").
func resolveFlueSandbox() string {
	if s := strings.ToLower(strings.TrimSpace(os.Getenv(envFlueSandbox))); s != "" {
		return s
	}
	return "local"
}

// runnerInput is the JSON contract passed to the flue runner workflow.
type runnerInput struct {
	Sandbox       string `json:"sandbox"` // "local" | "daytona-task"
	Prompt        string `json:"prompt"`
	Model         string `json:"model,omitempty"`
	RepoRemoteURL string `json:"repo_remote_url,omitempty"`
	RepoBranch    string `json:"repo_branch,omitempty"`
	BaseRef       string `json:"base_ref,omitempty"`
	PatchOut      string `json:"patch_out,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	SyncStrategy  string `json:"sync_strategy,omitempty"` // "patch-back" (default) | "branch-push" | "epic-branch"
	// EpicBranch is the shared branch the runner commits onto for the
	// epic-branch strategy (every epic task builds on the prior task's commit).
	EpicBranch string `json:"epic_branch,omitempty"`
	// FetchTask tells the runner to pull the task's title/description/design/AC
	// from loom serve itself via @loom/sdk (getTask), instead of loom inlining
	// them into Prompt. Set only when a reachable loom serve + bootstrap is
	// available; otherwise Prompt carries the inlined task (fallback). See
	// docs/product/loom-typescript-sdk-spec.md (Phase B).
	FetchTask bool `json:"fetch_task,omitempty"`
	// CloseTask tells the runner to close/advance the task via the SDK on success.
	// The close policy itself lives in the runner.ts completeRun() (customizable).
	CloseTask bool `json:"close_task,omitempty"`
}

// Sync strategies for how a daytona-task result returns to loom. loom forwards
// the chosen name VERBATIM (LOOM_FLUE_SYNC) and does NOT interpret it — the
// strategy set + behavior live in the flue runner (runner.ts SYNC_STRATEGIES),
// so adding one needs no change here. For reference, the runner ships:
//   - patch-back (default): runner emits a diff; loom applies it to the local
//     worktree + commits/pushes (the one step that must stay host-side).
//   - branch-push (PRD Phase D): runner commits + pushes a per-task branch +
//     registers a "commit" Artifact; no host patch.
//   - epic-branch: runner commits onto a SHARED epic branch (LOOM_FLUE_EPIC_BRANCH)
//     so an epic's tasks accumulate on one integration branch; no host patch.
const (
	syncStrategyPatchBack  = "patch-back"
	syncStrategyBranchPush = "branch-push"
	syncStrategyEpicBranch = "epic-branch"
)

// resolveFlueSyncStrategy returns the runner's sync-strategy name from
// LOOM_FLUE_SYNC, normalized to lower-case but otherwise VERBATIM (default
// patch-back). loom does not validate it against a known set — the runner owns
// the registry, so a new strategy works with no change here.
func resolveFlueSyncStrategy() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_FLUE_SYNC"))); v != "" {
		return v
	}
	return syncStrategyPatchBack
}

// resolveFlueEpicBranch returns LOOM_FLUE_EPIC_BRANCH (the shared branch the
// epic-branch strategy commits onto), forwarded verbatim to the runner; empty
// otherwise.
func resolveFlueEpicBranch() string {
	return strings.TrimSpace(os.Getenv("LOOM_FLUE_EPIC_BRANCH"))
}

// runnerEvent is one LOOMRUNNER NDJSON event line emitted by the runner.
type runnerEvent struct {
	Type         string `json:"type"`
	Provider     string `json:"provider,omitempty"`
	SandboxID    string `json:"sandbox_id,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Commit       string `json:"commit,omitempty"`
	Path         string `json:"path,omitempty"`
	FilesChanged int    `json:"files_changed,omitempty"`
	InputTokens  int64  `json:"input_tokens,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
	Status       string `json:"status,omitempty"`
	Error        string `json:"error,omitempty"`
	Cleanup      string `json:"cleanup,omitempty"` // "deleted" | "retained"
}

const runnerEventPrefix = "LOOMRUNNER "

// runnerResult accumulates the structured events seen on the runner's output.
type runnerResult struct {
	provider  string
	sandboxID string
	remoteCwd string
	patchPath string
	status    string
	errMsg    string
	cleanup   string // "deleted" | "retained"
}

// handleLine parses one output line; LOOMRUNNER events update the result and
// feed usage into the collector.
func (r *runnerResult) handleLine(line string, collector *usage.Collector) {
	if !strings.HasPrefix(line, runnerEventPrefix) {
		return
	}
	var ev runnerEvent
	if json.Unmarshal([]byte(strings.TrimPrefix(line, runnerEventPrefix)), &ev) != nil {
		return
	}
	switch ev.Type {
	case "sandbox_created":
		r.provider, r.sandboxID, r.remoteCwd = ev.Provider, ev.SandboxID, ev.Cwd
	case "patch_ready":
		r.patchPath = ev.Path
	case "usage":
		if collector != nil {
			collector.Accumulate("", ev.InputTokens, ev.OutputTokens, 0, 0)
		}
	case "final":
		r.status, r.errMsg, r.cleanup = ev.Status, ev.Error, ev.Cleanup
		if ev.SandboxID != "" {
			r.sandboxID = ev.SandboxID
		}
	}
}

// runFlueDaytonaTask runs one agent turn in a fresh Daytona sandbox via the
// flue runner, then patch-syncs the remote changes back into the local
// worktree so loom's existing finalizer sees them. (Proposal Phase 2.)
func runFlueDaytonaTask(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	input, err := deriveDaytonaInput(workDir, prompt, agentName)
	if err != nil {
		return err
	}

	// The sandbox agent has no loom CLI, so it can't `loom claim` the task it
	// works on — record the claim host-side (best-effort) so automode's
	// agentClaimedTask() sees the run as progress (`--auto -m N` stops correctly
	// instead of looping to the no-progress backstop) and the monitor shows the
	// task. A local agent does this itself via `loom claim`.
	if taskID := resolveAssignedTaskID(workDir); taskID != "" {
		_ = cli.UpdateLockTask(workDir, taskID, "")
	}

	// Always give the runner a patch path; only the patch-back strategy writes to
	// it (branch-push / epic-branch push from the sandbox and leave it empty). loom
	// then applies the patch iff one was produced — so loom stays agnostic to which
	// strategy ran.
	patchOut, cleanup, err := tempPatchFile()
	if err != nil {
		return err
	}
	defer cleanup()
	input.PatchOut = patchOut

	fmt.Println("Launching flue agent in a Daytona sandbox (daytona-task)...")
	fmt.Println("")

	res, runErr := flueRunnerExec(context.Background(), workDir, agentName, input, shutdown, collector)
	recordSandboxMetadata(res, input.BaseRef, input.SyncStrategy)
	if runErr != nil {
		return runErr
	}
	if res.status != "completed" {
		if res.errMsg != "" {
			return fmt.Errorf("flue daytona-task failed (sandbox %s retained): %s", res.sandboxID, res.errMsg)
		}
		return fmt.Errorf("flue daytona-task did not report completion")
	}

	// Patch-sync (patch-back only — it's the sole strategy that produces a patch):
	// apply the remote changes into the local worktree, then commit + push so the
	// work lands "back in loom" the way a local agent's own git commit/push would
	// (the remote agent can't touch the local repo). branch-push / epic-branch push
	// from the sandbox and write no patch, so this is skipped for them.
	if hasPatch(res.patchPath) {
		if err := applyPatch(workDir, res.patchPath); err != nil {
			return fmt.Errorf("flue daytona-task patch_apply_failed (sandbox %s retained): %w", res.sandboxID, err)
		}
		fmt.Printf("[loom] applied Daytona patch from sandbox %s into %s\n", res.sandboxID, workDir)
		if err := pushWorktreeBack(workDir, agentName, res.sandboxID); err != nil {
			fmt.Printf("[loom] warning: could not commit Daytona work back to loom: %v\n", err)
		}
	}
	fmt.Printf("[loom] flue daytona-task complete (sandbox=%s remote_cwd=%s cleanup=%s)\n", res.sandboxID, res.remoteCwd, res.cleanup)
	return nil
}

// pushWorktreeBack commits the patch-synced changes in the local worktree and
// pushes them, so a remote-sandbox run lands back in loom the same way a local
// agent's git commit + push (task prompt Step 9) does. Commit is required (the
// work must be durable in loom's git); push is best-effort — a missing remote
// credential must not fail the task, since the work is already committed locally.
func pushWorktreeBack(workDir, agentName, sandboxID string) error {
	// applyPatch staged exactly the sandbox's changes (git apply --index), so the
	// index holds the agent's work and nothing else — deliberately NOT `git add
	// -A`, which would also sweep in loom's own runtime files (.agent.lock,
	// sessions/, usage.jsonl). Empty index → the run produced no changes.
	if _, err := gitOutput(workDir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}
	// Commit with an explicit loom identity: agent worktrees may not have
	// user.name/email configured, which would fail `git commit` with exit 128.
	msg := fmt.Sprintf("loom: apply flue daytona-task work (agent %s, sandbox %s)", agentName, sandboxID)
	if out, err := gitCombined(workDir, "-c", "user.name=loom", "-c", "user.email=loom@localhost", "commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %v\n%s", err, out)
	}
	fmt.Printf("[loom] committed Daytona work in %s\n", workDir)
	if out, err := gitCombined(workDir, "push", "origin", "HEAD"); err != nil {
		fmt.Printf("[loom] push origin HEAD failed (work is committed locally): %v\n%s", err, out)
		return nil
	}
	fmt.Println("[loom] pushed Daytona work to origin HEAD")
	return nil
}

// deriveDaytonaInput builds the runner input from the local worktree's git
// state. A remote is required: the sandbox hydrates by cloning it at base_ref.
func deriveDaytonaInput(workDir, prompt, agentName string) (runnerInput, error) {
	remote, _ := gitOutput(workDir, "remote", "get-url", "origin")
	if remote == "" {
		return runnerInput{}, fmt.Errorf("flue backend: sandbox=daytona requires a git remote on %s (push the branch first)", workDir)
	}
	baseRef, _ := gitOutput(workDir, "rev-parse", "HEAD")
	branch, _ := gitOutput(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	in := runnerInput{
		Sandbox:       "daytona-task",
		Model:         resolveFlueModel(),
		RepoRemoteURL: remote,
		RepoBranch:    branch,
		BaseRef:       baseRef,
		TaskID:        agentName,
		SyncStrategy:  resolveFlueSyncStrategy(),
		EpicBranch:    resolveFlueEpicBranch(), // forwarded verbatim; the runner decides how to use it
	}
	// PRD Phase B: when loom serve is reachable (bootstrap present), the runner
	// — which runs on the host — fetches the task content itself via @loom/sdk,
	// so we send only the sandbox preamble (env guidance). Otherwise we inline
	// the task's design into the prompt as before (the no-server fallback).
	if sandboxReadPathAvailable(workDir) {
		in.FetchTask = true
		in.Prompt = daytonaSandboxPreamble
		in.CloseTask = flueCloseTaskEnabled()
	} else {
		in.Prompt = buildSandboxPrompt(workDir, prompt)
	}
	return in, nil
}

// flueCloseTaskEnabled reports whether a successful SDK-path run should close its
// task (LOOM_FLUE_CLOSE_TASK; default on, disable with 0/false/no/off). The close
// policy itself lives in the runner.ts completeRun() so it stays customizable.
func flueCloseTaskEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_FLUE_CLOSE_TASK"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// sandboxReadPathAvailable reports whether loom can hand the runner a usable
// bootstrap for the SDK read path: a reachable loom serve URL, the workspace,
// and a resolvable task id. All arrive via LOOM_* env (allowlisted by the
// LOOM_ prefix) which the runner reads through @loom/sdk's bootstrapFromEnv.
func sandboxReadPathAvailable(workDir string) bool {
	if strings.TrimSpace(os.Getenv("LOOM_SERVER_URL")) == "" {
		return false
	}
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		workspace = strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_ID"))
	}
	if workspace == "" {
		return false
	}
	return resolveAssignedTaskID(workDir) != ""
}

// resolveAssignedTaskID resolves the claimed task id for this run: the daemon
// injects it as LOOM_ASSIGNED_TASK_ID; single-task runs record it in the
// worktree lock. Returns "" when neither is present.
func resolveAssignedTaskID(workDir string) string {
	if taskID := strings.TrimSpace(os.Getenv("LOOM_ASSIGNED_TASK_ID")); taskID != "" {
		return taskID
	}
	if info, err := cli.ReadLockFile(workDir); err == nil {
		return info.TaskID
	}
	return ""
}

// daytonaSandboxPreamble reframes loom's task prompt for execution inside a
// fresh remote sandbox. loom's task prompts assume the `loom` CLI and the
// host's local worktree paths are present (for `loom data close`, `loom
// complete`, committing, etc.) — none of which exist in the sandbox. Without
// this the agent refuses ("loom is not installed... repo path does not
// exist"). The agent's only job in the sandbox is the code change; loom
// captures the resulting diff on the host and handles all task bookkeeping.
const daytonaSandboxPreamble = "You are running inside an isolated sandbox that contains a fresh clone of " +
	"the repository, checked out at the current working directory. The following rules OVERRIDE any " +
	"conflicting instructions below:\n" +
	"- The `loom` and `bd` CLIs are NOT installed here, and the host's local workspace paths do NOT exist. " +
	"Do not run `loom`/`bd` commands, do not attempt to select/claim/close/complete/signal tasks, and do " +
	"not run `git commit`, `git push`, or open a PR.\n" +
	"- Simply implement the requested change by creating/editing files in the current working directory. " +
	"loom captures your file changes as a diff on the host and handles all task bookkeeping, commits, and " +
	"pushes for you.\n" +
	"- Stay within the current repository directory.\n\n" +
	"--- TASK ---\n\n"

// buildSandboxPrompt produces the prompt for the sandbox agent. loom's task
// prompts assume the `loom` CLI is present to *discover* the claimed task's
// design — but the sandbox has no `loom` CLI, so the agent can't fetch it and
// makes no changes. We inline the claimed task's title/description/design
// (fetched on the host via the lock file + issue backend) so the sandbox agent
// is self-sufficient. Falls back to the original prompt if no task is resolvable.
func buildSandboxPrompt(workDir, fallback string) string {
	taskID := resolveAssignedTaskID(workDir)
	if taskID == "" {
		return daytonaSandboxPreamble + fallback
	}
	detail, err := cli.DefaultIssueBackend().Get(context.Background(), taskID)
	if err != nil || detail == nil {
		return daytonaSandboxPreamble + fallback
	}
	var b strings.Builder
	b.WriteString(daytonaSandboxPreamble)
	fmt.Fprintf(&b, "Implement task %s: %s\n\n", taskID, detail.Title)
	if detail.Description != "" {
		fmt.Fprintf(&b, "Description:\n%s\n\n", detail.Description)
	}
	if detail.Design != "" {
		fmt.Fprintf(&b, "Approved design / implementation plan (follow it exactly):\n%s\n\n", detail.Design)
	}
	if detail.AcceptanceCriteria != "" {
		fmt.Fprintf(&b, "Acceptance criteria:\n%s\n\n", detail.AcceptanceCriteria)
	}
	b.WriteString("Make exactly the code changes this task requires in the current working directory, then stop.")
	return b.String()
}

// recordSandboxMetadata stashes the sandbox/runtime metadata for the session
// finalizer as soon as the sandbox ID is known — even on failure, so a retained
// failed sandbox stays auditable (proposal: failed sandboxes are kept with
// their ID in session metadata).
func recordSandboxMetadata(res *runnerResult, baseRef, syncStrategy string) {
	if res == nil || res.sandboxID == "" {
		return
	}
	SetLastRuntimeMetadata(&sessions.RuntimeMetadata{
		Provider:     orDefault(res.provider, "daytona"),
		SandboxID:    res.sandboxID,
		RemoteCwd:    res.remoteCwd,
		BaseRef:      baseRef,
		SyncStrategy: orDefault(syncStrategy, syncStrategyPatchBack),
		Cleanup:      res.cleanup,
	})
}

// flueRunnerExec runs the flue runner workflow and returns the accumulated
// result. Seam for tests (swapped to a fake runner per the proposal's E2E).
var flueRunnerExec = defaultFlueRunnerExec

func defaultFlueRunnerExec(ctx context.Context, workDir, agentName string, input runnerInput, shutdown <-chan struct{}, collector *usage.Collector) (*runnerResult, error) {
	flueBin, projectDir, err := flue.DefaultManager().EnsureProject(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("flue backend: marshal runner input: %w", err)
	}
	res := &runnerResult{}
	runErr := runHarness(ctx, shutdown, harnessInvocation{
		BinaryName:  flueBin,
		Args:        flueRunArgs("runner", projectDir, string(payload)),
		WorkDir:     workDir,
		Env:         flueDaytonaEnv(workDir, agentName),
		Prompt:      "",
		HarnessName: "",
		LineHandler: func(line string) {
			fmt.Println(line)
			res.handleLine(line, collector)
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
	return res, runErr
}

// flueDaytonaEnv is the runner subprocess env: the standard backend env plus an
// explicit DAYTONA_API_KEY passthrough (FilteredEnv is an allowlist and does
// not carry it). Codex auth is read from ~/.codex by the project's app.ts.
//
// The Phase-B SDK bootstrap (LOOM_SERVER_URL, LOOM_WORKSPACE, LOOM_ASSIGNED_TASK_ID,
// LOOM_SESSION_ID, LOOM_FLEET_DB_ACTOR, …) needs no special handling here: those
// are LOOM_-prefixed and so pass through buildBackendEnv's allowlist to the
// runner, where @loom/sdk's bootstrapFromEnv reads them.
func flueDaytonaEnv(workDir, agentName string) []string {
	env := buildBackendEnv(workDir, agentName)
	if key := os.Getenv("DAYTONA_API_KEY"); key != "" {
		env = append(env, "DAYTONA_API_KEY="+key)
	}
	return env
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() //nolint:gosec // fixed git args
	return strings.TrimSpace(string(out)), err
}

func gitCombined(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git args
	// Never block on an interactive credential prompt — a push to an
	// auth-required remote must fail fast (the daemon has no TTY), not hang.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func applyPatch(workDir, patchPath string) error {
	// --index stages exactly the patch's files (and only those) so the caller
	// can commit them without `git add -A` sweeping in loom's runtime files.
	cmd := exec.Command("git", "-C", workDir, "apply", "--3way", "--index", "--whitespace=nowarn", patchPath) //nolint:gosec // fixed args, patchPath is a loom temp file
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func hasPatch(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func tempPatchFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "loom-flue-daytona-*.patch")
	if err != nil {
		return "", func() {}, fmt.Errorf("flue backend: temp patch file: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	return name, func() { _ = os.Remove(name) }, nil
}
