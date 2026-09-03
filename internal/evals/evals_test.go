package evals

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestInSampleDeterministicAndBoundaryPercents(t *testing.T) {
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		if InSample(id, 1) != expectedFNVSample(id, 1) {
			t.Fatalf("InSample(%q, 1) = %v, want FNV cohort %v", id, InSample(id, 1), expectedFNVSample(id, 1))
		}
		if !InSample(id, 100) {
			t.Fatalf("InSample(%q, 100) = false, want true", id)
		}
		if InSample(id, 0) {
			t.Fatalf("InSample(%q, 0) = true, want false", id)
		}
		if InSample(id, 1) != InSample(id, 1) {
			t.Fatalf("InSample(%q, 1) is not deterministic", id)
		}
	}
}

func TestListUnevaluatedRequestedLaneExemptionsAndNewestFirst(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:                 "WS",
		Name:                "Workspace",
		EvalSamplingPercent: 1,
		EvalBatchSize:       3,
		EvalLookbackDays:    30,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	withNow(t, now)
	inSampleIDs := sampleIDs(t, 1, true, 3)
	outSampleIDs := sampleIDs(t, 1, false, 2)

	seedEvalSession(t, st, "WS", outSampleIDs[0], now.AddDate(0, 0, -90), map[string]string{
		MetadataTranscriptRef: "artifact://requested",
		MetadataEvalRequested: "true",
	})
	seedEvalSession(t, st, "WS", inSampleIDs[0], now.Add(-1*time.Hour), map[string]string{MetadataTranscriptRef: "artifact://newest"})
	seedEvalSession(t, st, "WS", inSampleIDs[1], now.Add(-2*time.Hour), map[string]string{MetadataTranscriptRef: "artifact://older"})
	seedEvalSession(t, st, "WS", inSampleIDs[2], now.Add(-3*time.Hour), map[string]string{})
	seedEvalSession(t, st, "WS", outSampleIDs[1], now.Add(-30*time.Minute), map[string]string{MetadataTranscriptRef: "artifact://out"})
	seedEvalSession(t, st, "WS", "stamped-current", now.Add(-10*time.Minute), map[string]string{
		MetadataTranscriptRef:     "artifact://stamped",
		MetadataEvalStatus:        EvalStatusDone,
		MetadataEvalPromptVersion: "v1",
	})
	seedEvalSession(t, st, "WS", "old-not-requested", now.AddDate(0, 0, -45), map[string]string{MetadataTranscriptRef: "artifact://old"})

	candidates, policy, err := ListUnevaluated(ctx, st, "WS", "v1")
	if err != nil {
		t.Fatalf("ListUnevaluated: %v", err)
	}
	if policy.SamplingPercent != 1 || policy.BatchSize != 3 || policy.LookbackDays != 30 {
		t.Fatalf("policy = %+v", policy)
	}
	gotIDs := candidateIDs(candidates)
	wantIDs := []string{outSampleIDs[0], inSampleIDs[0], inSampleIDs[1]}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("candidate IDs = %v, want %v", gotIDs, wantIDs)
	}
	if candidates[0].TranscriptRef != "artifact://requested" || candidates[1].DurationS <= 0 {
		t.Fatalf("candidate projection = %+v", candidates)
	}
}

func TestListUnevaluatedBatchClampsToServerCap(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:                 "WS",
		Name:                "Workspace",
		EvalSamplingPercent: 100,
		EvalBatchSize:       500,
		EvalLookbackDays:    30,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	withNow(t, now)
	for i := 0; i < 101; i++ {
		seedEvalSession(t, st, "WS", fmt.Sprintf("sess-%03d", i), now.Add(-time.Duration(i)*time.Minute), map[string]string{MetadataTranscriptRef: "artifact://t"})
	}
	candidates, policy, err := ListUnevaluated(ctx, st, "WS", "v1")
	if err != nil {
		t.Fatalf("ListUnevaluated: %v", err)
	}
	if policy.BatchSize != MaxBatchSize {
		t.Fatalf("policy.BatchSize = %d, want %d", policy.BatchSize, MaxBatchSize)
	}
	if len(candidates) != MaxBatchSize {
		t.Fatalf("len(candidates) = %d, want %d", len(candidates), MaxBatchSize)
	}
	if candidates[0].SessionID != "sess-000" || candidates[99].SessionID != "sess-099" {
		t.Fatalf("candidate order first/last = %s/%s", candidates[0].SessionID, candidates[99].SessionID)
	}
}

func TestValidateEvalPayloadTagsAndExactKeys(t *testing.T) {
	valid := validPayload()
	valid.ErrorTaxonomyTags = []string{"false_success_claim", "other:needs_better_fixture"}
	if err := ValidateEvalPayload(valid, "v1"); err != nil {
		t.Fatalf("ValidateEvalPayload(valid): %v", err)
	}
	invalidTag := validPayload()
	invalidTag.ErrorTaxonomyTags = []string{"other:NotSnake"}
	if err := ValidateEvalPayload(invalidTag, "v1"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid other tag err = %v, want ErrInvalid", err)
	}
	missingScore := validPayload()
	delete(missingScore.Scores, "efficiency")
	if err := ValidateEvalPayload(missingScore, "v1"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing score err = %v, want ErrInvalid", err)
	}
	extraCategory := validPayload()
	extraCategory.ImprovementCategories["docs"] = []string{"Change docs so that evals are clearer"}
	if err := ValidateEvalPayload(extraCategory, "v1"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("extra category err = %v, want ErrInvalid", err)
	}
}

func TestPutMetricDoneCreatesRecordAndClearsRequested(t *testing.T) {
	ctx := context.Background()
	st := evalStoreFixture(t)
	ended := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	seedEvalSession(t, st, "WS", "sess-1", ended, map[string]string{
		MetadataTranscriptRef: "artifact://t",
		MetadataEvalRequested: "true",
	})
	evalID, created, err := PutMetric(ctx, st, "WS", PutMetricParams{
		SessionID:     "sess-1",
		PromptVersion: "v1",
		Status:        EvalStatusDone,
		Eval:          validPayload(),
	})
	if err != nil {
		t.Fatalf("PutMetric done: %v", err)
	}
	if evalID != "eval-sess-1-v1" || !created {
		t.Fatalf("evalID/created = %q/%v", evalID, created)
	}
	record, err := st.SessionEvals().Get(ctx, "WS", evalID)
	if err != nil {
		t.Fatalf("get eval: %v", err)
	}
	if record.SessionID != "sess-1" || record.TaskID != "TASK-sess-1" || record.JudgePromptVersion != "v1" || record.SessionEndedAt != ended {
		t.Fatalf("record = %+v", record)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataEvalStatus] != EvalStatusDone || session.Metadata[MetadataEvalPromptVersion] != "v1" {
		t.Fatalf("metadata after done = %+v", session.Metadata)
	}
	if _, ok := session.Metadata[MetadataEvalRequested]; ok {
		t.Fatalf("eval_requested was not cleared: %+v", session.Metadata)
	}
}

func TestPutMetricStampsProvidedJudgeSessionID(t *testing.T) {
	ctx := context.Background()
	st := evalStoreFixture(t)
	seedEvalSession(t, st, "WS", "sess-judge-link", time.Now().UTC(), map[string]string{MetadataTranscriptRef: "artifact://t"})
	if _, _, err := PutMetric(ctx, st, "WS", PutMetricParams{SessionID: "sess-judge-link", JudgeSessionID: "judge-session-1", PromptVersion: "v1", Status: EvalStatusDone, Eval: validPayload()}); err != nil {
		t.Fatalf("PutMetric: %v", err)
	}
	record, err := st.SessionEvals().Get(ctx, "WS", "eval-sess-judge-link-v1")
	if err != nil || record.JudgeSessionID != "judge-session-1" {
		t.Fatalf("judge linkage = %+v, err=%v", record, err)
	}
}

func TestPutMetricConflictIsIdempotentAndStampsDone(t *testing.T) {
	ctx := context.Background()
	st := evalStoreFixture(t)
	ended := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	seedEvalSession(t, st, "WS", "sess-1", ended, map[string]string{
		MetadataTranscriptRef: "artifact://t",
		MetadataEvalRequested: "true",
	})
	if _, err := st.SessionEvals().Create(ctx, &domain.SessionEval{
		EvalID:             "eval-sess-1-v1",
		SessionID:          "sess-1",
		AgentID:            "agent-1",
		WorkspaceKey:       "WS",
		JudgePromptVersion: "v1",
	}); err != nil {
		t.Fatalf("precreate eval: %v", err)
	}
	evalID, created, err := PutMetric(ctx, st, "WS", PutMetricParams{
		SessionID:     "sess-1",
		PromptVersion: "v1",
		Status:        EvalStatusDone,
		Eval:          validPayload(),
	})
	if err != nil {
		t.Fatalf("PutMetric conflict: %v", err)
	}
	if evalID != "eval-sess-1-v1" || created {
		t.Fatalf("evalID/created = %q/%v, want existing false", evalID, created)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataEvalStatus] != EvalStatusDone || session.Metadata[MetadataEvalRequested] != "" {
		t.Fatalf("metadata after conflict = %+v", session.Metadata)
	}
}

func TestPutMetricFailedStampsNoRecordAndClearsRequested(t *testing.T) {
	ctx := context.Background()
	st := evalStoreFixture(t)
	seedEvalSession(t, st, "WS", "sess-1", time.Now().UTC(), map[string]string{
		MetadataTranscriptRef: "artifact://t",
		MetadataEvalRequested: "true",
	})
	evalID, created, err := PutMetric(ctx, st, "WS", PutMetricParams{
		SessionID:     "sess-1",
		PromptVersion: "v1",
		Status:        EvalStatusFailed,
		ErrorClass:    "judge_error",
	})
	if err != nil {
		t.Fatalf("PutMetric failed: %v", err)
	}
	if evalID != "eval-sess-1-v1" || created {
		t.Fatalf("evalID/created = %q/%v", evalID, created)
	}
	if _, err := st.SessionEvals().Get(ctx, "WS", evalID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get failed eval err = %v, want ErrNotFound", err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataEvalStatus] != EvalStatusFailed || session.Metadata[MetadataEvalErrorClass] != "judge_error" || session.Metadata[MetadataEvalRequested] != "" {
		t.Fatalf("metadata after failed = %+v", session.Metadata)
	}
}

func TestRejudgeValidationAndDeleteClearSetSemantics(t *testing.T) {
	ctx := context.Background()
	st := evalStoreFixture(t)
	ended := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	seedEvalSession(t, st, "WS", "running", ended, map[string]string{MetadataTranscriptRef: "artifact://t"}, func(in *store.AgentSessionCreate) {
		in.Status = domain.AgentSessionRunning
	})
	if err := Rejudge(ctx, st, "WS", "running"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("running Rejudge err = %v, want ErrInvalid", err)
	}
	seedEvalSession(t, st, "WS", "missing-ref", ended, map[string]string{})
	if err := Rejudge(ctx, st, "WS", "missing-ref"); !errors.Is(err, domain.ErrInvalid) || !stringsContains(err.Error(), "loom doctor --fix") {
		t.Fatalf("missing-ref Rejudge err = %v, want doctor ErrInvalid", err)
	}
	seedEvalSession(t, st, "WS", "sess-1", ended, map[string]string{
		MetadataTranscriptRef:     "artifact://t",
		MetadataEvalStatus:        EvalStatusDone,
		MetadataEvalPromptVersion: "v1",
		MetadataEvalErrorClass:    "judge_error",
	})
	if _, err := st.SessionEvals().Create(ctx, &domain.SessionEval{
		EvalID:             "eval-sess-1-v1",
		SessionID:          "sess-1",
		AgentID:            "agent-1",
		WorkspaceKey:       "WS",
		JudgePromptVersion: "v1",
	}); err != nil {
		t.Fatalf("precreate eval: %v", err)
	}
	if err := Rejudge(ctx, st, "WS", "sess-1"); err != nil {
		t.Fatalf("Rejudge: %v", err)
	}
	if _, err := st.SessionEvals().Get(ctx, "WS", "eval-sess-1-v1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("eval after Rejudge err = %v, want ErrNotFound", err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataEvalRequested] != "true" || session.Metadata[MetadataEvalStatus] != "" || session.Metadata[MetadataEvalPromptVersion] != "" || session.Metadata[MetadataEvalErrorClass] != "" {
		t.Fatalf("metadata after Rejudge = %+v", session.Metadata)
	}
}

func evalStoreFixture(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return st
}

func seedEvalSession(t *testing.T, st *memstore.Store, ws, id string, ended time.Time, metadata map[string]string, mutate ...func(*store.AgentSessionCreate)) {
	t.Helper()
	started := ended.Add(-10 * time.Minute)
	create := store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    id,
		AgentID:      "agent-1",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-" + id,
		Status:       domain.AgentSessionCompleted,
		StartedAt:    started,
		Metadata:     metadata,
	}
	for _, fn := range mutate {
		fn(&create)
	}
	terminalStatus := create.Status
	if !terminalStatus.IsTerminal() {
		if _, err := st.AgentSessions().Create(context.Background(), create); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
		return
	}
	create.Status = domain.AgentSessionRunning
	if _, err := st.AgentSessions().Create(context.Background(), create); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
	finishedAt := ended.UTC()
	finishedAtPtr := &finishedAt
	if _, err := st.AgentSessions().Update(context.Background(), ws, id, store.AgentSessionUpdate{
		Status:     &terminalStatus,
		FinishedAt: &finishedAtPtr,
	}); err != nil {
		t.Fatalf("finish session %s: %v", id, err)
	}
}

func validPayload() EvalPayload {
	return EvalPayload{
		Scores: map[string]int{
			"outcome_success":       90,
			"instruction_adherence": 91,
			"efficiency":            92,
			"tool_use_quality":      93,
		},
		ScoreRationales: map[string]string{
			"outcome_success":       "Entry 1 shows the task completed.",
			"instruction_adherence": "Entry 2 shows instructions were followed.",
			"efficiency":            "Entry 3 shows efficient progress.",
			"tool_use_quality":      "Entry 4 shows appropriate tool use.",
		},
		ErrorTaxonomyTags: []string{"verification_skipped"},
		ImprovementCategories: map[string][]string{
			"harness": {"Change harness so that entry 1 is checked."},
			"linter":  {},
			"prompt":  {},
			"skill":   {},
		},
		JudgeSummary: "Strong session with minor verification gap.",
		JudgeModel:   "codex-test",
		EvalCost:     domain.SessionEvalCost{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

func withNow(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
}

func sampleIDs(t *testing.T, percent int, want bool, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for i := 0; len(out) < n && i < 100000; i++ {
		id := fmt.Sprintf("sample-%t-%d", want, i)
		if InSample(id, percent) == want {
			out = append(out, id)
		}
	}
	if len(out) != n {
		t.Fatalf("could not find %d sample IDs want=%v", n, want)
	}
	return out
}

func candidateIDs(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.SessionID)
	}
	return out
}

func expectedFNVSample(id string, percent int) bool {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32()%100) < percent
}

func stringsContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
