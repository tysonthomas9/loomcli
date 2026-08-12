package backends

import (
	"context"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// envLeadControlled is the escape hatch for the controlled lead runtime.
// Set LOOM_LEAD_CONTROLLED=0 to launch leads as plain interactive processes
// (no PTY supervision, no queued message delivery).
const envLeadControlled = "LOOM_LEAD_CONTROLLED"

// Claude's fullscreen renderer only advertises mouse tracking when its
// virtualized scrollback mode is enabled. Keep this scoped to controlled
// interactive leads; background Claude workers do not render in web xterm.
const claudeVirtualScrollEnv = "CLAUDE_CODE_NO_FLICKER=1"

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
	inv, ok := harnessLeadInvocation(backend, workDir)
	if !ok {
		return false, nil
	}
	// Same defense-in-depth re-validation the other invokers do: a knob the
	// resolved backend cannot apply refuses the launch rather than dropping
	// the restriction. Returning (true, err) is deliberate — the caller must
	// NOT fall back to a plain interactive launch, which is precisely the
	// unrestricted run this refuses.
	if err := validateSafetyKnobsFromEnv(backend); err != nil {
		return true, err
	}
	return true, runHarnessLead(ctx, leadcontrol.HarnessLeadRuntimeConfig{
		Store:            st,
		Workspace:        workspace,
		LeadName:         leadName,
		SessionID:        sessionID,
		WorkDir:          workDir,
		Prompt:           prompt,
		Backend:          backend,
		BinaryPath:       inv.binary,
		Args:             inv.args,
		PromptFlag:       inv.promptFlag,
		Env:              inv.env,
		HarnessSessionID: inv.harnessSessionID,
	})
}

// harnessLeadLaunch is a controlled lead backend's launch spec. (Distinct
// from harnessInvocation, the one-shot wrapper-run spec in backend_wrapper.go.)
// harnessSessionID is non-empty when the args pin the harness's own session id
// at launch (claude --session-id), making the transcript location knowable
// from boot instead of waiting for the runtime to scrape it off the TUI.
type harnessLeadLaunch struct {
	binary           string
	args             []string
	env              []string
	promptFlag       string
	harnessSessionID string
}

// newHarnessSessionID is a seam for tests; production generates a UUIDv4,
// the format claude's --session-id requires.
var newHarnessSessionID = uuid.NewString

// harnessLeadInvocation mirrors each backend's InvokeInteractive command
// construction (binary, args, env) for use under harness-wrapper supervision.
// The prompt is appended by the runtime as the final positional argument.
//
// "Mirrors" is load-bearing and has been wrong once: this builder used to
// hardcode each backend's permissive flag and never consult the role safety
// knobs, so an interactive role carrying read_only or a tool list got the
// restriction on the daemon and agent invoker paths and silently lost it when
// the same role launched through RunControlledLeadRuntime. The knobs are
// resolved through the same helpers the other builders use — keep it that way,
// and add new backends by calling a helper rather than writing the flag out.
func harnessLeadInvocation(backend, workDir string) (harnessLeadLaunch, bool) {
	switch backend {
	case "claude":
		sessionID := newHarnessSessionID()
		env := append(buildClaudeEnv(workDir, ""), claudeVirtualScrollEnv)
		return harnessLeadLaunch{
			binary:           "claude",
			args:             appendClaudeSafetyArgs([]string{"--session-id", sessionID, "--dangerously-skip-permissions"}),
			env:              env,
			harnessSessionID: sessionID,
		}, true
	case "gemini":
		return harnessLeadLaunch{binary: "gemini", args: []string{geminiApprovalModeArg()}, env: buildBackendEnv(workDir, "")}, true
	case "opencode":
		return harnessLeadLaunch{
			binary:     "opencode",
			args:       openCodeInteractiveArgs(),
			promptFlag: "--prompt",
			env:        buildBackendEnv(workDir, ""),
		}, true
	case "cursor":
		// the headless agent CLI is `cursor-agent`; `cursor` is the IDE launcher.
		// --force is cursor-agent's permission bypass and has no read_only
		// counterpart, which is why cursor is outside SupportsHardReadOnly:
		// read_only on cursor is the prompt preamble and nothing more, and the
		// supervisor says so at spawn. A tool list on cursor never reaches
		// here — ValidateSafetyKnobs refuses the run first.
		return harnessLeadLaunch{binary: "cursor-agent", args: []string{"--force"}, env: buildBackendEnv(workDir, "")}, true
	default:
		return harnessLeadLaunch{}, false
	}
}
