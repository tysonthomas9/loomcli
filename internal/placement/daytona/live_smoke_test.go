package daytona

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// TestLiveDaytonaSmoke exercises the money-critical mappings against the real
// API: create with labels, point-read Get, label-filtered ListManaged,
// idempotent CreatePty, ListPtySessions, Delete, and Get-confirmed absence.
//
// It creates a real, billable sandbox and always deletes it.
func TestLiveDaytonaSmoke(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_LIVE_TEST") != "1" || os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("live test disabled")
	}

	p, err := New(Config{SnapshotName: "loom-lead-poc-v2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	nodeID := "live-smoke-" + time.Now().UTC().Format("150405")
	labels := map[string]string{
		placement.PlacementLabelKey:   nodeID,
		placement.EnvironmentLabelKey: "mac-smoke",
		"loom-workspace":              "LIVE-SMOKE",
		"loom-agent":                  "smoke",
	}

	created, err := p.Create(ctx, placement.CreateRequest{
		WorkspaceKey: "LIVE-SMOKE",
		AgentName:    "smoke",
		SnapshotRef:  "loom-lead-poc-v2",
		Labels:       labels,
		Env:          map[string]string{"LOOM_SMOKE": "1"},
		Resource:     placement.ResourceSize{VCPU: 1, MemGiB: 2},
		NetworkDomainAllowlist: []string{
			"app.daytona.io", "api.anthropic.com", "registry.npmjs.org",
		},
	})
	sandboxID := created.SandboxID
	t.Cleanup(func() {
		if sandboxID == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if delErr := p.Delete(cleanupCtx, sandboxID); delErr != nil && !errors.Is(delErr, placement.ErrSandboxNotFound) {
			t.Errorf("CLEANUP FAILED, sandbox %q may still bill: %v", sandboxID, delErr)
		} else {
			t.Logf("cleanup: deleted %s", sandboxID)
		}
	})
	if err != nil {
		t.Fatalf("Create: %v (sandboxID=%q)", err, sandboxID)
	}
	t.Logf("PASS Create -> sandbox %s", sandboxID)

	got, err := p.Get(ctx, sandboxID)
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	t.Logf("PASS Get -> state=%s labels=%v", got.State, got.Labels)
	if got.Labels[placement.PlacementLabelKey] != nodeID {
		t.Errorf("placement label = %q, want %q -- label did not ride create",
			got.Labels[placement.PlacementLabelKey], nodeID)
	}
	if got.Labels[placement.EnvironmentLabelKey] != "mac-smoke" {
		t.Errorf("env label missing: %v", got.Labels)
	}

	listed, err := p.ListManaged(ctx, map[string]string{placement.PlacementLabelKey: nodeID})
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}
	t.Logf("PASS ListManaged -> %d match(es)", len(listed))

	spec := placement.ProcessSpec{
		SessionID: placement.LeadPTYSessionID,
		TTY:       true,
	}
	if err := p.CreatePty(ctx, sandboxID, spec); err != nil {
		t.Fatalf("CreatePty: %v", err)
	}
	t.Log("PASS CreatePty(lead)")

	sessions, err := p.ListPtySessions(ctx, sandboxID)
	if err != nil {
		t.Fatalf("ListPtySessions: %v", err)
	}
	t.Logf("PASS ListPtySessions -> %v", sessions)

	// The idempotency claim the broker's no-duplicate-lead guarantee rests on.
	err = p.CreatePty(ctx, sandboxID, spec)
	if errors.Is(err, placement.ErrPtySessionAlreadyExists) {
		t.Log("PASS CreatePty twice -> ErrPtySessionAlreadyExists")
	} else {
		t.Errorf("IDEMPOTENCY UNVERIFIED: second CreatePty returned %v, want ErrPtySessionAlreadyExists", err)
	}

	if err := p.Delete(ctx, sandboxID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	t.Log("PASS Delete")

	// Deletion is asynchronous: an immediate Get still succeeds. Confirmation
	// is therefore a bounded poll to a terminal state, which is exactly what
	// the broker's confirmSandboxDeleted does. Asserting a single read here
	// would be asserting a contract the provider does not offer.
	confirmed := false
	for attempt := range 10 {
		sb, getErr := p.Get(ctx, sandboxID)
		if errors.Is(getErr, placement.ErrSandboxNotFound) || (getErr == nil && sb.State == placement.ProviderSandboxAbsent) {
			t.Logf("PASS delete confirmed after %d poll(s)", attempt+1)
			confirmed = true
			sandboxID = ""
			break
		}
		if getErr != nil {
			t.Fatalf("Get while confirming delete: %v", getErr)
		}
		time.Sleep(time.Duration(attempt+1) * 400 * time.Millisecond)
	}
	if !confirmed {
		t.Error("delete never confirmed within the poll budget")
	}
}
