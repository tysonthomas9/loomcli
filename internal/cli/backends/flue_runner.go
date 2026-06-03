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
	SyncStrategy  string `json:"sync_strategy,omitempty"` // "patch-back" (default) | "branch-push" | "none"
}

// syncStrategyPatchBack is the only strategy implemented today: the runner
// captures a binary-safe diff in the sandbox and loom applies it to the local
// worktree. (Proposal also describes "branch-push" for server-scale operation.)
const syncStrategyPatchBack = "patch-back"

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

	patchOut, cleanup, err := tempPatchFile()
	if err != nil {
		return err
	}
	defer cleanup()
	input.PatchOut = patchOut

	fmt.Println("Launching flue agent in a Daytona sandbox (daytona-task)...")
	fmt.Println("")

	res, runErr := flueRunnerExec(context.Background(), workDir, agentName, input, shutdown, collector)
	recordSandboxMetadata(res, input.BaseRef)
	if runErr != nil {
		return runErr
	}
	if res.status != "completed" {
		if res.errMsg != "" {
			return fmt.Errorf("flue daytona-task failed (sandbox %s retained): %s", res.sandboxID, res.errMsg)
		}
		return fmt.Errorf("flue daytona-task did not report completion")
	}

	// Patch-sync: apply the remote changes back into the local worktree, then
	// commit + push so the work lands "back in loom" the way a local agent's own
	// git commit/push would (the remote agent can't touch the local repo).
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
	return runnerInput{
		Sandbox:       "daytona-task",
		Prompt:        buildSandboxPrompt(workDir, prompt),
		Model:         resolveFlueModel(),
		RepoRemoteURL: remote,
		RepoBranch:    branch,
		BaseRef:       baseRef,
		TaskID:        agentName,
		SyncStrategy:  syncStrategyPatchBack,
	}, nil
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
	// The daemon injects the claimed task as LOOM_ASSIGNED_TASK_ID; single-task
	// runs record it in the worktree lock. Prefer the env, fall back to the lock.
	taskID := strings.TrimSpace(os.Getenv("LOOM_ASSIGNED_TASK_ID"))
	if taskID == "" {
		if info, err := cli.ReadLockFile(workDir); err == nil {
			taskID = info.TaskID
		}
	}
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
func recordSandboxMetadata(res *runnerResult, baseRef string) {
	if res == nil || res.sandboxID == "" {
		return
	}
	SetLastRuntimeMetadata(&sessions.RuntimeMetadata{
		Provider:     orDefault(res.provider, "daytona"),
		SandboxID:    res.sandboxID,
		RemoteCwd:    res.remoteCwd,
		BaseRef:      baseRef,
		SyncStrategy: syncStrategyPatchBack,
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
