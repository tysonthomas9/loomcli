package backends

// Launch-time model pinning.
//
// Claude Code's in-session /model switch offers to save the choice "as your
// default for new sessions", and accepting it rewrites `model` in the LIVE
// profile settings.json. Until an operator reconciles that, the NEXT session
// boots on whatever was last saved rather than on what the workspace
// provisioned. Detection already exists (the managed semantic verifier flags
// the drifted key in `loom doctor`); enforcement lands separately. This closes
// the residual gap by making the START STATE immune: the launch passes
// `--model <provisioned value>` on the command line, read from the profile's
// .provisioned/ baseline, and a CLI argument outranks settings.json.
//
// Precedence, and it is deliberate:
//
//	LOOM_AGENT_MODEL (explicit role.model from the supervisor)   <- wins
//	-> provisioned baseline for the resolved profile root
//	-> "" (no pin; backend default / settings.json applies)
//
// Role intent outranks the baseline: a supervisor-spawned agent whose role
// pins `model` has made a deliberate choice. The baseline is the FALLBACK that
// closes the lead's hole, not an override of configured intent.
//
// SCOPE: `model` only. The permission surface is already pinned at launch by
// argument (--dangerously-skip-permissions plus appendClaudeSafetyArgs), so
// pinning permissions.defaultMode again buys nothing; --effort is already wired
// to LOOM_AGENT_EFFORT/LOOM_CLAUDE_EFFORT and no in-session write of it to the
// profile file has been shown. Everything else in settings.json stays
// file-borne — the profile file remains the general mechanism and this is a
// short, explicit allowlist. Adding a key means adding one table entry below,
// in one place per harness.
//
// Nothing here can fail a launch. Every unresolvable case is "no pin" plus a
// debug log; boot refusal belongs to profile enforcement, and a resolver that
// can exit from inside an argv builder is a new way to brick the fleet.

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// The pinned setting, named once per harness: the managed file inside the
// profile root and the top-level key in it that carries the model.
const (
	claudePinnedModelFile = "settings.json"
	claudePinnedModelKey  = "model"
	codexPinnedModelFile  = "config.toml"
	codexPinnedModelKey   = "model"
)

// pinnedClaudeModel resolves the model to pass as `--model` to claude:
// LOOM_AGENT_MODEL, else the CLAUDE_CONFIG_DIR profile's provisioned
// settings.json `model`, else "" for no pin.
func pinnedClaudeModel() string {
	return pinnedModelFrom("claude", "CLAUDE_CONFIG_DIR", claudePinnedModelFile, claudePinnedModelKey)
}

// pinnedCodexModel resolves the model to pass as `-c model="..."` to codex:
// LOOM_AGENT_MODEL, else the CODEX_HOME profile's provisioned config.toml
// `model`, else "" for no pin.
func pinnedCodexModel() string {
	return pinnedModelFrom("codex", "CODEX_HOME", codexPinnedModelFile, codexPinnedModelKey)
}

// PinnedModelFor is the resolver for one backend name, exported for the one
// caller with an operator at the terminal (`loom lead`, which warns when a
// lead session starts unpinned). Backends other than claude and codex have no
// provisioned profile root and resolve to "".
//
// The resolution itself is not duplicated there on purpose: two copies of a
// precedence ladder drift apart, and the ladder is the contract.
func PinnedModelFor(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		return pinnedClaudeModel()
	case NameCodex:
		return pinnedCodexModel()
	default:
		return ""
	}
}

// pinnedModelFrom is the shared ladder. The profile root is read from the
// ENVIRONMENT VARIABLE rather than from lead's profile enforcement internals:
// by the time any launch path in this package runs, the root has already been
// exported (or verified), and the env var is the contract every other consumer
// uses.
func pinnedModelFrom(harness, envVar, rel, key string) string {
	if model := strings.TrimSpace(os.Getenv("LOOM_AGENT_MODEL")); model != "" {
		return model
	}
	dir := strings.TrimSpace(os.Getenv(envVar))
	if dir == "" {
		// An unprofiled launch on the operator's own ~/.claude: nothing
		// provisioned it, so there is nothing to pin to.
		return ""
	}
	// The workspace launcher currently exports a RELATIVE config dir, and a
	// lead may run from any directory, so resolve it before reading.
	abs, err := filepath.Abs(dir)
	if err != nil {
		slog.Debug("model pin: unresolvable profile root", "harness", harness, "dir", dir, "err", err)
		return ""
	}
	model, found, err := agentprofile.ProvisionedString(abs, rel, key)
	if err != nil {
		slog.Debug("model pin: unreadable provisioned baseline", "harness", harness, "dir", abs, "file", rel, "err", err)
		return ""
	}
	if !found {
		slog.Debug("model pin: no provisioned value", "harness", harness, "dir", abs, "file", rel, "key", key)
		return ""
	}
	// Only the resolved model and the profile directory are logged. The
	// profile root also holds oauth-token/.credentials.json; nothing here
	// reads or prints them.
	slog.Debug("model pin resolved", "harness", harness, "dir", abs, "model", model)
	return model
}
