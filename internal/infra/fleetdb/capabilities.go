package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	// CapabilitiesAPIPath is the deployment-level FleetDB capability manifest.
	// It is intentionally workspace-independent: readiness must fail before a
	// capability-specific mutation is attempted in any workspace.
	CapabilitiesAPIPath = "/api/v1/capabilities"

	// SupportedCapabilitiesAPIRevision is the manifest schema this client can
	// interpret. Capability keys carry their own versions independently.
	SupportedCapabilitiesAPIRevision = "v1"

	// WorkflowCatalogVersionLifecycleCapability is required when Loom's
	// catalog lifecycle slice is enabled.
	WorkflowCatalogVersionLifecycleCapability = "workflow_catalog.version_lifecycle.v1"
	// AutomationTriggerAdmissionCapability is required when Loom composes the
	// Phase 3 Automation core, ingress workflows, and runtime components. The
	// running FleetDB advertises it only for a backend with full contract parity.
	AutomationTriggerAdmissionCapability = "automation.trigger_admission.v1"
	// ExecutionAwaitAtomicResumeCapability is required by every Loom serve
	// profile. Await dispatch and run-outcome reconciliation must never fall
	// back to separate resolve/resume writes, even when the Catalog and
	// Automation slices are disabled.
	ExecutionAwaitAtomicResumeCapability = "execution.await_atomic_resume.v1"
	// ExecutionTaskRunLeaseFencingCapability certifies atomic TaskRun claim,
	// generic-Lease creation/release, and monotonic owner fencing across the
	// running backend. Phase 4 Execution has no unfenced compatibility path.
	ExecutionTaskRunLeaseFencingCapability = "execution.task_run_lease_fencing.v1"
	// ExecutionIssueClaimTaskRunStartCapability certifies the coordinating
	// command that claims one Work Item and starts its TaskRun in the same
	// Redis transaction or PostgreSQL transaction, including replay fencing.
	ExecutionIssueClaimTaskRunStartCapability     = "execution.issue_claim_task_run_start.v1"
	ExecutionIssueClaimNextTaskRunStartCapability = "execution.issue_claim_next_task_run_start.v1"
	ExecutionDriverStepTerminalRepairCapability   = "execution.driver_step_terminal_repair.v1"
	ExecutionTaskRunRequestCapability             = "execution.task_run_request.v1"
	ExecutionTaskRunRequeueCapability             = "execution.task_run_requeue.v1"
	ExecutionTaskRunRetryExhaustionCapability     = "execution.task_run_retry_exhaustion.v1"
	ExecutionDriverRunChildStartCapability        = "execution.driver_run_child_start.v1"
	ExecutionDriverRunChildCascadeCapability      = "execution.driver_run_child_cascade.v1"
	ExecutionDriverRunLeaseFencingCapability      = "execution.driver_run_lease_fencing.v1"
	ExecutionDriverRunWorkItemClaimCapability     = "execution.driver_run_work_item_claim.v1"
	ExecutionTaskRunLogIdempotencyCapability      = "execution.task_run_log_idempotency.v1"
	// ArtifactsOwnerFencedLifecycleCapability certifies owner-fenced,
	// idempotent Artifact create/upload/finalize/reference commands.
	ArtifactsOwnerFencedLifecycleCapability = "artifacts.owner_fenced_lifecycle.v1"
)

// Phase4FoundationCapabilities is the complete indivisible Execution and
// Artifacts deployment profile. Await atomic resume is an older always-on
// invariant and is intentionally required separately by serve.
func Phase4FoundationCapabilities() []string {
	return []string{
		ArtifactsOwnerFencedLifecycleCapability,
		ExecutionIssueClaimTaskRunStartCapability,
		ExecutionTaskRunLeaseFencingCapability,
		ExecutionIssueClaimNextTaskRunStartCapability,
		ExecutionDriverStepTerminalRepairCapability,
		ExecutionTaskRunRequestCapability,
		ExecutionTaskRunRequeueCapability,
		ExecutionTaskRunRetryExhaustionCapability,
		ExecutionDriverRunChildStartCapability,
		ExecutionDriverRunChildCascadeCapability,
		ExecutionDriverRunLeaseFencingCapability,
		ExecutionDriverRunWorkItemClaimCapability,
		ExecutionTaskRunLogIdempotencyCapability,
	}
}

// CapabilityIncompatibilityKind identifies why the running FleetDB deployment
// cannot satisfy the capabilities required by the enabled Loom slices.
type CapabilityIncompatibilityKind string

const (
	CapabilityEndpointUnavailable CapabilityIncompatibilityKind = "endpoint_unavailable"
	CapabilityRevisionUnsupported CapabilityIncompatibilityKind = "revision_unsupported"
	CapabilityKeysMissing         CapabilityIncompatibilityKind = "keys_missing"
)

// CapabilityIncompatibilityError is returned when FleetDB is reachable but its
// deployment capability manifest is absent or incompatible. Callers should use
// errors.As rather than parsing Error. Required and Missing are normalized,
// sorted, and unique so startup failures are stable and actionable.
type CapabilityIncompatibilityError struct {
	Kind        CapabilityIncompatibilityKind
	APIRevision string
	Required    []string
	Missing     []string
	Cause       error
}

func (e *CapabilityIncompatibilityError) Error() string {
	if e == nil {
		return "fleetdb: incompatible deployment"
	}
	required := strings.Join(e.Required, ", ")
	switch e.Kind {
	case CapabilityEndpointUnavailable:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities endpoint %s is unavailable (HTTP 404); required capabilities: %s",
			CapabilitiesAPIPath,
			required,
		)
	case CapabilityRevisionUnsupported:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities API revision %q is unsupported (want %q); required capabilities: %s",
			e.APIRevision,
			SupportedCapabilitiesAPIRevision,
			required,
		)
	case CapabilityKeysMissing:
		return fmt.Sprintf(
			"fleetdb: incompatible deployment: capabilities API revision %q is missing required capabilities: %s",
			e.APIRevision,
			strings.Join(e.Missing, ", "),
		)
	default:
		return fmt.Sprintf("fleetdb: incompatible deployment: required capabilities: %s", required)
	}
}

// Unwrap retains the underlying HTTP 404 for diagnostics while the typed error
// remains the authoritative classification for startup/readiness handling.
func (e *CapabilityIncompatibilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type capabilityManifest struct {
	APIRevision  string   `json:"api_revision"`
	Capabilities []string `json:"capabilities"`
}

// RequireCapabilities verifies that the running FleetDB deployment advertises
// every caller-supplied capability key. An empty normalized requirement set is
// a strict no-op, preserving compatibility for Loom configurations that have
// not enabled a capability-negotiated slice.
func (c *Client) RequireCapabilities(ctx context.Context, requiredKeys []string) error {
	required := normalizeCapabilityKeys(requiredKeys)
	if len(required) == 0 {
		return nil
	}

	var manifest capabilityManifest
	if err := c.do(ctx, http.MethodGet, CapabilitiesAPIPath, nil, &manifest); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &CapabilityIncompatibilityError{
				Kind:     CapabilityEndpointUnavailable,
				Required: required,
				Cause:    err,
			}
		}
		return fmt.Errorf(
			"fleetdb: check required capabilities %s: %w",
			strings.Join(required, ", "),
			err,
		)
	}

	manifest.APIRevision = strings.TrimSpace(manifest.APIRevision)
	if manifest.APIRevision != SupportedCapabilitiesAPIRevision {
		return &CapabilityIncompatibilityError{
			Kind:        CapabilityRevisionUnsupported,
			APIRevision: manifest.APIRevision,
			Required:    required,
		}
	}

	available := normalizeCapabilityKeys(manifest.Capabilities)
	missing := missingCapabilityKeys(required, available)
	if len(missing) != 0 {
		return &CapabilityIncompatibilityError{
			Kind:        CapabilityKeysMissing,
			APIRevision: manifest.APIRevision,
			Required:    required,
			Missing:     missing,
		}
	}
	return nil
}

func normalizeCapabilityKeys(keys []string) []string {
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			unique[key] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(unique))
	for key := range unique {
		normalized = append(normalized, key)
	}
	sort.Strings(normalized)
	return normalized
}

func missingCapabilityKeys(required, available []string) []string {
	availableSet := make(map[string]struct{}, len(available))
	for _, key := range available {
		availableSet[key] = struct{}{}
	}
	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := availableSet[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}
