// Package localbackend resolves the AI backend selected for a local task
// runner target. It owns only target precedence; readiness classification
// belongs to runtimepreflight.
package localbackend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	// LocalTaskRunnerEntrypoint routes task runs to the bundled local runner.
	LocalTaskRunnerEntrypoint = "local-task-runner"
	DefaultBackend            = backendnames.Codex
)

// Target identifies the local task runner backend configuration to resolve.
type Target struct {
	WorkspaceKey    string
	AgentName       string
	AgentRequired   bool
	BackendOverride string
}

// Source records which precedence level selected a backend.
type Source string

const (
	SourceOverride  Source = "override"
	SourceAgent     Source = "agent"
	SourceWorkspace Source = "workspace"
	SourceDefault   Source = "default"
)

// TargetStore provides the configuration consulted by Resolve.
type TargetStore interface {
	Agents() store.AgentStore
	Daemon() store.DaemonProfileStore
}

// Resolve applies explicit override > agent > workspace > default precedence.
// A named agent is always looked up, even when override is set, so required
// command targets cannot be bypassed by supplying an explicit backend.
func Resolve(
	ctx context.Context,
	st TargetStore,
	target Target,
) (string, Source, error) {
	workspaceKey := strings.TrimSpace(target.WorkspaceKey)
	agentName := strings.TrimSpace(target.AgentName)
	backendOverride := strings.TrimSpace(target.BackendOverride)
	agentBackend, err := resolveAgentBackend(ctx, st, workspaceKey, agentName, target.AgentRequired)
	if err != nil {
		return "", "", err
	}
	if backendOverride != "" {
		return backendOverride, SourceOverride, nil
	}
	if agentBackend != "" {
		return agentBackend, SourceAgent, nil
	}
	return resolveWorkspaceBackend(ctx, st, workspaceKey)
}

func resolveAgentBackend(
	ctx context.Context,
	st TargetStore,
	workspaceKey, agentName string,
	agentRequired bool,
) (string, error) {
	if agentName == "" {
		return "", nil
	}
	if workspaceKey == "" {
		return "", fmt.Errorf("resolve agent %q: workspace is required: %w", agentName, domain.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if st == nil {
		return "", errors.New("local backend target store is unavailable")
	}
	agent, err := st.Agents().Get(ctx, workspaceKey, agentName)
	if err != nil {
		if !agentRequired && errors.Is(err, domain.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("get agent %q in workspace %q: %w", agentName, workspaceKey, err)
	}
	if agent == nil {
		if agentRequired {
			return "", fmt.Errorf("get agent %q in workspace %q: %w", agentName, workspaceKey, domain.ErrNotFound)
		}
		return "", nil
	}
	return strings.TrimSpace(agent.Backend), nil
}

func resolveWorkspaceBackend(ctx context.Context, st TargetStore, workspaceKey string) (string, Source, error) {
	if workspaceKey == "" {
		return "", "", fmt.Errorf("active workspace is required when no backend override is provided: %w", domain.ErrInvalid)
	}
	if st == nil {
		return "", "", errors.New("local backend target store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	profile, err := st.Daemon().Get(ctx, workspaceKey)
	if err != nil {
		return "", "", fmt.Errorf("get daemon profile for workspace %q: %w", workspaceKey, err)
	}
	if profile != nil {
		if backend := strings.TrimSpace(profile.AgentBackend); backend != "" {
			return backend, SourceWorkspace, nil
		}
	}
	return DefaultBackend, SourceDefault, nil
}
