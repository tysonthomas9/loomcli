package driver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workspaceReadStore interface {
	Workspaces() workspaceowner.WorkspaceStore
}

type awaitTimeoutStore interface {
	workspaceReadStore
	Awaits() execution.AwaitStore
	DriverRuns() execution.DriverRunStore
}

type executorStore interface {
	workspaceReadStore
	Awaits() execution.AwaitStore
	Drivers() workflowcatalog.DriverStore
	DriverVersions() workflowcatalog.DriverVersionStore
	DriverRuns() execution.DriverRunStore
	TriggerEvents() automation.TriggerEventStore
}

type taskWorkerStore interface {
	workspaceReadStore
	DriverVersions() workflowcatalog.DriverVersionStore
	Repos() workspaceowner.RepoStore
	WorkerProfiles() execution.WorkerProfileStore
}

type driverRunReadStore interface {
	Drivers() workflowcatalog.DriverStore
	DriverVersions() workflowcatalog.DriverVersionStore
	TriggerEvents() automation.TriggerEventStore
}

// CompositionMaxDepthEnvVar overrides the composition depth cap (a positive
// integer: the deepest ParentRunID chain a child run may sit on).
const CompositionMaxDepthEnvVar = "LOOM_COMPOSITION_MAX_DEPTH"

// DefaultCompositionMaxDepth bounds workflow nesting when no override is
// configured, matching the internal-event hop-depth cap.
const DefaultCompositionMaxDepth = automation.DefaultInternalEventHopDepthCap

// ChildRunSourceKind is the immutable provenance discriminator for a child
// DriverRun created through Execution's parent-fenced composition command.
const ChildRunSourceKind = "workflow"

// CancelErrorClassParentTerminal is the error class used by Execution's
// atomic terminal-parent child cascade.
const CancelErrorClassParentTerminal = "parent_run_terminal"

// ChildWorkflowRunID is the public deterministic identity projection retained
// for SDK/transport compatibility. Creation itself belongs to Execution.
func ChildWorkflowRunID(parentRunID, key string) string {
	digest := sha256.Sum256([]byte("loom-child:" + parentRunID + ":" + key))
	return "run-" + hex.EncodeToString(digest[:16])
}

// ResolveChildWorkflowStartKey validates the deterministic child key supplied
// by the workflow SDK. Execution derives the child run identity from this key
// and the authenticated parent owner envelope.
func ResolveChildWorkflowStartKey(idempotencyKey string, startIndex int) (string, error) {
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		return key, nil
	}
	if startIndex >= 1 {
		return "start-" + strconv.Itoa(startIndex), nil
	}
	return "", fmt.Errorf("idempotencyKey or startIndex >= 1 required for a deterministic child run id: %w", persistence.ErrInvalid)
}

func compositionMaxDepthFromEnv() int {
	if raw := strings.TrimSpace(os.Getenv(CompositionMaxDepthEnvVar)); raw != "" {
		if depth, err := strconv.Atoi(raw); err == nil && depth > 0 {
			return depth
		}
	}
	return DefaultCompositionMaxDepth
}

// ResolveCompositionMaxDepth snapshots the shipped env/default depth policy
// for an injected Execution command. Capability core receives the resolved
// value and does not read host environment itself.
func ResolveCompositionMaxDepth() int {
	return compositionMaxDepthFromEnv()
}

// ValidateSatisfiedChildAwait prevents historical or forged run.finished
// rows from becoming composition truth. The child terminal record is the
// authority; event identity, actor, and bounded payload must agree exactly.
func ValidateSatisfiedChildAwait(ctx context.Context, instance *execution.DriverAwaitInstance, child *execution.DriverRun) error {
	if instance == nil || child == nil || !child.Status.IsTerminal() {
		return fmt.Errorf("composition await was satisfied while child is nonterminal: %w", persistence.ErrConflict)
	}
	expectedEventID := RunFinishedEventID(child.RunID, child.Status)
	expectedPayload := marshalRunFinishedPayload(ctx, child)
	if instance.SatisfiedByEventID != expectedEventID || instance.SatisfiedActor != RunFinishedActor ||
		!bytes.Equal(instance.SatisfiedPayload, expectedPayload) {
		return fmt.Errorf(
			"composition await winner %q/%q does not match terminal child outcome %q/%q: %w",
			instance.SatisfiedByEventID, instance.SatisfiedActor,
			expectedEventID, RunFinishedActor, persistence.ErrConflict,
		)
	}
	return nil
}
