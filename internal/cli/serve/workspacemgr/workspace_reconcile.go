package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit/abandon"
	"github.com/tysonthomas9/loomcli/internal/loomgit/applyrecovery"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/reconcile"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
)

// ReconcileJournal classifies open journal work before dispatching it to the
// workspace owner. Apply recovery is wired by its owner separately.
func ReconcileJournal(ctx context.Context, s storepkg.Store) error {
	if err := reconcile.RunOnce(ctx, reconcile.Handlers{
		Workspace: reconcile.RecoverFunc(func(ctx context.Context) error { return Reconcile(ctx, s) }),
		Apply:     reconcile.RecoverFunc(func(ctx context.Context) error { return recoverPullThenApply(ctx, applyrecovery.Recover) }),
		Landing: reconcile.RecoverFunc(func(ctx context.Context) error {
			return landing.RunOnceWithOptions(ctx, landingOptions())
		}),
	}); err != nil {
		return err
	}
	abandonment := abandon.New()
	claims, ok := cli.GetDeps(nil).IssueBackend.(abandon.ClaimReleaser)
	if !ok {
		return errors.New("issue backend cannot read current claim holder")
	}
	abandonment.Claims = claims
	abandonment.Sessions = s.AgentSessions()
	return abandonment.Reconcile(ctx)
}

func landingOptions() landing.Options {
	return landing.Options{
		Dependents: func(ctx context.Context, workspace, change string) ([]landing.Dependent, error) {
			lineages, err := taskcopy.DependentsOf(ctx, workspace, change)
			if err != nil {
				return nil, err
			}
			dependents := make([]landing.Dependent, 0, len(lineages))
			for _, lineage := range lineages {
				dependents = append(dependents, landing.Dependent{Task: lineage.Task, Repo: lineage.Repo})
			}
			return dependents, nil
		},
		Restack: publish.RestackOffer,
	}
}

// Reconcile adopts every open workspace creation before serve accepts work.
// A checkout mismatch remains journal-owned and is surfaced for repair.
func Reconcile(ctx context.Context, s storepkg.Store) error {
	recoveries, err := loomworkspace.OpenCreations(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, recovery := range recoveries {
		if err := reconcileCreation(ctx, s, recovery.Workspace(), recovery); err != nil {
			failure := fmt.Errorf("reconcile workspace %s: %w", recovery.Workspace(), err)
			slog.Warn("workspace recovery failed", "workspace", recovery.Workspace(), "err", err)
			failures = append(failures, failure)
		}
		if err := recovery.Close(); err != nil {
			slog.Warn("workspace recovery close failed", "workspace", recovery.Workspace(), "err", err)
			failures = append(failures, fmt.Errorf("close workspace recovery %s: %w", recovery.Workspace(), err))
		}
	}
	return errors.Join(failures...)
}

func reconcileCreation(ctx context.Context, s storepkg.Store, key string, recovery *loomworkspace.Recovery) error {
	plan := recovery.Plan
	if recovery.IsAttach() {
		if _, err := s.Workspaces().Get(ctx, key); errors.Is(err, domain.ErrNotFound) {
			if err := recovery.DiscardMissingAttachment(ctx); err != nil {
				return err
			}
			slog.Warn("discarded interrupted repo attachment for deleted workspace", "workspace", key)
			return nil
		} else if err != nil {
			return err
		}
	}
	if recovery.Unplanned() {
		return markCreationAttention(ctx, s, key, plan, errors.New("journal has no recovery plan; inspect its worktrees"))
	}
	if plan.Kind == "clone" && len(plan.Repos) == 0 {
		return markCreationAttention(ctx, s, key, plan, errors.New("clone interrupted before checkout plan was complete"))
	}
	for _, repo := range plan.Repos {
		if repo.BaseSHA == "" {
			return markCreationAttention(ctx, s, key, plan, errors.New("workspace base resolution was interrupted"))
		}
	}
	if err := recovery.ResumeCheckouts(ctx); err != nil {
		return markCreationAttention(ctx, s, key, plan, err)
	}
	if err := ensureRecoveryWorkspace(ctx, s, key, plan, recovery.IsAttach()); err != nil {
		return err
	}
	if err := ensureBuiltInRoles(ctx, s, key); err != nil {
		return err
	}
	repos, err := ensureRecoveryRepos(ctx, s, key, plan)
	if err != nil {
		return err
	}
	if err := saveLocalWorkspaceState(key, plan.Path, repos, false); err != nil {
		return err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		return err
	}
	if !recovery.IsAttach() && recovery.Phase() != "rows_written" {
		if err := recovery.RowsWritten(ctx); err != nil {
			return err
		}
	}
	return recovery.Commit(ctx)
}

func ensureRecoveryWorkspace(ctx context.Context, s storepkg.Store, key string, plan loomworkspace.Creation, attach bool) error {
	_, err := s.Workspaces().Get(ctx, key)
	if err == nil {
		return nil
	}
	if attach {
		return fmt.Errorf("attachment workspace missing: %w", err)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	_, err = s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{Key: key, Name: plan.Name, DefaultBranch: plan.Trunk})
	return err
}

func ensureRecoveryRepos(ctx context.Context, s storepkg.Store, key string, plan loomworkspace.Creation) ([]config.RepoConfig, error) {
	var repos []config.RepoConfig
	for _, repo := range plan.Repos {
		if _, err := s.Repos().Get(ctx, key, repo.Name); errors.Is(err, domain.ErrNotFound) {
			if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{WorkspaceKey: key, Name: repo.Name, RemoteURL: gitRemoteURL(repo.Path, "origin"), Remote: "origin", DefaultBranch: plan.Trunk}); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		repos = append(repos, config.RepoConfig{Name: repo.Name, Path: repo.Path, Remote: "origin", DefaultBranch: plan.Trunk})
	}
	return repos, nil
}

func ensureBuiltInRoles(ctx context.Context, s storepkg.Store, key string) error {
	// The original create may have stopped between role writes.
	for _, role := range []storepkg.RoleCreate{
		{WorkspaceKey: key, Name: "plan", Description: "Planning agent", TaskFilter: "needs_plan", ReadOnly: true},
		{WorkspaceKey: key, Name: "task", Description: "Task implementation agent", TaskFilter: "has_design"},
		{WorkspaceKey: key, Name: "lead", Kind: string(domain.RoleKindInteractive), Description: "Lead/orchestrator terminal"},
	} {
		if _, err := s.Roles().Get(ctx, key, role.Name); errors.Is(err, domain.ErrNotFound) {
			if _, err := s.Roles().Create(ctx, role); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func markCreationAttention(ctx context.Context, s storepkg.Store, key string, plan loomworkspace.Creation, cause error) error {
	if _, err := s.Workspaces().Get(ctx, key); errors.Is(err, domain.ErrNotFound) {
		if _, err := s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{Key: key, Name: plan.Name, DefaultBranch: plan.Trunk}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	state := domain.WorkspaceStateAttentionRequired
	message := fmt.Sprintf("Workspace creation needs repair: %v", cause)
	if _, err := s.Workspaces().Update(ctx, key, storepkg.WorkspaceUpdate{State: &state, ErrorMessage: &message}); err != nil {
		return err
	}
	slog.Warn("workspace creation needs attention", "workspace", key, "err", cause, "path", plan.Path)
	return nil
}
