package memstore

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// The optional task-run Input payload is persisted on create and returned
// verbatim from Get, List, and ClaimQueued. A nil Input round-trips as nil
// (back-compat: runs created without a payload behave exactly as before).
func TestTaskRunInputRoundTripMem(t *testing.T) {
	reviewInput := json.RawMessage(`{"kind":"github_review","prNumber":42,"diff":"@@ -1 +1 @@","rubric":["clarity"]}`)
	cases := []struct {
		name string
		in   json.RawMessage
		want json.RawMessage
	}{
		{name: "with review payload", in: reviewInput, want: reviewInput},
		{name: "nil payload back-compat", in: nil, want: nil},
		{name: "empty slice normalizes to nil", in: json.RawMessage{}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s := New()
			if _, err := s.Nodes().Create(ctx, store.NodeCreate{
				WorkspaceKey:    "WS",
				NodeID:          "node-1",
				RuntimeProvider: domain.RuntimeProviderLocal,
				DrainState:      domain.NodeDrainActive,
				TTL:             time.Hour,
			}); err != nil {
				t.Fatalf("Create node: %v", err)
			}

			created, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
				WorkspaceKey: "WS",
				TaskRunID:    "run-1",
				TaskID:       "WS-1",
				Status:       domain.TaskRunQueued,
				Input:        tc.in,
			})
			if err != nil {
				t.Fatalf("Create task run: %v", err)
			}
			assertInput(t, "create", created.Input, tc.want)

			got, err := s.TaskRuns().Get(ctx, "WS", "run-1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			assertInput(t, "get", got.Input, tc.want)

			runs, err := s.TaskRuns().List(ctx, "WS", store.TaskRunFilter{})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(runs) != 1 {
				t.Fatalf("List returned %d runs, want 1", len(runs))
			}
			assertInput(t, "list", runs[0].Input, tc.want)

			claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
				NodeID:  "node-1",
				LeaseID: "lease-1",
			})
			if err != nil {
				t.Fatalf("ClaimQueued: %v", err)
			}
			assertInput(t, "claim", claimed.Input, tc.want)

			// Deep-copy guarantee: mutating the returned payload must not
			// corrupt the stored copy.
			if len(claimed.Input) > 0 {
				claimed.Input[0] = 'X'
				again, err := s.TaskRuns().Get(ctx, "WS", "run-1")
				if err != nil {
					t.Fatalf("Get after mutate: %v", err)
				}
				assertInput(t, "post-mutate get", again.Input, tc.want)
			}
		})
	}
}

func assertInput(t *testing.T, stage string, got, want json.RawMessage) {
	t.Helper()
	if len(want) == 0 {
		if got != nil {
			t.Fatalf("%s: Input = %q, want nil", stage, got)
		}
		return
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: Input = %q, want %q", stage, got, want)
	}
}
