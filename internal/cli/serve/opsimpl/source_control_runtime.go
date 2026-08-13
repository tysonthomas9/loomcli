package opsimpl

import (
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/filesystem"
)

// SourceControlRuntime composes Source Control's owner ports over private
// machine-local adapters. The serve command receives only this narrow runtime
// handle instead of importing every adapter detail itself.
type SourceControlRuntime struct {
	mechanics *LocalSourceControlMechanics
	ports     sourcecontrol.WorkspacePorts
	grants    sourcecontrol.AccessGrantIssuer
}

// NewSourceControlRuntime creates the machine-local Source Control runtime.
func NewSourceControlRuntime() (*SourceControlRuntime, error) {
	mechanics := NewLocalSourceControlMechanics()
	adapter := NewSourceControlAdapter(mechanics)
	grants := sourcecontrol.NewAccessGrantIssuer()
	ports, err := sourcecontrol.NewWorkspacePorts(
		grants,
		adapter,
		adapter,
		filesystem.New(adapter),
		adapter,
		adapter,
	)
	if err != nil {
		return nil, err
	}
	return &SourceControlRuntime{mechanics: mechanics, ports: ports, grants: grants}, nil
}

// Browse exposes Source Control's read-only workspace capability.
func (runtime *SourceControlRuntime) Browse() sourcecontrol.Browse {
	return runtime.ports.Browse
}

// Mutate exposes Source Control's workspace mutation capability.
func (runtime *SourceControlRuntime) Mutate() sourcecontrol.Mutate {
	return runtime.ports.Mutate
}

// Checkout exposes Source Control's checkout and publication capability.
func (runtime *SourceControlRuntime) Checkout() sourcecontrol.Checkout {
	return runtime.ports.Checkout
}

// AccessGrants exposes the seal-bound issuer used only by trusted HTTP
// composition to mint request-scoped Source Control grants.
func (runtime *SourceControlRuntime) AccessGrants() sourcecontrol.AccessGrantIssuer {
	return runtime.grants
}

// WithWorkspaceProjection supplies the two exact workspace queries required by
// machine-local Source Control mechanics.
func (runtime *SourceControlRuntime) WithWorkspaceProjection(projection WorkspaceProjection) *SourceControlRuntime {
	if runtime != nil {
		runtime.mechanics.WithWorkspaceProjection(projection)
	}
	return runtime
}

// WithAgentQueries supplies the canonical Agent identity projection used for
// worktree placement without exposing the concrete mechanics to serve.
func (runtime *SourceControlRuntime) WithAgentQueries(queries agents.IdentityQueries) *SourceControlRuntime {
	if runtime != nil {
		runtime.mechanics.WithAgentQueries(queries)
	}
	return runtime
}
