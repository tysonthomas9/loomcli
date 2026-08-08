package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPlacementReaperProvisioningMissingSandboxRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name       string
		sandboxID  string
		deadline   time.Time
		wantState  domain.PlacementState
		wantActed  int
		wantAction string
	}{
		{
			name:       "R2 deadline past empty sandbox id releases reservation",
			deadline:   now.Add(-time.Minute),
			wantState:  domain.PlacementStateReleased,
			wantActed:  1,
			wantAction: reaperActionMarkReleased,
		},
		{
			name:      "R2 within deadline observes only",
			deadline:  now.Add(time.Minute),
			wantState: domain.PlacementStateProvisioning,
		},
		{
			name:       "R4 deadline past recorded sandbox get 404 releases reservation",
			sandboxID:  "sandbox-missing",
			deadline:   now.Add(-time.Minute),
			wantState:  domain.PlacementStateReleased,
			wantActed:  1,
			wantAction: reaperActionMarkReleased,
		},
		{
			name:      "R4 within deadline observes only",
			sandboxID: "sandbox-missing",
			deadline:  now.Add(time.Minute),
			wantState: domain.PlacementStateProvisioning,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, provider, broker := reaperFixture(t, now)
			node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
				Generation:             7,
				State:                  domain.PlacementStateProvisioning,
				SandboxID:              tc.sandboxID,
				ProvisioningDeadlineAt: &tc.deadline,
			})
			reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

			result, err := reaper.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			got := getNode(t, st, "WS", node.NodeID)
			if got.Placement.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.Placement.State, tc.wantState)
			}
			if result.Acted != tc.wantActed {
				t.Fatalf("Acted = %d, want %d", result.Acted, tc.wantActed)
			}
			if tc.wantAction != "" {
				assertReaperAction(t, result, tc.wantAction, node.NodeID)
			}
			if provider.deleteCallCount() != 0 {
				t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
			}
		})
	}
}

func TestPlacementReaperProvisioningLabelledSandboxRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("R1 R3 within deadline observes labeled sandbox", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		deadline := now.Add(time.Minute)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:             1,
			State:                  domain.PlacementStateProvisioning,
			ProvisioningDeadlineAt: &deadline,
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateProvisioning, "")
		if result.Acted != 0 {
			t.Fatalf("Acted = %d, want 0", result.Acted)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R1 R3 deadline past adopts deletes and releases", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		deadline := now.Add(-time.Minute)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:             1,
			State:                  domain.PlacementStateProvisioning,
			ProvisioningDeadlineAt: &deadline,
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleased, "sandbox-1")
		assertReaperAction(t, result, reaperActionAdoptDelete, node.NodeID)
		if provider.deleteCallCount() != 1 {
			t.Fatalf("Delete calls = %d, want 1", provider.deleteCallCount())
		}
	})

	t.Run("R1 R3 double label dead letters", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		deadline := now.Add(-time.Minute)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:             1,
			State:                  domain.PlacementStateProvisioning,
			ProvisioningDeadlineAt: &deadline,
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		provider.addSandbox(reaperSandbox("sandbox-2", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err == nil || !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("RunOnce error = %v, want ErrConflict", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateProvisioning, "")
		if result.DeadLettered != 1 {
			t.Fatalf("DeadLettered = %d, want 1", result.DeadLettered)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})
}

func TestPlacementReaperActiveRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("R9 active Get 404 marks lost", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateActive,
			SandboxID:  "sandbox-missing",
		})
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateLost, "sandbox-missing")
		assertReaperAction(t, result, reaperActionMarkLost, node.NodeID)
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R6 active running expired node is not killed", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateActive,
			SandboxID:  "sandbox-1",
		})
		setNodeExpiresAt(t, st, node, now.Add(-time.Minute))
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateActive, "sandbox-1")
		if result.Acted != 0 {
			t.Fatalf("Acted = %d, want 0", result.Acted)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R7 stopped active is parked and never collected", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateActive,
			SandboxID:  "sandbox-1",
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxStopped, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateActive, "sandbox-1")
		if result.Acted != 0 {
			t.Fatalf("Acted = %d, want 0", result.Acted)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})
}

func TestPlacementReaperReleasingRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("R10 R11 success releases without appending abandoned ids", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:          1,
			State:               domain.PlacementStateReleasing,
			SandboxID:           "sandbox-1",
			AbandonedSandboxIDs: []string{"sandbox-1"},
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleased, "sandbox-1")
		assertStringSlicesEqual(t, got.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
		assertReaperAction(t, result, reaperActionDeleteReleased, node.NodeID)
	})

	t.Run("R10 R11 failure bumps retry fields and repeated passes do not grow abandoned ids", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		provider.deleteErr = errors.New("delete unavailable")
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:          1,
			State:               domain.PlacementStateReleasing,
			SandboxID:           "sandbox-1",
			AbandonedSandboxIDs: []string{"sandbox-1"},
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		currentNow := now
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return currentNow }})

		result, err := reaper.RunOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "delete unavailable") {
			t.Fatalf("RunOnce error = %v, want delete unavailable", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleasing, "sandbox-1")
		if got.Placement.DeleteAttempts != 1 {
			t.Fatalf("DeleteAttempts = %d, want 1", got.Placement.DeleteAttempts)
		}
		if !got.Placement.NextDeleteAt.Equal(now.Add(reaperDeleteBackoff(1))) {
			t.Fatalf("NextDeleteAt = %s, want %s", got.Placement.NextDeleteAt, now.Add(reaperDeleteBackoff(1)))
		}
		assertStringSlicesEqual(t, got.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
		assertReaperAction(t, result, reaperActionDeleteReleased, node.NodeID)

		currentNow = got.Placement.NextDeleteAt.Add(time.Second)
		_, err = reaper.RunOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "delete unavailable") {
			t.Fatalf("second RunOnce error = %v, want delete unavailable", err)
		}
		got = getNode(t, st, "WS", node.NodeID)
		if got.Placement.DeleteAttempts != 2 {
			t.Fatalf("DeleteAttempts after second pass = %d, want 2", got.Placement.DeleteAttempts)
		}
		assertStringSlicesEqual(t, got.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
	})

	t.Run("R10 R11 dry-run failure writes no retry fields", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		provider.deleteErr = errors.New("delete unavailable")
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:          1,
			State:               domain.PlacementStateReleasing,
			SandboxID:           "sandbox-1",
			AbandonedSandboxIDs: []string{"sandbox-1"},
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: false, Now: func() time.Time { return now }})

		if _, err := reaper.RunOnce(context.Background()); err != nil {
			t.Fatalf("dry-run RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleasing, "sandbox-1")
		if got.Placement.DeleteAttempts != 0 || !got.Placement.NextDeleteAt.IsZero() || got.Placement.LastDeleteError != "" {
			t.Fatalf("dry-run wrote retry state: attempts=%d next=%s err=%q",
				got.Placement.DeleteAttempts, got.Placement.NextDeleteAt, got.Placement.LastDeleteError)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("dry-run Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R10 R11 future NextDeleteAt skips pass", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		next := now.Add(time.Minute)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation:   1,
			State:        domain.PlacementStateReleasing,
			SandboxID:    "sandbox-1",
			NextDeleteAt: next,
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleasing, "sandbox-1")
		if result.Acted != 0 {
			t.Fatalf("Acted = %d, want 0", result.Acted)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})
}

func TestPlacementReaperNoRecordOrphans(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name       string
		labels     map[string]string
		createdAt  time.Time
		wantDelete bool
	}{
		{
			name:       "R14 own old labeled orphan deletes",
			labels:     reaperOrphanLabels(testDeploymentID, "missing-placement", "WS", "nova"),
			createdAt:  now.Add(-10 * time.Minute),
			wantDelete: true,
		},
		{
			name:      "R14 younger orphan skips",
			labels:    reaperOrphanLabels(testDeploymentID, "missing-placement", "WS", "nova"),
			createdAt: now.Add(-time.Second),
		},
		{
			name:   "R14 unknown CreatedAt skips",
			labels: reaperOrphanLabels(testDeploymentID, "missing-placement", "WS", "nova"),
		},
		{
			name:      "R14 foreign environment skips",
			labels:    reaperOrphanLabels("other-env", "missing-placement", "WS", "nova"),
			createdAt: now.Add(-10 * time.Minute),
		},
		{
			name: "R15 no placement label skips",
			labels: map[string]string{
				EnvironmentLabelKey: testDeploymentID,
				"loom-workspace":    "WS",
				"loom-agent":        "nova",
			},
			createdAt: now.Add(-10 * time.Minute),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, provider, broker := reaperFixture(t, now)
			provider.addSandbox(ProviderSandbox{
				ID:        "sandbox-1",
				Labels:    tc.labels,
				State:     ProviderSandboxRunning,
				CreatedAt: tc.createdAt,
			})
			reaper := NewPlacementReaper(broker, ReaperConfig{
				Enforce: true,
				Grace:   2 * time.Minute,
				Now:     func() time.Time { return now },
			})

			result, err := reaper.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			gotDelete := provider.deleteCallCount() > 0
			if gotDelete != tc.wantDelete {
				t.Fatalf("deleted = %v, want %v; result=%+v", gotDelete, tc.wantDelete, result)
			}
			if tc.wantDelete {
				assertReaperAction(t, result, reaperActionDeleteOrphan, "missing-placement")
			}
		})
	}
}

func TestPlacementReaperReleasedAndLostRows(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("R12 released still listed but Get 404 is ignored as list lag", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateReleased,
			SandboxID:  "sandbox-1",
		})
		provider.addListOnlySandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if result.DeadLettered != 0 {
			t.Fatalf("DeadLettered = %d, want 0", result.DeadLettered)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R12 released running sandbox dead letters without delete", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateReleased,
			SandboxID:  "sandbox-1",
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err == nil || !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("RunOnce error = %v, want ErrConflict", err)
		}
		if result.DeadLettered != 1 {
			t.Fatalf("DeadLettered = %d, want 1", result.DeadLettered)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})

	t.Run("R13 lost reappears is kept lost and dead-lettered", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateLost,
			SandboxID:  "sandbox-1",
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		result, err := reaper.RunOnce(context.Background())
		if err == nil || !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("RunOnce error = %v, want ErrConflict", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateLost, "sandbox-1")
		if result.DeadLettered != 1 {
			t.Fatalf("DeadLettered = %d, want 1", result.DeadLettered)
		}
		if provider.deleteCallCount() != 0 {
			t.Fatalf("Delete calls = %d, want 0", provider.deleteCallCount())
		}
	})
}

func TestPlacementReaperDryRunDoesNotWrite(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st, provider, broker := reaperFixture(t, now)
	deadline := now.Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
		Generation:             1,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &deadline,
	})
	provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))

	dryRun := NewPlacementReaper(broker, ReaperConfig{Now: func() time.Time { return now }})
	result, err := dryRun.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("dry-run RunOnce: %v", err)
	}
	if len(result.Actions) == 0 {
		t.Fatal("dry-run Actions empty, want intended diff")
	}
	got := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, got, domain.PlacementStateProvisioning, "")
	if provider.deleteCallCount() != 0 {
		t.Fatalf("dry-run Delete calls = %d, want 0", provider.deleteCallCount())
	}

	enforced := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})
	result, err = enforced.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("enforced RunOnce: %v", err)
	}
	got = getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, got, domain.PlacementStateReleased, "sandbox-1")
	assertReaperAction(t, result, reaperActionAdoptDelete, node.NodeID)
	if provider.deleteCallCount() != 1 {
		t.Fatalf("enforced Delete calls = %d, want 1", provider.deleteCallCount())
	}
}

func TestPlacementReaperSerializesWithProvision(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st, provider, broker := reaperFixture(t, now)
	provider.createResult = &CreateResult{SandboxID: "sandbox-2"}
	node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
		Generation:          1,
		State:               domain.PlacementStateReleasing,
		SandboxID:           "sandbox-1",
		AbandonedSandboxIDs: []string{"sandbox-1"},
	})
	provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	provider.deleteHook = func(string) {
		close(deleteEntered)
		<-releaseDelete
	}
	createStarted := make(chan struct{})
	provider.createHook = func(string) {
		close(createStarted)
	}
	reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})
	reaperDone := make(chan error, 1)
	go func() {
		_, err := reaper.RunOnce(context.Background())
		reaperDone <- err
	}()
	select {
	case <-deleteEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reaper delete")
	}

	provisionDone := make(chan error, 1)
	go func() {
		_, err := broker.Provision(context.Background(), testProvisionRequest("nova", 1, 2))
		provisionDone <- err
	}()
	select {
	case <-createStarted:
		t.Fatal("Provision created sandbox while reaper held placement lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if err := <-reaperDone; err != nil {
		t.Fatalf("reaper RunOnce: %v", err)
	}
	if err := <-provisionDone; err != nil {
		t.Fatalf("Provision: %v", err)
	}
	select {
	case <-createStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provision create after reaper unlock")
	}
	old := getNode(t, st, "WS", node.NodeID)
	assertStringSlicesEqual(t, old.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
}

func TestPlacementReaperDeleteConfirmationListLag(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("still listed but Get 404 confirms delete", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateReleasing,
			SandboxID:  "sandbox-1",
		})
		provider.addListOnlySandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		_, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleased, "sandbox-1")
	})

	t.Run("still listed and Get running does not confirm delete", func(t *testing.T) {
		st, provider, broker := reaperFixture(t, now)
		provider.deleteLeavesSandbox = true
		node := createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
			Generation: 1,
			State:      domain.PlacementStateReleasing,
			SandboxID:  "sandbox-1",
		})
		provider.addSandbox(reaperSandbox("sandbox-1", node.NodeID, "WS", "nova", ProviderSandboxRunning, now.Add(-5*time.Minute)))
		reaper := NewPlacementReaper(broker, ReaperConfig{Enforce: true, Now: func() time.Time { return now }})

		_, err := reaper.RunOnce(context.Background())
		if err == nil || !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("RunOnce error = %v, want ErrConflict", err)
		}
		got := getNode(t, st, "WS", node.NodeID)
		assertPlacement(t, got, domain.PlacementStateReleasing, "sandbox-1")
		if got.Placement.DeleteAttempts != 1 {
			t.Fatalf("DeleteAttempts = %d, want 1", got.Placement.DeleteAttempts)
		}
	})
}

func TestPlacementNeedsAttention(t *testing.T) {
	if PlacementNeedsAttention(nil) {
		t.Fatal("nil node needs attention")
	}
	node := &domain.Node{Placement: &domain.NodePlacement{State: domain.PlacementStateActive, DeleteAttempts: reaperDeadLetterThreshold - 1}}
	if PlacementNeedsAttention(node) {
		t.Fatal("below threshold active placement needs attention")
	}
	node.Placement.DeleteAttempts = reaperDeadLetterThreshold
	if !PlacementNeedsAttention(node) {
		t.Fatal("threshold delete attempts did not need attention")
	}
	node.Placement.DeleteAttempts = 0
	node.Placement.State = domain.PlacementStateLost
	if !PlacementNeedsAttention(node) {
		t.Fatal("lost placement did not need attention")
	}

	st := memstore.New()
	createWorkspace(t, st, "WS")
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	createPlacementNode(t, st, "WS", "lost-placement", "nova", domain.NodePlacement{
		Generation: 1,
		State:      domain.PlacementStateLost,
	})
	createPlacementNode(t, st, "WS", "retry-placement", "orion", domain.NodePlacement{
		Generation:     1,
		State:          domain.PlacementStateReleasing,
		DeleteAttempts: reaperDeadLetterThreshold,
	})

	result, err := broker.List(context.Background(), "WS")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertStringSlicesEqual(t, result.NeedsAttention, []string{"lost-placement", "retry-placement"})
}

func reaperFixture(t *testing.T, now time.Time) (store.Store, *fakeProvider, *Broker) {
	t.Helper()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	provider := &fakeProvider{}
	broker := mustBrokerWithNow(t, st, provider, now)
	return st, provider, broker
}

func reaperSandbox(id, placementID, workspaceKey, agentName string, state ProviderSandboxState, createdAt time.Time) ProviderSandbox {
	return ProviderSandbox{
		ID: id,
		Labels: map[string]string{
			PlacementLabelKey:   placementID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    workspaceKey,
			"loom-agent":        agentName,
		},
		State:     state,
		CreatedAt: createdAt,
	}
}

func reaperOrphanLabels(env, placementID, workspaceKey, agentName string) map[string]string {
	return map[string]string{
		PlacementLabelKey:   placementID,
		EnvironmentLabelKey: env,
		"loom-workspace":    workspaceKey,
		"loom-agent":        agentName,
	}
}

func setNodeExpiresAt(t *testing.T, st store.Store, node *domain.Node, expiresAt time.Time) {
	t.Helper()
	if _, err := st.Nodes().Update(context.Background(), node.WorkspaceKey, node.NodeID, store.NodeUpdate{ExpiresAt: &expiresAt}); err != nil {
		t.Fatalf("set node expires_at: %v", err)
	}
}

func assertReaperAction(t *testing.T, result ReaperResult, actionName, nodeID string) {
	t.Helper()
	for _, action := range result.Actions {
		if action.Action == actionName && action.NodeID == nodeID {
			return
		}
	}
	t.Fatalf("missing action %q for node %q in %#v", actionName, nodeID, result.Actions)
}
