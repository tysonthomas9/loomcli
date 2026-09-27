package lead

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// leadAgentsFileName and leadClaudeFileName are the two ambient-instruction
// files the harnesses read from their working directory: codex reads AGENTS.md,
// claude reads CLAUDE.md. AGENTS.md is canonical and CLAUDE.md is a pointer, so
// there is one body of text rather than two that drift.
const (
	leadAgentsFileName = "AGENTS.md"
	leadClaudeFileName = "CLAUDE.md"
)

// leadClaudeStub is the seeded CLAUDE.md, following the pointer convention
// already used by loomcli's own CLAUDE.md.
const leadClaudeStub = "# Lead\n\n" +
	"- Read `AGENTS.md` for the lead persona, the label ladder, and backlog conventions.\n"

// resolveLeadWorkdir picks the directory `loom lead` runs in, and reports
// whether that directory is DEDICATED to lead.
//
// Dedicated means the path came from LOOM_LEAD_WORKDIR or from
// <workspacePath>/lead - somewhere lead owns. The fallback, os.Getwd, is
// whatever directory the operator happened to be standing in, and is reported
// as not dedicated: seeding files there or shrinking the argv prompt would be
// actively harmful (see leadSeedAndShrink).
//
// A dedicated directory is created if absent. If creating it fails, the
// fallback applies rather than the launch: `loom lead` must always run.
func resolveLeadWorkdir(ctx context.Context) (string, bool, error) {
	wsKey, err := bootstrap.ResolveActiveWorkspaceKey(ctx, nil)
	if err != nil {
		wsKey = ""
	}
	if dir, ok := localworkspace.EnsureLeadWorkdir(wsKey); ok {
		return dir, true, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	return cwd, false, nil
}

// seedLeadWorkdirFiles writes AGENTS.md and CLAUDE.md into a dedicated lead
// workdir when they are absent, and NEVER overwrites either one.
//
// That contract is what makes the files genuinely operator-editable: tuning
// lead's persona in <ws>/lead/AGENTS.md survives every upgrade, and adopting a
// newer shipped default is an explicit operator act (delete the file and
// relaunch). Seeding is best-effort - a failure is logged and lead still runs,
// it just keeps whatever prompt it was given on argv.
func seedLeadWorkdirFiles(workDir string) {
	seedLeadWorkdirFile(filepath.Join(workDir, leadAgentsFileName), agent.LeadAgentsFileText())
	seedLeadWorkdirFile(filepath.Join(workDir, leadClaudeFileName), leadClaudeStub)
}

func seedLeadWorkdirFile(path, content string) {
	_, err := os.Stat(path)
	if err == nil {
		return // already present - leave the operator's copy alone
	}
	if !os.IsNotExist(err) {
		slog.Warn("lead workdir: stat failed, not seeding", "path", path, "err", err)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		slog.Warn("lead workdir: seed failed", "path", path, "err", err)
	}
}
