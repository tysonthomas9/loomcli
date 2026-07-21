package evals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	evalcore "github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/evaladmin"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestHandleGetRollupMathTagsFailuresAndVersions(t *testing.T) {
	st := newEvalTestStore(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	seedSessionEval(t, st, "eval-1", "sess-1", base.Add(34*time.Hour), "v1", domain.SessionEvalScores{
		OutcomeSuccess: 80, InstructionAdherence: 60, Efficiency: 40, ToolUseQuality: 20,
	}, []string{"idle_wait", "verification_skipped"}, domain.SessionEvalImprovementCategories{Harness: []string{"h-third"}})
	seedSessionEval(t, st, "eval-2", "sess-2", base.Add(44*time.Hour), "v1", domain.SessionEvalScores{
		OutcomeSuccess: 100, InstructionAdherence: 80, Efficiency: 60, ToolUseQuality: 40,
	}, []string{"idle_wait"}, domain.SessionEvalImprovementCategories{Harness: []string{"h-second"}})
	seedSessionEval(t, st, "eval-3", "sess-3", base.Add(58*time.Hour), "v2", domain.SessionEvalScores{
		OutcomeSuccess: 60, InstructionAdherence: 40, Efficiency: 20, ToolUseQuality: 0,
	}, []string{"scope_creep"}, domain.SessionEvalImprovementCategories{Harness: []string{"h-newest"}})
	seedFailedStamp(t, st, "failed-1", base.Add(36*time.Hour), "transcript_too_large", domain.AgentSessionKindTask)
	seedFailedStamp(t, st, "failed-2", base.Add(37*time.Hour), "judge_error", domain.AgentSessionKindTask)
	seedFailedStamp(t, st, "failed-3", base.Add(-time.Hour), "outside", domain.AgentSessionKindTask)
	seedFailedStamp(t, st, "failed-4", base.Add(38*time.Hour), "ignored", domain.AgentSessionKindOrchestration)

	rec := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/eval-rollup?since=2026-07-01T00:00:00Z&until=2026-07-04T00:00:00Z", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rollup service.EvalRollupData
	decodeData(t, rec, &rollup)
	if rollup.EvalCount != 3 {
		t.Fatalf("eval_count = %d, want 3", rollup.EvalCount)
	}
	if rollup.ScoreAverages.OutcomeSuccess != 80 || rollup.ScoreAverages.InstructionAdherence != 60 ||
		rollup.ScoreAverages.Efficiency != 40 || rollup.ScoreAverages.ToolUseQuality != 20 {
		t.Fatalf("score averages = %+v", rollup.ScoreAverages)
	}
	if len(rollup.ScoreBuckets) != 2 {
		t.Fatalf("bucket count = %d, buckets = %+v", len(rollup.ScoreBuckets), rollup.ScoreBuckets)
	}
	if got := rollup.ScoreBuckets[0]; got.EvalCount != 2 || got.Averages.OutcomeSuccess != 90 || !got.BucketStart.Equal(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first bucket = %+v", got)
	}
	if rollup.TagFrequencies[0].Tag != "idle_wait" || rollup.TagFrequencies[0].Count != 2 {
		t.Fatalf("tag frequencies = %+v", rollup.TagFrequencies)
	}
	if got := insightTexts(rollup.Insights.Harness); strings.Join(got, ",") != "h-newest,h-second,h-third" {
		t.Fatalf("harness insights = %v", got)
	}
	if len(rollup.FailureClasses) != 2 || rollup.FailureClasses[0].Count != 1 {
		t.Fatalf("failure classes = %+v", rollup.FailureClasses)
	}
	if rollup.JudgePromptVersions[0].Version != "v1" || rollup.JudgePromptVersions[0].Count != 2 {
		t.Fatalf("judge prompt versions = %+v", rollup.JudgePromptVersions)
	}
}

func TestHandleGetRollupHourlyBucketsEmptyWindowInvalidParamsAndInsightCaps(t *testing.T) {
	st := newEvalTestStore(t)
	created := time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC)
	seedSessionEval(t, st, "eval-hour", "sess-hour", created, "v1", domain.SessionEvalScores{
		OutcomeSuccess: 50, InstructionAdherence: 60, Efficiency: 70, ToolUseQuality: 80,
	}, nil, domain.SessionEvalImprovementCategories{Harness: manyInsights(55)})

	rec := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/eval-rollup?since=2026-07-02T00:00:00Z&until=2026-07-03T00:00:00Z", "")
	var rollup service.EvalRollupData
	decodeData(t, rec, &rollup)
	if len(rollup.ScoreBuckets) != 1 || !rollup.ScoreBuckets[0].BucketStart.Equal(time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("hourly buckets = %+v", rollup.ScoreBuckets)
	}
	if len(rollup.Insights.Harness) != 50 {
		t.Fatalf("harness insight cap = %d, want 50", len(rollup.Insights.Harness))
	}

	empty := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/eval-rollup?since=2026-07-05T00:00:00Z&until=2026-07-06T00:00:00Z", "")
	var emptyRollup service.EvalRollupData
	decodeData(t, empty, &emptyRollup)
	if emptyRollup.EvalCount != 0 || len(emptyRollup.ScoreBuckets) != 0 || len(emptyRollup.TagFrequencies) != 0 {
		t.Fatalf("empty rollup = %+v", emptyRollup)
	}

	invalid := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/eval-rollup?since=not-a-date", "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid since") {
		t.Fatalf("invalid status/body = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestHandleGetSessionEvalStatesDoneFailedNone(t *testing.T) {
	st := newEvalTestStore(t)
	seedAgentSession(t, st, "done", domain.AgentSessionCompleted, domain.AgentSessionKindTask, time.Now().UTC(), map[string]string{
		evalcore.MetadataEvalStatus:        evalcore.EvalStatusDone,
		evalcore.MetadataEvalPromptVersion: "v1",
	})
	seedSessionEval(t, st, evalcore.EvalID("done", "v1"), "done", time.Now().UTC(), "v1", domain.SessionEvalScores{}, nil, domain.SessionEvalImprovementCategories{})
	seedAgentSession(t, st, "failed", domain.AgentSessionFailed, domain.AgentSessionKindTask, time.Now().UTC(), map[string]string{
		evalcore.MetadataEvalStatus:        evalcore.EvalStatusFailed,
		evalcore.MetadataEvalPromptVersion: "v1",
		evalcore.MetadataEvalErrorClass:    "transcript_too_large",
		evalcore.MetadataEvalRequested:     "true",
	})
	seedAgentSession(t, st, "none", domain.AgentSessionCompleted, domain.AgentSessionKindTask, time.Now().UTC(), nil)

	done := sessionEvalState(t, st, "done")
	if done.EvalStatus != "done" || done.Eval == nil || done.EvalPromptVersion == nil || *done.EvalPromptVersion != "v1" {
		t.Fatalf("done state = %+v", done)
	}
	failed := sessionEvalState(t, st, "failed")
	if failed.EvalStatus != "failed" || failed.Eval != nil || failed.EvalErrorClass == nil || *failed.EvalErrorClass != "transcript_too_large" || !failed.EvalRequested {
		t.Fatalf("failed state = %+v", failed)
	}
	none := sessionEvalState(t, st, "none")
	if none.EvalStatus != "none" || none.Eval != nil || none.EvalPromptVersion != nil || none.EvalErrorClass != nil {
		t.Fatalf("none state = %+v", none)
	}
}

func TestHandleRejudgeUsesCoreValidationAndReportsBindingEnabled(t *testing.T) {
	st := newEvalTestStore(t)
	seedAgentSession(t, st, "running", domain.AgentSessionRunning, domain.AgentSessionKindTask, time.Now().UTC(), map[string]string{
		evalcore.MetadataTranscriptRef: "artifact://running",
	})
	seedAgentSession(t, st, "done", domain.AgentSessionCompleted, domain.AgentSessionKindTask, time.Now().UTC(), map[string]string{
		evalcore.MetadataTranscriptRef:     "artifact://done",
		evalcore.MetadataEvalStatus:        evalcore.EvalStatusDone,
		evalcore.MetadataEvalPromptVersion: "v1",
	})
	seedSessionEval(t, st, evalcore.EvalID("done", "v1"), "done", time.Now().UTC(), "v1", domain.SessionEvalScores{}, nil, domain.SessionEvalImprovementCategories{})

	invalid := serveEvalRequest(t, st, http.MethodPost, "/api/workspaces/WS/sessions/running/rejudge", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("running rejudge status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	missing := serveEvalRequest(t, st, http.MethodPost, "/api/workspaces/WS/sessions/missing/rejudge", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing rejudge status = %d, body = %s", missing.Code, missing.Body.String())
	}

	rec := serveEvalRequest(t, st, http.MethodPost, "/api/workspaces/WS/sessions/done/rejudge", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rejudge status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result service.EvalRejudgeResult
	decodeData(t, rec, &result)
	if !result.Requested || result.BindingEnabled {
		t.Fatalf("rejudge result = %+v", result)
	}
	if _, err := st.SessionEvals().Get(context.Background(), "WS", evalcore.EvalID("done", "v1")); err == nil {
		t.Fatal("old eval record still exists after rejudge")
	}
	session, err := st.AgentSessions().Get(context.Background(), "WS", "done")
	if err != nil {
		t.Fatal(err)
	}
	if session.Metadata[evalcore.MetadataEvalRequested] != "true" || session.Metadata[evalcore.MetadataEvalStatus] != "" {
		t.Fatalf("rejudge metadata = %+v", session.Metadata)
	}
}

func TestHandleRejudgeMapsServiceConflictTo409(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(conflictEvalService{}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/sessions/sess/rejudge", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEvalCronGetPutEnsureAndFlip(t *testing.T) {
	st := newEvalTestStore(t)
	get := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/evals/cron", "")
	var state service.EvalCronState
	decodeData(t, get, &state)
	if state.Provisioned || state.Enabled || state.Schedule != nil {
		t.Fatalf("initial cron = %+v", state)
	}

	disabledNoop := serveEvalRequest(t, st, http.MethodPut, "/api/workspaces/WS/evals/cron", `{"enabled":false}`)
	decodeData(t, disabledNoop, &state)
	if state.Provisioned || state.Enabled {
		t.Fatalf("disabled unprovisioned cron = %+v", state)
	}

	seedActiveSessionEvalBuiltin(t, st, "version-1")
	enabled := serveEvalRequest(t, st, http.MethodPut, "/api/workspaces/WS/evals/cron", `{"enabled":true}`)
	decodeData(t, enabled, &state)
	if !state.Provisioned || !state.Enabled || state.Schedule == nil || *state.Schedule != evalcore.DefaultEvalCronSchedule {
		t.Fatalf("enabled cron = %+v", state)
	}

	disabled := serveEvalRequest(t, st, http.MethodPut, "/api/workspaces/WS/evals/cron", `{"enabled":false}`)
	decodeData(t, disabled, &state)
	if !state.Provisioned || state.Enabled || state.Schedule == nil || *state.Schedule != evalcore.DefaultEvalCronSchedule {
		t.Fatalf("disabled cron = %+v", state)
	}

	reEnabled := serveEvalRequest(t, st, http.MethodPut, "/api/workspaces/WS/evals/cron", `{"enabled":true}`)
	decodeData(t, reEnabled, &state)
	if !state.Enabled {
		t.Fatalf("re-enabled cron = %+v", state)
	}
	binding, err := st.TriggerBindings().GetByRouteKey(context.Background(), "WS", evalcore.EvalCronRouteKey)
	if err != nil {
		t.Fatal(err)
	}
	if binding.DriverVersionID != "version-1" {
		t.Fatalf("driver version = %q, want version-1", binding.DriverVersionID)
	}
}

func serveEvalRequest(t *testing.T, st *memstore.Store, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewModule(evaladmin.NewEvalAdminService(st)).Register(mux)
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.Success {
		t.Fatalf("success = false, error = %q", env.Error)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

func sessionEvalState(t *testing.T, st *memstore.Store, sessionID string) service.SessionEvalState {
	t.Helper()
	rec := serveEvalRequest(t, st, http.MethodGet, "/api/workspaces/WS/sessions/"+sessionID+"/eval", "")
	var state service.SessionEvalState
	decodeData(t, rec, &state)
	return state
}

func newEvalTestStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return st
}

func seedSessionEval(t *testing.T, st *memstore.Store, evalID, sessionID string, createdAt time.Time, version string, scores domain.SessionEvalScores, tags []string, insights domain.SessionEvalImprovementCategories) {
	t.Helper()
	if _, err := st.SessionEvals().Create(context.Background(), &domain.SessionEval{
		EvalID:                evalID,
		SessionID:             sessionID,
		TaskID:                "TASK-" + sessionID,
		AgentID:               "agent-1",
		WorkspaceKey:          "WS",
		Scores:                scores,
		ErrorTaxonomyTags:     append([]string(nil), tags...),
		ImprovementCategories: insights,
		JudgeSummary:          "summary",
		JudgeModel:            "codex-test",
		JudgePromptVersion:    version,
		CreatedAt:             createdAt.UTC(),
		UpdatedAt:             createdAt.UTC(),
	}); err != nil {
		t.Fatalf("create eval %s: %v", evalID, err)
	}
}

func seedAgentSession(t *testing.T, st *memstore.Store, sessionID string, status domain.AgentSessionStatus, kind domain.AgentSessionKind, startedAt time.Time, metadata map[string]string) {
	t.Helper()
	initialStatus := status
	if status.IsTerminal() {
		initialStatus = domain.AgentSessionRunning
	}
	if _, err := st.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      "agent-1",
		Kind:         kind,
		TaskID:       "TASK-" + sessionID,
		Status:       initialStatus,
		StartedAt:    startedAt.UTC(),
		Metadata:     metadata,
	}); err != nil {
		t.Fatalf("create session %s: %v", sessionID, err)
	}
	if status.IsTerminal() {
		finishedAt := startedAt.UTC().Add(10 * time.Minute)
		finishedAtPtr := &finishedAt
		if _, err := st.AgentSessions().Update(context.Background(), "WS", sessionID, store.AgentSessionUpdate{
			Status:     &status,
			FinishedAt: &finishedAtPtr,
		}); err != nil {
			t.Fatalf("finish session %s: %v", sessionID, err)
		}
	}
}

func seedFailedStamp(t *testing.T, st *memstore.Store, sessionID string, startedAt time.Time, errorClass string, kind domain.AgentSessionKind) {
	t.Helper()
	seedAgentSession(t, st, sessionID, domain.AgentSessionCompleted, kind, startedAt, map[string]string{
		evalcore.MetadataEvalStatus:     evalcore.EvalStatusFailed,
		evalcore.MetadataEvalErrorClass: errorClass,
	})
}

func manyInsights(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "insight"
	}
	return out
}

func insightTexts(in []service.EvalInsight) []string {
	out := make([]string, 0, len(in))
	for _, insight := range in {
		out = append(out, insight.Text)
	}
	return out
}

func seedActiveSessionEvalBuiltin(t *testing.T, st *memstore.Store, versionID string) {
	t.Helper()
	ctx := context.Background()
	workDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", workDir)
	name := workflowdefs.BuiltinSessionEvalAgentWorkflowName
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     name,
		Name:         name,
		Status:       domain.DriverStatusActive,
		TrustLevel:   domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	bundleRel := filepath.ToSlash(filepath.Join(".loom", "workflow-builds", versionID))
	bundleRoot := filepath.Join(workDir, filepath.FromSlash(bundleRel))
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("create bundle root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	spec, ok := workflowdefs.BuiltinWorkflow(name)
	if !ok {
		t.Fatal("session-eval-agent builtin missing")
	}
	runners, err := json.Marshal([]driverpkg.DriverRunnerSpec{{
		Name:       workflowdefs.BuiltinSessionEvalTaskRunnerName,
		Kind:       driverpkg.RunnerKindFlueWorkflow,
		Entrypoint: workflowdefs.BuiltinSessionEvalTaskRunnerName,
	}})
	if err != nil {
		t.Fatalf("marshal runners: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        versionID,
		DriverID:         name,
		Version:          1,
		SourceRef:        "builtin://workflows/" + name + "/versions/" + versionID,
		SourceDigest:     workflowdefs.SourceDigest(spec.Files),
		BundleRef:        bundleRel,
		BundleDigest:     "sha256:" + versionID,
		Runtime:          driverpkg.RuntimeFlueNode,
		Manifest:         map[string]string{"workflow_name": name, "runners": string(runners)},
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        "system",
	}); err != nil {
		t.Fatalf("create driver version %s: %v", versionID, err)
	}
	if _, err := st.Drivers().Update(ctx, "WS", name, store.DriverUpdate{ActiveVersionID: &versionID}); err != nil {
		t.Fatalf("activate version %s: %v", versionID, err)
	}
}

type conflictEvalService struct{}

func (conflictEvalService) GetRollup(context.Context, string, service.EvalRollupOptions) (*service.EvalRollupData, error) {
	return nil, nil
}
func (conflictEvalService) GetSessionEvalState(context.Context, string, string) (*service.SessionEvalState, error) {
	return nil, nil
}
func (conflictEvalService) RejudgeSession(context.Context, string, string) (*service.EvalRejudgeResult, error) {
	return nil, service.ErrConflict("conflict")
}
func (conflictEvalService) GetCron(context.Context, string) (*service.EvalCronState, error) {
	return nil, nil
}
func (conflictEvalService) SetCronEnabled(context.Context, string, bool) (*service.EvalCronState, error) {
	return nil, nil
}
