package lead

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
)

// SuppressionReason names WHY the argv persona is suppressed for a run. It is
// quoted verbatim in the refusal, so an operator reading the error knows which
// switch to unset.
type SuppressionReason string

const (
	// SuppressedByBuiltinNone is `--prompt builtin:none` on the command line.
	SuppressedByBuiltinNone SuppressionReason = "builtin:none"
	// SuppressedByProfileSource is the lead role declaring that its persona
	// arrives as ambient profile context instead of on argv.
	SuppressedByProfileSource SuppressionReason = "persona_source: profile"
)

// builtinNonePromptFlag is the --prompt value that suppresses the argv persona.
const builtinNonePromptFlag = "builtin:" + agent.BuiltinPromptNone

// envClaudeConfigDir is claude's relocated config root. A claude session run
// under a loom agent profile has it exported, and the profile's CLAUDE.md - the
// file this check verifies - lives directly inside it.
//
// It is read from the ENVIRONMENT on purpose, and must stay that way:
// exporting CLAUDE_CONFIG_DIR is the whole mechanism by which a lead is given a
// profile (setProfileEnv), so the variable is the authoritative answer to
// "which CLAUDE.md will this session actually read?". Threading a value out of
// the profile-application code instead would (a) add a dependency on a file
// that does not exist on this base, and (b) silently ignore an operator who
// exported their own value. Please do not "fix" this back.
const envClaudeConfigDir = "CLAUDE_CONFIG_DIR"

// repairProvisionProfile is the second half of the claude repair recipe: the
// generated file still has to be copied into the profile and re-manifested.
const repairProvisionProfile = "scripts/provision-profile.sh"

// PersonaSuppression reports whether the ACTIVE workspace's lead role declares
// a suppressed persona, opening a short-lived read-only store handle to find
// out. It answers ("", false) whenever there is no workspace, no store or no
// lead role - a lead whose persona is on argv has nothing that can go stale.
//
// This is the probe `loom doctor` uses: doctor cannot see the `--prompt` flag
// of a lead somebody else launched, so the persisted role is the only signal it
// has. Once persona_source lands on Role, check that field here as well.
func PersonaSuppression(ctx context.Context) (SuppressionReason, bool) {
	handle, ws, ok := openLeadSessionStore(ctx)
	if !ok {
		return "", false
	}
	defer func() { _ = handle.Close() }()
	if handle.Store == nil || handle.Store.Roles() == nil || ws == "" {
		return "", false
	}
	loadCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	role, err := handle.Store.Roles().Get(loadCtx, ws, leadRoleName())
	if err != nil || role == nil {
		return "", false
	}
	if strings.TrimSpace(role.PromptFile) == builtinNonePromptFlag {
		return SuppressedByProfileSource, true
	}
	return "", false
}

// ResolveWorkdir is resolveLeadWorkdir for callers outside this package (the
// doctor check), returning lead's own directory and whether it is dedicated.
func ResolveWorkdir(ctx context.Context) (string, bool, error) {
	return resolveLeadWorkdir(ctx)
}

// CheckAmbientSafetyBlock refuses a suppressed persona whose ambient
// instruction file does not carry the CURRENT multi-agent safety block.
//
// LeadAgentsFileText's doc comment is explicit that the safety block is
// rendered per run and per backend and "must not become a static file that
// silently goes stale". Suppressing argv makes it exactly that, so the only
// safe answer is to refuse rather than to boot a lead whose guardrails are a
// stale copy - or absent altogether.
//
// A nil error means the block was found. Every non-nil error is a refusal and
// carries its own Repair: line.
func CheckAmbientSafetyBlock(backendName, workDir string, dedicated bool, reason SuppressionReason) error {
	path, err := ambientInstructionPath(backendName, workDir, dedicated, reason)
	if err != nil {
		return err
	}

	block := normalizeAmbientText(agent.LeadSafetyPrompt())
	if block == "" {
		return nil // nothing to verify; an empty block is a prompt-template bug, not drift
	}

	body, readErr := os.ReadFile(path) // #nosec G304 -- path is derived from the harness's own config root
	if readErr != nil {
		return fmt.Errorf("lead persona is suppressed (%s) but %s could not be read: %v\n%s",
			reason, path, readErr, repairLine(backendName, path))
	}
	if !strings.Contains(normalizeAmbientText(string(body)), block) {
		return fmt.Errorf("lead persona is suppressed (%s) but %s does not contain the current multi-agent safety block\n%s",
			reason, path, repairLine(backendName, path))
	}
	return nil
}

// ambientInstructionPath resolves the file the active harness reads ambient
// instructions from, or refuses when there is no such file to point at.
//
// claude reads it from its relocated config root, NOT from the working
// directory (that is what CLAUDE_CONFIG_DIR relocates), so the workdir plays no
// part there. codex reads AGENTS.md from the directory it runs in, which is
// only lead's own file when that directory is dedicated to lead.
func ambientInstructionPath(backendName, workDir string, dedicated bool, reason SuppressionReason) (string, error) {
	if backendName == backendnames.Claude {
		configDir := strings.TrimSpace(os.Getenv(envClaudeConfigDir))
		if configDir == "" {
			return "", fmt.Errorf("lead persona is suppressed (%s) but %s is not set: suppression requires a profiled lead, whose CLAUDE.md carries the multi-agent safety block\nRepair: loom lead --print-prompt > \"$WORKSPACE/profiles/%s/claude/CLAUDE.md\" && %s %s",
				reason, envClaudeConfigDir, resolveLeadAgentID(), repairProvisionProfile, resolveLeadAgentID())
		}
		return filepath.Join(configDir, "CLAUDE.md"), nil
	}
	if !dedicated {
		return "", fmt.Errorf("lead persona is suppressed (%s) but %s is not a dedicated lead workdir: it is wherever loom lead was started, so an %s sitting there is not lead's\nRepair: run loom lead inside a workspace, or point %s at lead's own directory",
			reason, workDir, leadAgentsFileName, "LOOM_LEAD_WORKDIR")
	}
	return filepath.Join(workDir, leadAgentsFileName), nil
}

// repairLine is the recipe that regenerates the ambient file. Run it WITHOUT
// the suppression switch: `--print-prompt` under builtin:none prints 0 bytes.
func repairLine(backendName, path string) string {
	repair := fmt.Sprintf("Repair: loom lead --print-prompt > %q", path)
	if backendName == backendnames.Claude {
		repair += fmt.Sprintf(" && %s %s", repairProvisionProfile, resolveLeadAgentID())
	}
	return repair + " (run it without --prompt " + builtinNonePromptFlag + ", which prints nothing)"
}

// normalizeAmbientText makes the substring compare survive an operator-reflowed
// file: CRLF becomes LF, per-line trailing whitespace goes, and the whole text
// is trimmed. It never touches interior content, so a genuinely stale block
// still fails to match.
func normalizeAmbientText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// leadRunPersonaSuppression reports whether THIS `loom lead` run has its argv
// persona suppressed: by the operator's own --prompt, or by the role.
func leadRunPersonaSuppression(ctx context.Context) (SuppressionReason, bool) {
	if strings.TrimSpace(leadPromptFile) == builtinNonePromptFlag {
		return SuppressedByBuiltinNone, true
	}
	return PersonaSuppression(ctx)
}
