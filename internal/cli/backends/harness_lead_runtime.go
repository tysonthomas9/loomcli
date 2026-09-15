package backends

import (
	"context"
	"fmt"
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

// ControlledLeadOptions is one controlled lead launch. It replaced a
// seven-positional-parameter signature: resume needs three more inputs, and a
// ten-argument call site is a bug waiting to be written.
//
// The three Resume* fields are mutually consistent by construction — the
// resolver in leadcontrol fills exactly the one that matches the backend — and
// all three empty means a fresh session.
type ControlledLeadOptions struct {
	Store     store.Store
	Workspace string
	LeadName  string
	SessionID string
	WorkDir   string
	Prompt    string
	Backend   string

	// ResumeHarnessSessionID is the harness's own session id to reopen
	// (claude --resume <uuid>). Harness backends only.
	ResumeHarnessSessionID string
	// ResumeCodexThreadID is the codex thread to reopen. Codex only.
	ResumeCodexThreadID string
	// ResumeLast asks codex for its own most recent thread. Codex only.
	ResumeLast bool
}

// resumeRequested reports whether this launch is a resume on any backend.
func (o ControlledLeadOptions) resumeRequested() bool {
	return strings.TrimSpace(o.ResumeHarnessSessionID) != "" ||
		strings.TrimSpace(o.ResumeCodexThreadID) != "" ||
		o.ResumeLast
}

// LeadControlDisabled reports whether LOOM_LEAD_CONTROLLED opts out of the
// controlled lead runtime. Exported so `loom lead` can refuse a resume before
// it touches the store: the uncontrolled path is a plain interactive launch
// with no session plumbing, so it cannot carry a resume id.
func LeadControlDisabled() bool { return leadControlDisabled() }

// RunControlledLeadRuntime launches the controlled lead runtime for the given
// backend: the Codex app-server runtime for codex, the harness-wrapper PTY
// runtime for the other supported backends. Returns handled=false when the
// backend has no controlled runtime (or LOOM_LEAD_CONTROLLED=0), in which
// case the caller should fall back to a plain interactive launch.
func RunControlledLeadRuntime(ctx context.Context, opts ControlledLeadOptions) (bool, error) {
	if leadControlDisabled() {
		return false, nil
	}
	backend := strings.ToLower(strings.TrimSpace(opts.Backend))
	if backend == NameCodex {
		return true, RunCodexLeadRuntime(ctx, opts)
	}
	inv, ok, err := harnessLeadInvocation(backend, opts.WorkDir, strings.TrimSpace(opts.ResumeHarnessSessionID))
	if err != nil {
		// Handled, deliberately: a not-handled refusal would send the caller
		// to the plain interactive fallback, which starts a FRESH session --
		// the silent data loss resume exists to prevent.
		return true, err
	}
	if !ok {
		if opts.resumeRequested() {
			return true, fmt.Errorf("backend %q has no controlled lead runtime, so it cannot resume a session", backend)
		}
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
		Store:                opts.Store,
		Workspace:            opts.Workspace,
		LeadName:             opts.LeadName,
		SessionID:            opts.SessionID,
		WorkDir:              opts.WorkDir,
		Prompt:               opts.Prompt,
		Backend:              backend,
		BinaryPath:           inv.binary,
		Args:                 inv.args,
		PromptFlag:           inv.promptFlag,
		Env:                  inv.env,
		HarnessSessionID:     inv.harnessSessionID,
		ResumedFromSessionID: strings.TrimSpace(opts.ResumeHarnessSessionID),
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
//
// resumeSessionID, when non-empty, reopens an existing harness conversation.
// Only claude supports it today; every other backend refuses rather than
// silently starting a fresh session, because a lead that answers "--continue"
// with an empty conversation has lost the transcript the operator asked for.
func harnessLeadInvocation(backend, workDir, resumeSessionID string) (harnessLeadLaunch, bool, error) {
	switch backend {
	case "claude":
		env := append(buildClaudeEnv(workDir, ""), claudeVirtualScrollEnv)
		if resumeSessionID != "" {
			// --session-id and --resume are mutually exclusive: claude refuses
			// a launch carrying both. The resume prefix comes from the harness
			// profile via claudeResumeArgs, the same builder the RunTurn path
			// uses, so resume stays owned in one place.
			args := append([]string{}, claudeResumeArgs(resumeSessionID)...)
			args = append(args, "--dangerously-skip-permissions")
			return harnessLeadLaunch{
				binary:           "claude",
				args:             appendClaudeSafetyArgs(args),
				env:              env,
				harnessSessionID: resumeSessionID,
			}, true, nil
		}
		sessionID := newHarnessSessionID()
		return harnessLeadLaunch{
			binary:           "claude",
			args:             appendClaudeSafetyArgs([]string{"--session-id", sessionID, "--dangerously-skip-permissions"}),
			env:              env,
			harnessSessionID: sessionID,
		}, true, nil
	case "gemini":
		if err := refuseResumeOnBackend(backend, resumeSessionID); err != nil {
			return harnessLeadLaunch{}, false, err
		}
		return harnessLeadLaunch{binary: "gemini", args: []string{geminiApprovalModeArg()}, env: buildBackendEnv(workDir, "")}, true, nil
	case "opencode":
		if err := refuseResumeOnBackend(backend, resumeSessionID); err != nil {
			return harnessLeadLaunch{}, false, err
		}
		return harnessLeadLaunch{
			binary:     "opencode",
			args:       openCodeInteractiveArgs(),
			promptFlag: "--prompt",
			env:        buildBackendEnv(workDir, ""),
		}, true, nil
	case "cursor":
		// the headless agent CLI is `cursor-agent`; `cursor` is the IDE launcher.
		// --force is cursor-agent's permission bypass and has no read_only
		// counterpart, which is why cursor is outside SupportsHardReadOnly:
		// read_only on cursor is the prompt preamble and nothing more, and the
		// supervisor says so at spawn. A tool list on cursor never reaches
		// here — ValidateSafetyKnobs refuses the run first.
		if err := refuseResumeOnBackend(backend, resumeSessionID); err != nil {
			return harnessLeadLaunch{}, false, err
		}
		return harnessLeadLaunch{binary: "cursor-agent", args: []string{"--force"}, env: buildBackendEnv(workDir, "")}, true, nil
	default:
		return harnessLeadLaunch{}, false, nil
	}
}

// refuseResumeOnBackend names the backend in the refusal. The caller must not
// fall back to a fresh launch: "--continue quietly started a new conversation"
// is the failure mode, not the graceful degradation.
func refuseResumeOnBackend(backend, resumeSessionID string) error {
	if resumeSessionID == "" {
		return nil
	}
	return fmt.Errorf("backend %q cannot resume a lead session; resume is supported on claude and codex only", backend)
}
