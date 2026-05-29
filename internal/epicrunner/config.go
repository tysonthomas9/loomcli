package epicrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

// RunnerConfig configures one epic runner.
type RunnerConfig struct {
	Store                 store.Store
	IssueBackend          backend.IssueBackend
	WorkspaceKey          string
	EpicID                string
	LeadName              string
	Role                  string
	Backend               string
	WorkerPrefix          string
	MaxConcurrency        int
	Interval              time.Duration
	OrchestratorSessionID string
	TargetNodeID          string
	DryRun                bool
	MutateLead            bool
	RequireCommandStore   bool
	RequireRepos          bool
	ValidateEpic          bool
	FailOnDispatchError   bool
	PrepareWorktrees      bool
	Out                   io.Writer
	ErrOut                io.Writer
}

func (cfg *RunnerConfig) normalize() {
	cfg.WorkspaceKey = strings.TrimSpace(cfg.WorkspaceKey)
	cfg.EpicID = strings.TrimSpace(cfg.EpicID)
	cfg.LeadName = strings.TrimSpace(cfg.LeadName)
	cfg.Role = strings.TrimSpace(cfg.Role)
	cfg.Backend = strings.TrimSpace(cfg.Backend)
	cfg.WorkerPrefix = strings.TrimSpace(cfg.WorkerPrefix)
	cfg.OrchestratorSessionID = strings.TrimSpace(cfg.OrchestratorSessionID)
	cfg.TargetNodeID = strings.TrimSpace(cfg.TargetNodeID)
	if cfg.Role == "" {
		cfg.Role = "task"
	}
	if cfg.WorkerPrefix == "" {
		cfg.WorkerPrefix = SanitizePrefix(cfg.EpicID)
	}
	if cfg.MaxConcurrency < 1 {
		cfg.MaxConcurrency = 1
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.ErrOut == nil {
		cfg.ErrOut = io.Discard
	}
}

func validateRunnerConfig(ctx context.Context, cfg RunnerConfig) error {
	if cfg.Store == nil {
		return runError(ErrorKindUnavailable, "store not configured", nil)
	}
	if cfg.IssueBackend == nil {
		return runError(ErrorKindUnavailable, "issue backend not available", nil)
	}
	if cfg.WorkspaceKey == "" {
		return runError(ErrorKindValidation, "workspace key required", nil)
	}
	if cfg.EpicID == "" {
		return runError(ErrorKindValidation, "epic id required", nil)
	}
	if cfg.RequireCommandStore && cfg.Store.AgentCommands() == nil && !cfg.DryRun {
		return runError(ErrorKindUnavailable, "agent command store not configured; start a daemon-backed workspace before running an epic from the UI", nil)
	}
	if cfg.RequireRepos {
		if err := validateWorkspaceHasRepos(ctx, cfg.Store, cfg.WorkspaceKey); err != nil {
			return err
		}
	}
	if cfg.ValidateEpic {
		if err := validateEpicIssue(ctx, cfg.IssueBackend, cfg.EpicID); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkspaceHasRepos(ctx context.Context, st store.Store, workspace string) error {
	if st.Repos() == nil {
		return runError(ErrorKindUnavailable, "repo store not configured", nil)
	}
	repos, err := st.Repos().List(ctx, workspace)
	if err != nil {
		return runError(ErrorKindInternal, "list workspace repos", err)
	}
	count := 0
	for _, repo := range repos {
		if repo != nil {
			count++
		}
	}
	if count == 0 {
		return runError(ErrorKindValidation,
			fmt.Sprintf("workspace %s has no repos attached; add or clone a repo before running an epic", workspace),
			nil)
	}
	return nil
}

func validateEpicIssue(ctx context.Context, ib backend.IssueBackend, epicID string) error {
	detail, err := ib.Get(ctx, epicID)
	if err != nil {
		return backendRunError(ErrorKindNotFound, fmt.Sprintf("epic %s was not found", epicID), err)
	}
	if detail == nil {
		return runError(ErrorKindNotFound, fmt.Sprintf("epic %s was not found", epicID), domain.ErrNotFound)
	}
	if detail.IssueType != "" && detail.IssueType != "epic" {
		return runError(ErrorKindValidation,
			fmt.Sprintf("issue %s has type %q; run epic requires an issue_type of epic", epicID, detail.IssueType),
			nil)
	}
	return nil
}

func ensureEpicWorkflowRun(ctx context.Context, cfg RunnerConfig) (string, error) {
	if cfg.DryRun {
		return "", nil
	}
	if cfg.Store == nil || cfg.Store.WorkflowRuns() == nil || cfg.Store.TaskRuns() == nil || cfg.Store.RunEvents() == nil {
		return "", runError(ErrorKindUnavailable, "workflow control-plane stores are required for epic runner dispatch", nil)
	}
	input := workflowpkg.ParentWorkItemsInput{
		ParentID:       cfg.EpicID,
		Role:           cfg.Role,
		MaxConcurrency: cfg.MaxConcurrency,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", runError(ErrorKindInternal, "marshal epic workflow input", err)
	}
	actor := cfg.LeadName
	if actor == "" {
		actor = "epic-runner"
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, cfg.Store, cfg.WorkspaceKey, workflowpkg.RunParentWorkItemsName, raw, actor)
	if err != nil {
		return "", runError(ErrorKindInternal, "create or resume epic workflow run", err)
	}
	return run.RunID, nil
}

func backendRunError(defaultKind ErrorKind, msg string, err error) error {
	var be *backend.BackendError
	if !errors.As(err, &be) {
		return runError(defaultKind, msg, err)
	}
	switch be.Kind {
	case backend.KindNotFound:
		return runError(ErrorKindNotFound, msg, err)
	case backend.KindValidation:
		return runError(ErrorKindValidation, msg, err)
	case backend.KindConflict:
		return runError(ErrorKindConflict, msg, err)
	case backend.KindUnavailable, backend.KindTimeout, backend.KindCanceled:
		return runError(ErrorKindUnavailable, msg, err)
	default:
		return runError(ErrorKindInternal, msg, err)
	}
}
