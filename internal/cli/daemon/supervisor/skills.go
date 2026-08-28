package supervisor

import (
	"context"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/hookcfg"
	"github.com/tysonthomas9/loomcli/internal/skillmat"
)

func (s *Supervisor) ensureHookConfig(ap *AgentProcess) {
	backend := s.GetEffectiveBackend(ap)
	if err := hookcfg.EnsureSkillMaterializeHook(ap.WorktreePath, backend); err != nil {
		slog.Warn("agent hook configuration failed; continuing without raw-PTY pre-turn hook",
			"worktree", ap.Entry.Worktree, "backend", backend, "err", err)
	}
}

func (s *Supervisor) materializeSkills(ap *AgentProcess) error {
	if s.WorkspaceID == "" {
		return nil
	}
	if s.ControlStore == nil {
		slog.Warn("skill store is not configured; continuing without skill materialization",
			"worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID)
		return nil
	}
	ctx, cancel := context.WithTimeout(cmdstore.RootContext(), controlPlaneOperationTimeout)
	defer cancel()
	return skillmat.MaterializeLeased(ctx, s.ControlStore, s.WorkspaceID, ap.Entry.Role, ap.WorktreePath)
}

// materializeIdleSkills keeps an idle worker's worktree current while the
// ready queue is empty: skills edited between spawns converge within one
// no_work_backoff instead of waiting for the next claim. Only the no-work
// loop qualifies — other pre-flight failures (backend gates) skip it.
// Failures only log; the next spawn's materializeSkills call is the
// enforcing one.
func (s *Supervisor) materializeIdleSkills(ap *AgentProcess) {
	ap.Mu.Lock()
	noWork := ap.LastNoWork
	ap.Mu.Unlock()
	if !noWork {
		return
	}
	if err := s.materializeSkills(ap); err != nil {
		slog.Warn("idle skill materialization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
}
