package placement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func latestLivePlacement(nodes []*domain.Node) *domain.Node {
	var out *domain.Node
	for _, node := range nodes {
		if node != nil && node.Placement != nil && isLivePlacementState(node.Placement.State) {
			out = laterPlacement(out, node)
		}
	}
	return out
}

func latestPlacement(nodes []*domain.Node) *domain.Node {
	var out *domain.Node
	for _, node := range nodes {
		if node != nil && node.Placement != nil {
			out = laterPlacement(out, node)
		}
	}
	return out
}

func laterPlacement(current, candidate *domain.Node) *domain.Node {
	if current == nil {
		return candidate
	}
	if candidate.Placement.Generation > current.Placement.Generation {
		return candidate
	}
	if candidate.Placement.Generation == current.Placement.Generation && candidate.UpdatedAt.After(current.UpdatedAt) {
		return candidate
	}
	return current
}

func isLivePlacementState(state domain.PlacementState) bool {
	switch state {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive, domain.PlacementStateReleasing:
		return true
	default:
		return false
	}
}

func isQuotaReservedPlacementState(state domain.PlacementState) bool {
	switch state {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive, domain.PlacementStateReleasing, domain.PlacementStateLost:
		return true
	default:
		return false
	}
}

func nodeMatchesAgent(node *domain.Node, agentName string) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	if node.OwnerActor == agentOwnerActor(agentName) {
		return true
	}
	return hasLabel(node.Labels, "loom-agent="+agentName)
}

func placementAgentName(node *domain.Node) string {
	if node == nil {
		return ""
	}
	if name, ok := strings.CutPrefix(node.OwnerActor, "agent:"); ok {
		return strings.TrimSpace(name)
	}
	for _, label := range node.Labels {
		if name, ok := strings.CutPrefix(label, "loom-agent="); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func providerSandboxMatchesPlacement(sandbox ProviderSandbox, placementID string) bool {
	return strings.TrimSpace(sandbox.Labels[PlacementLabelKey]) == strings.TrimSpace(placementID)
}

func leadProcessRecorded(node *domain.Node) bool {
	return node != nil && node.Placement != nil && node.Placement.LeadProcessStartedAt != nil
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func addReservation(total *ResourceSize, placement *domain.NodePlacement) {
	total.VCPU += placement.ReservedVCPU
	total.MemGiB += placement.ReservedMemGiB
}

func normalizeCaps(caps []string) []string {
	out := make([]string, 0, len(caps)+1)
	for _, cap := range caps {
		if cap = strings.TrimSpace(cap); cap != "" {
			out = append(out, cap)
		}
	}
	if len(out) == 0 {
		return []string{CapLeadSession}
	}
	return out
}

func agentOwnerActor(agentName string) string {
	return "agent:" + agentName
}

func clonePlacement(in *domain.NodePlacement) domain.NodePlacement {
	if in == nil {
		return domain.NodePlacement{}
	}
	out := *in
	out.AbandonedSandboxIDs = append([]string(nil), in.AbandonedSandboxIDs...)
	return out
}

func (b *Broker) provisioningDeadline() time.Time {
	return b.now().UTC().Add(b.provisioningTimeout)
}

func (b *Broker) provisioningDeadlineExpired(node *domain.Node) bool {
	if node == nil || node.Placement == nil {
		return false
	}
	deadline := node.Placement.ProvisioningDeadlineAt
	if deadline == nil {
		fallback := node.CreatedAt
		if fallback.IsZero() {
			fallback = node.UpdatedAt
		}
		if fallback.IsZero() {
			fallback = b.now().UTC()
		}
		d := fallback.Add(b.provisioningTimeout)
		deadline = &d
	}
	return !b.now().UTC().Before(deadline.UTC())
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func detachedTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func newPlacementID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "lead-placement-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	}
	return "lead-placement-" + hex.EncodeToString(buf)
}
