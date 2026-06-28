package backends

import (
	"context"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// envLeadControlled is the escape hatch for the controlled lead runtime.
// Set LOOM_LEAD_CONTROLLED=0 to launch leads as plain interactive processes
// (no PTY supervision, no queued message delivery).
const envLeadControlled = "LOOM_LEAD_CONTROLLED"

func leadControlDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLeadControlled))) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

// runHarnessLead is a test seam over the harness lead runtime.
var runHarnessLead = leadcontrol.RunHarnessLeadRuntime

// RunControlledLeadRuntime launches the controlled lead runtime for the given
// backend: the Codex app-server runtime for codex, the harness-wrapper PTY
// runtime for the other supported backends. Returns handled=false when the
// backend has no controlled runtime (or LOOM_LEAD_CONTROLLED=0), in which
// case the caller should fall back to a plain interactive launch.
func RunControlledLeadRuntime(
	ctx context.Context,
	st store.Store,
	workspace string,
	leadName string,
	sessionID string,
	workDir string,
	prompt string,
	backendName string,
) (bool, error) {
	if leadControlDisabled() {
		return false, nil
	}
	backend := strings.ToLower(strings.TrimSpace(backendName))
	if backend == NameCodex {
		return true, RunCodexLeadRuntime(ctx, st, workspace, leadName, sessionID, workDir, prompt)
	}
	binary, args, env, ok := harnessLeadInvocation(backend, workDir)
	if !ok {
		return false, nil
	}
	return true, runHarnessLead(ctx, leadcontrol.HarnessLeadRuntimeConfig{
		Store:      st,
		Workspace:  workspace,
		LeadName:   leadName,
		SessionID:  sessionID,
		WorkDir:    workDir,
		Prompt:     prompt,
		Backend:    backend,
		BinaryPath: binary,
		Args:       args,
		Env:        env,
	})
}

// harnessLeadInvocation mirrors each backend's InvokeInteractive command
// construction (binary, args, env) for use under harness-wrapper supervision.
// The prompt is appended by the runtime as the final positional argument.
func harnessLeadInvocation(backend, workDir string) (string, []string, []string, bool) {
	switch backend {
	case "claude":
		return "claude", []string{"--dangerously-skip-permissions"}, buildClaudeEnv(workDir, ""), true
	case "gemini":
		return "gemini", []string{"--approval-mode=yolo"}, buildBackendEnv(workDir, ""), true
	case "opencode":
		args := append([]string{"run", "--dir", workDir, "--dangerously-skip-permissions"}, openCodeModelArgs()...)
		return "opencode", args, buildBackendEnv(workDir, ""), true
	case "cursor":
		// the headless agent CLI is `cursor-agent`; `cursor` is the IDE launcher.
		return "cursor-agent", []string{"--force"}, buildBackendEnv(workDir, ""), true
	default:
		return "", nil, nil, false
	}
}
