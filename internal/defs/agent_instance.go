package defs

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type AgentInstanceModule struct {
	Name             string                   `json:"name"`
	RoleName         string                   `json:"role_name"`
	SourcePath       string                   `json:"source_path"`
	SourceHash       string                   `json:"source_hash"`
	Version          string                   `json:"version"`
	Auto             bool                     `json:"auto,omitempty"`
	Backend          string                   `json:"backend,omitempty"`
	FallbackBackends []string                 `json:"fallback_backends,omitempty"`
	Repos            []string                 `json:"repos,omitempty"`
	RepoGroups       []string                 `json:"repo_groups,omitempty"`
	CrossRepo        bool                     `json:"cross_repo,omitempty"`
	Parent           string                   `json:"parent,omitempty"`
	State            domain.AgentState        `json:"state,omitempty"`
	Mode             domain.AgentMode         `json:"mode,omitempty"`
	TaskFilter       string                   `json:"task_filter,omitempty"`
	MaxConcurrency   int                      `json:"max_concurrency,omitempty"`
	BudgetPolicy     string                   `json:"budget_policy,omitempty"`
	DesiredState     domain.AgentDesiredState `json:"desired_state,omitempty"`
}

func applyAgentInstances(ctx context.Context, st store.Store, ws string, instances []AgentInstanceModule) error {
	if len(instances) == 0 {
		return nil
	}
	if st.Agents() == nil {
		return fmt.Errorf("agent store not configured")
	}
	for _, instance := range instances {
		if err := applyAgentInstance(ctx, st, ws, instance); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentInstance(ctx context.Context, st store.Store, ws string, instance AgentInstanceModule) error {
	create := store.AgentCreate{
		WorkspaceKey:     ws,
		Name:             instance.Name,
		RoleName:         instance.RoleName,
		Auto:             instance.Auto,
		Backend:          instance.Backend,
		FallbackBackends: append([]string(nil), instance.FallbackBackends...),
		Repos:            append([]string(nil), instance.Repos...),
		RepoGroups:       append([]string(nil), instance.RepoGroups...),
		CrossRepo:        instance.CrossRepo,
		Parent:           instance.Parent,
		Mode:             instance.Mode,
		TaskFilter:       instance.TaskFilter,
		MaxConcurrency:   instance.MaxConcurrency,
		BudgetPolicy:     instance.BudgetPolicy,
		DesiredState:     instance.DesiredState,
	}
	created, err := st.Agents().Create(ctx, create)
	if err == nil {
		return syncAgentInstanceState(ctx, st, ws, created.Name, instance)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create agent instance %s: %w", instance.Name, err)
	}
	patch := store.AgentUpdate{
		RoleName:         &instance.RoleName,
		Auto:             &instance.Auto,
		Backend:          &instance.Backend,
		FallbackBackends: &instance.FallbackBackends,
		Repos:            &instance.Repos,
		RepoGroups:       &instance.RepoGroups,
		CrossRepo:        &instance.CrossRepo,
		Parent:           &instance.Parent,
		State:            &instance.State,
		Mode:             &instance.Mode,
		TaskFilter:       &instance.TaskFilter,
		MaxConcurrency:   &instance.MaxConcurrency,
		BudgetPolicy:     &instance.BudgetPolicy,
		DesiredState:     &instance.DesiredState,
	}
	if _, err := st.Agents().Update(ctx, ws, instance.Name, patch); err != nil {
		return fmt.Errorf("update agent instance %s: %w", instance.Name, err)
	}
	return nil
}

func syncAgentInstanceState(ctx context.Context, st store.Store, ws, name string, instance AgentInstanceModule) error {
	if instance.State == "" {
		return nil
	}
	if _, err := st.Agents().Update(ctx, ws, name, store.AgentUpdate{State: &instance.State}); err != nil {
		return fmt.Errorf("update agent instance %s state: %w", name, err)
	}
	return nil
}
