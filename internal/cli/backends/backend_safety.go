package backends

import (
	"fmt"
	"os"
	"strings"
)

// Role safety knobs (allowed_tools / denied_tools / read_only), delivered by
// the supervisor as env (LOOM_ALLOWED_TOOLS / LOOM_DENIED_TOOLS /
// LOOM_READ_ONLY) and enforced here as real backend CLI flags. The governing
// rule is that a knob never silently means less than it says
// (ValidateSafetyKnobs): tool lists refuse the run on a backend that cannot
// apply them, and read_only degrades to the prompt preamble with a loud
// warning. Config that lies is worse than config that errors.
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
//	others   no hard mechanism: tool lists fail closed, read_only falls back
//	         to ReadOnlyPreamble and says so.
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

// SupportsHardReadOnly reports whether the backend has a mechanism that makes
// read_only true regardless of what the model decides to do — a CLI flag or
// an OS sandbox policy — as opposed to the prompt preamble alone.
func SupportsHardReadOnly(backendName string) bool {
	switch backendName {
	case "claude", "codex", "gemini":
		return true
	}
	return false
}

// ValidateSafetyKnobs decides whether a role's safety knobs can run on the
// resolved backend. A non-empty warning means a knob is in force only softly
// and the caller must say so out loud; a non-nil error means refuse the run.
// Called by the supervisor before spawn and by the backend invokers as
// defense in depth (an older daemon or direct CLI use bypasses the
// supervisor's check).
//
// The two knob families are deliberately treated differently.
//
// Tool lists FAIL CLOSED. There is no soft equivalent of an allowlist: the
// only way to honor "you may use Read and Grep and nothing else" without the
// flag is to ask the model nicely, which is not what allowed_tools claims to
// do. An unapplied allowlist is pure fiction, and fiction in a security-shaped
// field is the defect this whole change removes.
//
// read_only DEGRADES to ReadOnlyPreamble with a warning. Unlike a tool list it
// has a real, if weaker, soft layer that every backend gets, and failing
// closed on it turned out to break the product: seedBuiltInRoles gives the
// built-in `plan` role ReadOnly: true on every workspace, so a hard refusal
// refuses to spawn EVERY planner on every backend without a hard mechanism —
// localdogfood, opencode, cursor, external — including the deterministic test
// backend. The knob was inert for long enough that its default was set without
// anyone seeing that implication. Degrading loudly keeps the "never silently a
// lie" property, which is the part that matters: the operator is told, in the
// daemon log, exactly how much enforcement they are getting.
func ValidateSafetyKnobs(backendName string, allowedTools, deniedTools []string, readOnly bool) (string, error) {
	hasTools := len(allowedTools) > 0 || len(deniedTools) > 0
	if !hasTools && !readOnly {
		return "", nil
	}
	if hasTools && !SupportsToolControl(backendName) {
		return "", fmt.Errorf("backend %q cannot enforce allowed_tools/denied_tools (%s); refusing to run without the restriction (fail-closed) — remove the knob or use a backend with tool control",
			backendName, toolControlGap(backendName))
	}
	if readOnly && !SupportsHardReadOnly(backendName) {
		return fmt.Sprintf("backend %q has no hard read-only mechanism: read_only is enforced by prompt preamble only, so the agent CAN still write. Use a backend with a sandbox or tool control for a real restriction", backendName), nil
	}
	return "", nil
}

// toolControlGap names why a backend has no tool vocabulary, so the refusal
// says something more useful than "unsupported".
func toolControlGap(backendName string) string {
	switch backendName {
	case "codex":
		return "no tool vocabulary; its sandbox is all-or-nothing"
	case "gemini":
		return "upstream deprecated --allowed-tools"
	default:
		return "no tool-restriction flags"
	}
}

// validateSafetyKnobsFromEnv is the invoker-side defense-in-depth check,
// reading the same env the supervisor exported. The supervisor has already
// logged any soft-enforcement warning through its own gate; repeating it here
// would double every line in the daemon log, so this layer only enforces.
func validateSafetyKnobsFromEnv(backendName string) error {
	_, err := ValidateSafetyKnobs(backendName, resolveAllowedTools(), resolveDeniedTools(), resolveReadOnly())
	return err
}
