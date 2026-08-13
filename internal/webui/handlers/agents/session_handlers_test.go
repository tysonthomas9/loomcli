package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

func TestFlueTaskRunIsOwnedByTaskSessionsNotAgentHistory(t *testing.T) {
	ctx := t.Context()
	st := newAgentRecordStore(t)
	seedRole(t, st, "task")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "flue-worker", Name: "flue-worker", RoleName: "task",
		Kind: domain.AgentServiceKindSupport, DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    agentRecordTestWS,
		TaskRunID:       "task-run-shared-1",
		TaskID:          "TASK-SHARED-1",
		WorkerProfileID: "flue-worker",
		RunnerKind:      "flue-workflow",
		Status:          domain.TaskRunCompleted,
		RuntimeMetadata: map[string]string{"runtime": "flue"},
	}); err != nil {
		t.Fatalf("create Flue task run: %v", err)
	}

	rec := doAgentRequest(
		t,
		newAgentsMux(st),
		http.MethodGet,
		"/api/workspaces/WS/agents/flue-worker/runs",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent history status = %d body=%s", rec.Code, rec.Body.String())
	}
	var history agentRunsResponse
	decodeJSON(t, rec.Body.Bytes(), &history)
	if len(history.Sessions) != 0 {
		t.Fatalf("agent history sessions = %+v, want none", history.Sessions)
	}

	taskSessions, err := sessioncoord.NewSessionService(st, nil, nil).ListTaskSessions(
		ctx,
		agentRecordTestWS,
		"TASK-SHARED-1",
	)
	if err != nil {
		t.Fatalf("list task sessions: %v", err)
	}
	if len(taskSessions) != 1 {
		t.Fatalf("task sessions = %+v, want one", taskSessions)
	}
	if taskSessions[0].SessionID != "flue-task-run-shared-1" {
		t.Fatalf("task session ID = %q, want flue-task-run-shared-1", taskSessions[0].SessionID)
	}
}

func TestAgentRunsDoesNotIncludeDaemonLocalCompatibilitySession(t *testing.T) {
	ctx := t.Context()
	st := newAgentRecordStore(t)
	seedRole(t, st, "plan")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "advanced-planner", Name: "advanced-planner", RoleName: "plan",
		Kind: domain.AgentServiceKindSupport, DesiredState: domain.AgentServiceDesiredRunning, MaxInstances: 1,
	}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
	mux := http.NewServeMux()
	module.Register(mux)

	rec := doAgentRequest(
		t,
		mux,
		http.MethodGet,
		"/api/workspaces/WS/agents/advanced-planner/runs",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent history status = %d body=%s", rec.Code, rec.Body.String())
	}
	var history agentRunsResponse
	decodeJSON(t, rec.Body.Bytes(), &history)
	if len(history.Sessions) != 0 {
		t.Fatalf("retired local compatibility history leaked: %+v", history.Sessions)
	}
}

func TestAgentSessionTranscriptRouteReturnsCanonicalEntriesAndEnforcesOwner(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	body := []byte(`{"seq":1,"timestamp":"2026-07-24T12:00:00Z","role":"assistant","type":"text","text":"review complete"}` + "\n")
	finalized, err := st.SeedArtifact(ctx, artifactsmodule.Artifact{
		WorkspaceKey:  agentRecordTestWS,
		ArtifactID:    "transcript-interactive-1",
		AgentID:       "local-review",
		SessionID:     "interactive-1",
		OwnerType:     artifactsmodule.OwnerSession,
		OwnerID:       "interactive-1",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: artifactsmodule.StatusFinalized,
		Metadata: map[string]string{
			artifactsmodule.MetadataEvidenceCaptureStatus: "finalized",
			artifactsmodule.MetadataEvidenceTruncated:     "false",
		},
	}, body)
	if err != nil {
		t.Fatalf("create transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: agentRecordTestWS,
		SessionID:    "interactive-1",
		AgentID:      "local-review",
		Kind:         domain.AgentSessionKindInteractive,
		Status:       domain.AgentSessionCompleted,
		Metadata:     map[string]string{"transcript_ref": "artifact://" + finalized.ArtifactID},
	}); err != nil {
		t.Fatalf("create interactive session: %v", err)
	}
	artifactQueries, err := artifactsmodule.NewQuery(st.ArtifactQueries())
	if err != nil {
		t.Fatalf("compose artifact queries: %v", err)
	}
	captures, err := runcapture.New(
		agentTranscriptExecutionQueries{},
		agentTranscriptInteractionQueries{store: st.AgentSessions()},
		artifactQueries,
	)
	if err != nil {
		t.Fatalf("compose run captures: %v", err)
	}
	transcripts, ok := sessioncoord.NewSessionService(
		st, nil, captures,
	).(sessioncoord.AgentSessionTranscriptService)
	if !ok {
		t.Fatal("session service does not implement AgentSessionTranscriptService")
	}
	module := New(Config{
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
		artifact   *artifactsmodule.Artifact
		wantStatus int
	}{
		{
			name: "forged cross-agent artifact",
			ref:  "artifact://transcript-other-agent",
			artifact: &artifactsmodule.Artifact{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "transcript-other-agent",
				AgentID: "another-agent", SessionID: "another-session",
				OwnerType: artifactsmodule.OwnerSession, OwnerID: "another-session", Type: "transcript",
				MIMEType: "application/x-ndjson", DurableStatus: artifactsmodule.StatusFinalized,
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "stale cross-session artifact",
			ref:  "artifact://transcript-other-session",
			artifact: &artifactsmodule.Artifact{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "transcript-other-session",
				AgentID: "local-review", SessionID: "previous-session",
				OwnerType: artifactsmodule.OwnerSession, OwnerID: "previous-session", Type: "transcript",
				MIMEType: "application/x-ndjson", DurableStatus: artifactsmodule.StatusFinalized,
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "wrong artifact type",
			ref:  "artifact://patch-interactive-1",
			artifact: &artifactsmodule.Artifact{
				WorkspaceKey: agentRecordTestWS, ArtifactID: "patch-interactive-1",
				AgentID: "local-review", SessionID: "interactive-1",
				OwnerType: artifactsmodule.OwnerSession, OwnerID: "interactive-1", Type: "patch",
				MIMEType: "text/x-diff", DurableStatus: artifactsmodule.StatusFinalized,
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
				if _, err := st.SeedArtifact(ctx, *test.artifact, body); err != nil {
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
		SessionTranscripts: agentSessionTranscriptErrorService{
			err: apperrors.ErrUnavailable("transcript content is temporarily unavailable"),
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

type agentTranscriptExecutionQueries struct{}

func (agentTranscriptExecutionQueries) GetTaskRun(context.Context, string, string) (*execution.TaskRun, error) {
	return nil, execution.ErrNotFound
}

func (agentTranscriptExecutionQueries) ListTaskRuns(context.Context, execution.TaskRunArchiveQuery) ([]*execution.TaskRun, error) {
	return nil, nil
}

func (agentTranscriptExecutionQueries) ListActiveTaskRuns(context.Context, execution.ActiveTaskRunQuery) ([]*execution.TaskRun, error) {
	return nil, nil
}

func (agentTranscriptExecutionQueries) ListTaskRunEvents(context.Context, execution.TaskRunEventQuery) ([]*execution.TaskRunEvent, error) {
	return nil, nil
}

type agentTranscriptInteractionQueries struct{ store store.AgentSessionStore }

func (queries agentTranscriptInteractionQueries) GetSession(
	ctx context.Context,
	workspace, sessionID string,
) (*interaction.AgentSession, error) {
	value, err := queries.store.Get(ctx, workspace, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, interaction.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return agentTranscriptInteractionSnapshot(value), nil
}

func (queries agentTranscriptInteractionQueries) ListSessions(
	ctx context.Context,
	query interaction.SessionArchiveQuery,
) ([]*interaction.AgentSession, error) {
	values, err := queries.store.List(ctx, query.WorkspaceKey, store.AgentSessionFilter{
		AgentID: query.AgentID,
		TaskID:  query.WorkItemID,
		Limit:   query.Limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]*interaction.AgentSession, 0, len(values))
	for _, value := range values {
		result = append(result, agentTranscriptInteractionSnapshot(value))
	}
	return result, nil
}

func agentTranscriptInteractionSnapshot(value *domain.AgentSession) *interaction.AgentSession {
	if value == nil {
		return nil
	}
	return &interaction.AgentSession{
		WorkspaceKey:         value.WorkspaceKey,
		SessionID:            value.SessionID,
		AgentID:              value.AgentID,
		TaskID:               value.TaskID,
		Status:               interaction.SessionStatus(value.Status),
		TranscriptArtifactID: strings.TrimPrefix(value.Metadata["transcript_ref"], "artifact://"),
		Metadata:             value.Metadata,
		StartedAt:            value.StartedAt,
		FinishedAt:           value.FinishedAt,
		CreatedAt:            value.CreatedAt,
		UpdatedAt:            value.UpdatedAt,
	}
}

func (s agentSessionTranscriptErrorService) GetAgentSessionTranscript(
	context.Context,
	string,
	string,
	string,
) ([]artifactsmodule.Event, error) {
	return nil, s.err
}
