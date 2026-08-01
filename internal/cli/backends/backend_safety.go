package backends

import (
	"fmt"
	"os"
	"strings"
)

// Role safety knobs (allowed_tools / denied_tools / read_only), delivered by
// the supervisor as env (LOOM_ALLOWED_TOOLS / LOOM_DENIED_TOOLS /
// LOOM_READ_ONLY) and enforced here as real backend CLI flags. The rule is
// fail-closed: a knob the resolved backend cannot enforce refuses the run
// (ValidateSafetyKnobs) rather than running without the restriction — config
// that lies is worse than config that errors.
//
// Per-backend enforcement (verified against the installed CLIs):
//
//	claude   --allowedTools / --disallowedTools; read_only = deny
//	         Write,Edit,NotebookEdit,Bash (Bash included: with it the agent
//	         can still write via redirection, so "read-only" would be a lie;
//	         critic-class roles keep Read/Grep/Glob/Task).
//	codex    read_only = `--sandbox read-only` (an OS-level policy) INSTEAD of
//	         --dangerously-bypass-approvals-and-sandbox; no tool vocabulary.
//	gemini   read_only = `--approval-mode plan` (gemini's documented
//	         read-only mode) instead of yolo; no supported tool lists (its
//	         --allowed-tools is deprecated upstream).
//	others   nothing enforceable — any knob fails closed.
func resolveAllowedTools() []string {
	return splitToolList(os.Getenv("LOOM_ALLOWED_TOOLS"))
}

func resolveDeniedTools() []string {
	return splitToolList(os.Getenv("LOOM_DENIED_TOOLS"))
}

func resolveReadOnly() bool {
	return os.Getenv("LOOM_READ_ONLY") == "1"
}

func splitToolList(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// claudeReadOnlyDeniedTools is the deny-set read_only expands to on claude.
// Bash is deliberately included: a shell can write files regardless of the
// edit-tool denials. Roles that need read-only shell access should use
// denied_tools explicitly instead of read_only.
var claudeReadOnlyDeniedTools = []string{"Write", "Edit", "NotebookEdit", "Bash"}

// appendClaudeSafetyArgs applies the tool knobs to a claude argv. Lists are
// passed as one comma-joined argv element (claude splits internally), so the
// variadic flag can never swallow the positional prompt.
func appendClaudeSafetyArgs(args []string) []string {
	if allowed := resolveAllowedTools(); len(allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowed, ","))
	}
	denied := resolveDeniedTools()
	if resolveReadOnly() {
		denied = mergeToolLists(denied, claudeReadOnlyDeniedTools)
	}
	if len(denied) > 0 {
		args = append(args, "--disallowedTools", strings.Join(denied, ","))
	}
	return args
}

func mergeToolLists(base, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, t := range append(append([]string{}, base...), extra...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// codexSandboxArgs picks codex's sandbox posture: the OS-level read-only
// policy when the role is read_only, the historical bypass otherwise.
func codexSandboxArgs() []string {
	if resolveReadOnly() {
		return []string{"--sandbox", "read-only"}
	}
	return []string{"--dangerously-bypass-approvals-and-sandbox"}
}

// geminiApprovalModeArg picks gemini's approval mode: its documented
// read-only "plan" mode when the role is read_only, yolo otherwise.
func geminiApprovalModeArg() string {
	if resolveReadOnly() {
		return "--approval-mode=plan"
	}
	return "--approval-mode=yolo"
}

// SupportsToolControl reports whether the backend can enforce
// allowed_tools/denied_tools as real CLI flags. Kept in lockstep with
// ValidateSafetyKnobs.
func SupportsToolControl(backendName string) bool {
	return backendName == "claude"
}

// ValidateSafetyKnobs fails closed when the resolved backend cannot enforce a
// requested knob. Called by the supervisor before spawn and by the backend
// invokers as defense in depth (an older daemon or direct CLI use bypasses
// the supervisor's check).
func ValidateSafetyKnobs(backendName string, allowedTools, deniedTools []string, readOnly bool) error {
	hasTools := len(allowedTools) > 0 || len(deniedTools) > 0
	if !hasTools && !readOnly {
		return nil
	}
	switch backendName {
	case "claude":
		return nil // full support
	case "codex":
		if hasTools {
			return fmt.Errorf("backend %q cannot enforce allowed_tools/denied_tools (no tool vocabulary); remove the knob or use the claude backend", backendName)
		}
		return nil // read_only maps to --sandbox read-only
	case "gemini":
		if hasTools {
			return fmt.Errorf("backend %q cannot enforce allowed_tools/denied_tools (upstream deprecated --allowed-tools); remove the knob or use the claude backend", backendName)
		}
		return nil // read_only maps to --approval-mode plan
	default:
		knob := "read_only"
		if hasTools {
			knob = "allowed_tools/denied_tools"
		}
		return fmt.Errorf("backend %q cannot enforce %s; refusing to run without the restriction (fail-closed)", backendName, knob)
	}
}

// validateSafetyKnobsFromEnv is the invoker-side defense-in-depth check,
// reading the same env the supervisor exported.
func validateSafetyKnobsFromEnv(backendName string) error {
	return ValidateSafetyKnobs(backendName, resolveAllowedTools(), resolveDeniedTools(), resolveReadOnly())
}
