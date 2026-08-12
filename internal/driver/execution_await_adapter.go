package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// RunOutcomeAwaitComponentID is the registered Execution system component
// used by both the durable run-outcome reconciler and the executor's
// low-latency terminal fast path.
const RunOutcomeAwaitComponentID = "serve-driver-run-outcomes"

// ExecutionAwaitResolver adapts Execution's typed system command to the
// narrow atomic interfaces still consumed by the Phase 3 matcher and outcome
// reconciler. It contains no persistence fallback: production composition
// either injects a complete Execution capability or fails closed.
type ExecutionAwaitResolver struct {
	API         execution.DriverRunAPI
	Authorities execution.SystemAuthorityResolver
	ComponentID string
}

var (
	_ store.AtomicAwaitStore  = (*ExecutionAwaitResolver)(nil)
	_ RunOutcomeAwaitResolver = (*ExecutionAwaitResolver)(nil)
)

func (resolver *ExecutionAwaitResolver) ResolveAwaitAndResume(
	ctx context.Context,
	workspace, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
) error {
	return resolver.resolve(ctx, workspace, instanceKey, eventID, payload, actor)
}

func (resolver *ExecutionAwaitResolver) ResolveRunOutcomeAwaitAndResume(
	ctx context.Context,
	workspace, instanceKey, eventID string,
	payload json.RawMessage,
) error {
	return resolver.resolve(ctx, workspace, instanceKey, eventID, payload, RunFinishedActor)
}

func (resolver *ExecutionAwaitResolver) resolve(
	ctx context.Context,
	workspace, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
) error {
	if resolver == nil || resolver.API == nil || resolver.Authorities == nil || strings.TrimSpace(resolver.ComponentID) == "" {
		return fmt.Errorf("execution await resolver is unavailable: %w", execution.ErrUnavailable)
	}
	auth, err := resolver.Authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionResolveDriverAwait, resolver.ComponentID,
	)
	if err != nil {
		return err
	}
	return resolver.API.ResolveDriverAwait(ctx, auth, execution.ResolveDriverAwaitCommand{
		WorkspaceKey: workspace,
		RequestID:    "resolve-await:" + instanceKey + ":" + eventID,
		InstanceKey:  instanceKey,
		EventID:      eventID,
		Actor:        actor,
		Payload:      append(json.RawMessage(nil), payload...),
	})
}
