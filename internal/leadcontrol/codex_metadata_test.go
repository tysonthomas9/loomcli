package leadcontrol

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestUpdateCodexRuntimeMetadataThreadID(t *testing.T) {
	tests := []struct {
		name    string
		runtime CodexRuntimeMetadata
		want    string
		present bool
	}{
		{
			name: "clear deletes the existing id",
			runtime: CodexRuntimeMetadata{
				ThreadID:      "replacement-thread",
				ClearThreadID: true,
			},
		},
		{
			name:    "non-empty id writes",
			runtime: CodexRuntimeMetadata{ThreadID: "replacement-thread"},
			want:    "replacement-thread",
			present: true,
		},
		{
			name:    "unset leaves the existing id unchanged",
			want:    "existing-thread",
			present: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "WS",
				Name:         "nova",
				RoleName:     "lead",
				Backend:      "codex",
			}); err != nil {
				t.Fatalf("create lead: %v", err)
			}
			if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
				WorkspaceKey: "WS",
				SessionID:    "lead-session",
				AgentID:      "nova",
				Kind:         domain.AgentSessionKindOrchestration,
				Status:       domain.AgentSessionRunning,
				Metadata: map[string]string{
					MetadataCodexThreadID: "existing-thread",
				},
			}); err != nil {
				t.Fatalf("create lead session: %v", err)
			}

			if err := UpdateCodexRuntimeMetadata(ctx, st, "WS", "lead-session", tt.runtime); err != nil {
				t.Fatalf("UpdateCodexRuntimeMetadata() error = %v", err)
			}
			session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
			if err != nil {
				t.Fatalf("get lead session: %v", err)
			}
			got, present := session.Metadata[MetadataCodexThreadID]
			if got != tt.want || present != tt.present {
				t.Fatalf("thread metadata = %q, %v; want %q, %v", got, present, tt.want, tt.present)
			}
		})
	}
}
