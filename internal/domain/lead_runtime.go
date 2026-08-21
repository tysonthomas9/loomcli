package domain

import (
	"fmt"
	"strings"
	"time"
)

// LeadRuntimeStatus describes whether a Daytona interactive lead has a usable
// remote runtime. It is intentionally separate from Agent.State, which is a
// coarse control-plane assignment state.
type LeadRuntimeStatus string

const (
	LeadRuntimeNotProvisioned LeadRuntimeStatus = "not_provisioned"
	LeadRuntimeProvisioning   LeadRuntimeStatus = "provisioning"
	LeadRuntimeReady          LeadRuntimeStatus = "ready"
	LeadRuntimeDegraded       LeadRuntimeStatus = "degraded"
	LeadRuntimeReleasing      LeadRuntimeStatus = "releasing"
	LeadRuntimeReleased       LeadRuntimeStatus = "released"
	LeadRuntimeLost           LeadRuntimeStatus = "lost"
)

const (
	LeadProvisionOutcomeInProgress = "in_progress"
	LeadProvisionOutcomeSucceeded  = "succeeded"
	LeadProvisionOutcomeFailed     = "failed"
)

// LeadProvisionAttempt is the durable evidence for an eager Daytona lead
// provision attempt. Empty Outcome means no attempt is known to be in flight.
type LeadProvisionAttempt struct {
	Outcome string
	Error   string
	At      *time.Time
}

// LeadRuntimeStatusFor is the single placement-to-runtime-status projector for
// Daytona interactive leads. The returned detail is suitable for runtime_error;
// transient states have no error detail merely because they are not ready yet.
func LeadRuntimeStatusFor(node *Node, attempt LeadProvisionAttempt) (LeadRuntimeStatus, string) {
	if node == nil || node.Placement == nil {
		switch strings.TrimSpace(attempt.Outcome) {
		case LeadProvisionOutcomeInProgress:
			return LeadRuntimeProvisioning, ""
		case LeadProvisionOutcomeFailed:
			return LeadRuntimeNotProvisioned, strings.TrimSpace(attempt.Error)
		default:
			return LeadRuntimeNotProvisioned, ""
		}
	}

	placement := node.Placement
	switch placement.State {
	case PlacementStateProvisioning:
		return LeadRuntimeProvisioning, ""
	case PlacementStateActive:
		if strings.TrimSpace(placement.SandboxID) == "" {
			return LeadRuntimeDegraded, "lead sandbox active placement has no sandbox id"
		}
		if placement.LeadProcessStartedAt == nil {
			return LeadRuntimeDegraded, "lead sandbox active placement has no durable lead-boot evidence"
		}
		return LeadRuntimeReady, ""
	case PlacementStateReleasing:
		return LeadRuntimeReleasing, ""
	case PlacementStateReleased:
		return LeadRuntimeReleased, leadRuntimeReleaseDetail(placement)
	case PlacementStateLost:
		return LeadRuntimeLost, leadRuntimeReleaseDetail(placement)
	default:
		return LeadRuntimeDegraded, fmt.Sprintf("lead sandbox state %q is not attachable", placement.State)
	}
}

// LeadRuntimeAttachError returns the terminal-facing message for a projected
// runtime status. Keeping these strings beside LeadRuntimeStatusFor prevents the
// monitor and terminal paths from growing independent placement interpretations.
func LeadRuntimeAttachError(status LeadRuntimeStatus, detail string) string {
	detail = strings.TrimSpace(detail)
	switch status {
	case LeadRuntimeProvisioning:
		return "lead sandbox is still provisioning"
	case LeadRuntimeReleasing:
		return "lead sandbox is releasing"
	case LeadRuntimeReleased, LeadRuntimeLost:
		if detail != "" {
			return "lead sandbox provisioning failed: " + detail
		}
		return fmt.Sprintf("lead sandbox provisioning failed: runtime status %q", status)
	case LeadRuntimeDegraded:
		if detail != "" {
			return detail
		}
		return "lead sandbox placement is degraded"
	case LeadRuntimeNotProvisioned:
		if detail != "" {
			return "lead sandbox provisioning failed: " + detail
		}
		return "Daytona lead has no active placement to attach"
	case LeadRuntimeReady:
		return ""
	default:
		return fmt.Sprintf("lead sandbox runtime status %q is not attachable", status)
	}
}

func leadRuntimeReleaseDetail(placement *NodePlacement) string {
	if placement == nil {
		return ""
	}
	if reason := strings.TrimSpace(string(placement.ReleaseReason)); reason != "" {
		return reason
	}
	return strings.TrimSpace(placement.LastDeleteError)
}
