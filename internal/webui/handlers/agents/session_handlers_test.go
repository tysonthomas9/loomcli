package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestAgentSessionTranscriptRouteReturnsCanonicalEntriesAndEnforcesOwner(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	body := []byte(`{"seq":1,"timestamp":"2026-07-24T12:00:00Z","role":"assistant","type":"text","text":"review complete"}` + "\n")
	finalized, err := store.UploadContentArtifact(ctx, st.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  agentRecordTestWS,
		ArtifactID:    "transcript-interactive-1",
		AgentID:       "local-review",
		SessionID:     "interactive-1",
		OwnerType:     "session",
		OwnerID:       "interactive-1",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
	}, body)
	if err != nil {
		t.Fatalf("create transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: agentRecordTestWS,
		SessionID:    "interactive-1",
		AgentID:      "local-review",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionCompleted,
		Metadata:     map[string]string{"transcript_ref": "artifact://" + finalized.ArtifactID},
	}); err != nil {
		t.Fatalf("create interactive session: %v", err)
	}
	transcripts, ok := svcimpl.NewSessionService(st, nil).(service.AgentSessionTranscriptService)
	if !ok {
		t.Fatal("session service does not implement AgentSessionTranscriptService")
	}
	module := New(Config{
		Store:              st,
		SessionTranscripts: transcripts,
		WorkspaceFromContext: func(context.Context) string {
			return agentRecordTestWS
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)

	rec := doAgentRequest(
		t,
		mux,
		http.MethodGet,
		"/api/workspaces/WS/agents/local-review/sessions/interactive-1/transcript",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("transcript status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response agentSessionTranscriptResponse
	decodeJSON(t, rec.Body.Bytes(), &response)
	if !response.Success || response.Data == nil || response.Data.SessionID != "interactive-1" ||
		len(response.Data.Entries) != 1 || response.Data.Entries[0].Text != "review complete" {
		t.Fatalf("transcript response = %+v", response)
	}

	rec = doAgentRequest(
		t,
		mux,
		http.MethodGet,
		"/api/workspaces/WS/agents/another-agent/sessions/interactive-1/transcript",
		"",
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-agent transcript status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	for _, test := range []struct {
		name       string
		ref        string
		artifact   *store.ArtifactCreate
		wantStatus int
	}{
		{
			name: "forged cross-agent artifact",
			ref:  "artifact://transcript-other-agent",
			artifact: &store.ArtifactCreate{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "transcript-other-agent",
				AgentID: "another-agent", SessionID: "another-session",
				OwnerType: "session", OwnerID: "another-session", Type: "transcript",
				MIMEType: "application/x-ndjson", DurableStatus: "declared",
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "stale cross-session artifact",
			ref:  "artifact://transcript-other-session",
			artifact: &store.ArtifactCreate{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "transcript-other-session",
				AgentID: "local-review", SessionID: "previous-session",
				OwnerType: "session", OwnerID: "previous-session", Type: "transcript",
				MIMEType: "application/x-ndjson", DurableStatus: "declared",
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "wrong artifact type",
			ref:  "artifact://patch-interactive-1",
			artifact: &store.ArtifactCreate{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "patch-interactive-1",
				AgentID: "local-review", SessionID: "interactive-1",
				OwnerType: "session", OwnerID: "interactive-1", Type: "patch",
				MIMEType: "text/x-diff", DurableStatus: "declared",
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "non-artifact reference",
			ref:        "file:///tmp/forged-agent-transcript.ndjson",
			wantStatus: http.StatusNotFound,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.artifact != nil {
				if _, err := store.UploadContentArtifact(ctx, st.Artifacts(), *test.artifact, body); err != nil {
					t.Fatalf("create forged transcript artifact: %v", err)
				}
			}
			metadata := map[string]string{"transcript_ref": test.ref}
			if _, err := st.AgentSessions().Update(ctx, agentRecordTestWS, "interactive-1", store.AgentSessionUpdate{
				Metadata: &metadata,
			}); err != nil {
				t.Fatalf("update transcript ref: %v", err)
			}
			rec := doAgentRequest(
				t,
				mux,
				http.MethodGet,
				"/api/workspaces/WS/agents/local-review/sessions/interactive-1/transcript",
				"",
			)
			if rec.Code != test.wantStatus {
				t.Fatalf("transcript status = %d body=%s, want %d", rec.Code, rec.Body.String(), test.wantStatus)
			}
		})
	}
}

func TestAgentSessionTranscriptRouteFailsClosedWithoutService(t *testing.T) {
	module := New(Config{
		Store: memstore.New(),
		WorkspaceFromContext: func(context.Context) string {
			return agentRecordTestWS
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)

	rec := doAgentRequest(
		t,
		mux,
		http.MethodGet,
		"/api/workspaces/WS/agents/local-review/sessions/interactive-1/transcript",
		"",
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing service status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	var response agentSessionTranscriptResponse
	decodeJSON(t, rec.Body.Bytes(), &response)
	if response.Success || response.Error == "" {
		t.Fatalf("missing service response = %+v", response)
	}
}

func TestAgentSessionTranscriptRoutePreservesUnavailable(t *testing.T) {
	module := New(Config{
		Store: memstore.New(),
		SessionTranscripts: agentSessionTranscriptErrorService{
			err: service.ErrUnavailable("transcript content is temporarily unavailable"),
		},
		WorkspaceFromContext: func(context.Context) string {
			return agentRecordTestWS
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)

	rec := doAgentRequest(
		t,
		mux,
		http.MethodGet,
		"/api/workspaces/WS/agents/local-review/sessions/interactive-1/transcript",
		"",
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("transcript status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	var response agentSessionTranscriptResponse
	decodeJSON(t, rec.Body.Bytes(), &response)
	if response.Success || response.Error != "transcript content is temporarily unavailable" {
		t.Fatalf("transcript response = %+v", response)
	}
}

type agentSessionTranscriptErrorService struct{ err error }

func (s agentSessionTranscriptErrorService) GetAgentSessionTranscript(
	context.Context,
	string,
	string,
	string,
) ([]transcript.Event, error) {
	return nil, s.err
}
