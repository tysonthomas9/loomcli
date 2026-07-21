package evals

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	DefaultSamplingPercent = 100
	DefaultBatchSize       = 25
	DefaultLookbackDays    = 30
	MaxBatchSize           = 100

	MetadataTranscriptRef      = "transcript_ref"
	MetadataEvalStatus         = "eval_status"
	MetadataEvalPromptVersion  = "eval_prompt_version"
	MetadataEvalErrorClass     = "eval_error_class"
	MetadataEvalRequested      = "eval_requested"
	EvalStatusDone             = "done"
	EvalStatusFailed           = "failed"
	ErrorTranscriptFetchFailed = "transcript_fetch_failed"
)

var nowFunc = func() time.Time { return time.Now().UTC() }

type Policy struct {
	SamplingPercent int `json:"sampling_percent"`
	BatchSize       int `json:"batch_size"`
	LookbackDays    int `json:"lookback_days"`
}

func EffectivePolicy(ws *domain.Workspace) Policy {
	p := Policy{
		SamplingPercent: DefaultSamplingPercent,
		BatchSize:       DefaultBatchSize,
		LookbackDays:    DefaultLookbackDays,
	}
	if ws != nil {
		if ws.EvalSamplingPercent > 0 {
			p.SamplingPercent = ws.EvalSamplingPercent
		}
		if ws.EvalBatchSize > 0 {
			p.BatchSize = ws.EvalBatchSize
		}
		if ws.EvalLookbackDays > 0 {
			p.LookbackDays = ws.EvalLookbackDays
		}
	}
	if p.SamplingPercent < 1 {
		p.SamplingPercent = DefaultSamplingPercent
	}
	if p.SamplingPercent > 100 {
		p.SamplingPercent = 100
	}
	if p.BatchSize < 1 {
		p.BatchSize = DefaultBatchSize
	}
	if p.BatchSize > MaxBatchSize {
		p.BatchSize = MaxBatchSize
	}
	if p.LookbackDays < 1 {
		p.LookbackDays = DefaultLookbackDays
	}
	return p
}

// InSample uses a deterministic FNV-1a cohort so the same session remains in
// or out across prompt versions. A per-tick random roll would keep retrying
// unevaluated sessions until coverage converged to 100%.
func InSample(sessionID string, percent int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	return int(h.Sum32()%100) < percent
}

type CandidateTokenUsage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`
}

type CandidateDiffStats struct {
	FilesChanged int      `json:"files_changed"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	FilesTouched []string `json:"files_touched,omitempty"`
	DiffPath     string   `json:"diff_path,omitempty"`
}

type Candidate struct {
	SessionID       string                    `json:"session_id"`
	AgentID         string                    `json:"agent_id"`
	Kind            domain.AgentSessionKind   `json:"kind"`
	TaskID          string                    `json:"task_id,omitempty"`
	Status          domain.AgentSessionStatus `json:"status"`
	ExitCode        *int                      `json:"exit_code,omitempty"`
	ErrorClass      string                    `json:"error_class,omitempty"`
	StartedAt       time.Time                 `json:"started_at"`
	EndedAt         time.Time                 `json:"ended_at"`
	DurationS       float64                   `json:"duration_s,omitempty"`
	ParentSessionID string                    `json:"parent_session_id,omitempty"`
	Attempt         int                       `json:"attempt,omitempty"`
	TokenUsage      CandidateTokenUsage       `json:"token_usage"`
	DiffStats       CandidateDiffStats        `json:"diff_stats"`
	TranscriptRef   string                    `json:"transcript_ref"`
}

func ListUnevaluated(ctx context.Context, st store.Store, ws string, promptVersion string) ([]Candidate, Policy, error) {
	if st == nil {
		return nil, Policy{}, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	promptVersion = strings.TrimSpace(promptVersion)
	if promptVersion == "" {
		return nil, Policy{}, fmt.Errorf("promptVersion required: %w", domain.ErrInvalid)
	}
	workspace, err := st.Workspaces().Get(ctx, ws)
	if err != nil {
		return nil, Policy{}, fmt.Errorf("load workspace eval policy: %w", err)
	}
	policy := EffectivePolicy(workspace)

	sessionsOut, err := listTerminalTaskSessions(ctx, st, ws)
	if err != nil {
		return nil, policy, err
	}

	cutoff := nowFunc().AddDate(0, 0, -policy.LookbackDays)
	requested := make([]Candidate, 0)
	regular := make([]Candidate, 0)
	for _, sess := range sessionsOut {
		candidate, isRequested, ok := candidateFromSession(sess, promptVersion, policy, cutoff)
		if !ok {
			continue
		}
		if isRequested {
			requested = append(requested, candidate)
			continue
		}
		regular = append(regular, candidate)
	}
	sort.SliceStable(requested, func(i, j int) bool { return candidateNewer(requested[i], requested[j]) })
	sort.SliceStable(regular, func(i, j int) bool { return candidateNewer(regular[i], regular[j]) })

	out := make([]Candidate, 0, len(requested)+len(regular))
	out = append(out, requested...)
	out = append(out, regular...)
	if len(out) > policy.BatchSize {
		out = out[:policy.BatchSize]
	}
	return out, policy, nil
}

// listTerminalTaskSessions fetches the full terminal kind=task session set.
// fleet-db applies since/until to started_at, but the eval policy is defined
// over session_ended_at, so the lookback window is cut in Go by the caller —
// a server-side page cap here would silently starve the newest-first join of
// long-started/recently-ended sessions (fleet-db truncates only when
// limit > 0).
func listTerminalTaskSessions(ctx context.Context, st store.Store, ws string) ([]*domain.AgentSession, error) {
	var out []*domain.AgentSession
	for _, status := range []domain.AgentSessionStatus{
		domain.AgentSessionCompleted,
		domain.AgentSessionFailed,
		domain.AgentSessionCancelled,
		domain.AgentSessionExpired,
	} {
		items, _, err := st.AgentSessions().ListPage(ctx, ws, store.AgentSessionFilter{
			Kind:   domain.AgentSessionKindTask,
			Status: status,
		})
		if err != nil {
			return nil, fmt.Errorf("list terminal task sessions: %w", err)
		}
		out = append(out, items...)
	}
	return out, nil
}

func candidateFromSession(sess *domain.AgentSession, promptVersion string, policy Policy, cutoff time.Time) (Candidate, bool, bool) {
	if sess == nil || sess.Kind != domain.AgentSessionKindTask || !sess.Status.IsTerminal() || sess.FinishedAt == nil {
		return Candidate{}, false, false
	}
	metadata := sess.Metadata
	transcriptRef := strings.TrimSpace(metadata[MetadataTranscriptRef])
	if transcriptRef == "" {
		return Candidate{}, false, false
	}
	if strings.TrimSpace(metadata[MetadataEvalStatus]) != "" && strings.TrimSpace(metadata[MetadataEvalPromptVersion]) == promptVersion {
		return Candidate{}, false, false
	}
	requested := strings.EqualFold(strings.TrimSpace(metadata[MetadataEvalRequested]), "true")
	endedAt := sess.FinishedAt.UTC()
	if !requested {
		if endedAt.Before(cutoff) {
			return Candidate{}, false, false
		}
		if !InSample(sess.SessionID, policy.SamplingPercent) {
			return Candidate{}, false, false
		}
	}
	return candidateProjection(sess, transcriptRef), requested, true
}

func candidateProjection(sess *domain.AgentSession, transcriptRef string) Candidate {
	startedAt := sess.StartedAt
	if startedAt.IsZero() {
		startedAt = sess.CreatedAt
	}
	endedAt := time.Time{}
	if sess.FinishedAt != nil {
		endedAt = sess.FinishedAt.UTC()
	}
	diff := sessions.DecodeDiffStatsMetadata(sess.Metadata)
	return Candidate{
		SessionID:       sess.SessionID,
		AgentID:         sess.AgentID,
		Kind:            sess.Kind,
		TaskID:          sess.TaskID,
		Status:          sess.Status,
		ExitCode:        clonePtr(sess.ExitCode),
		ErrorClass:      sess.ErrorClass,
		StartedAt:       startedAt.UTC(),
		EndedAt:         endedAt,
		DurationS:       endedAt.Sub(startedAt).Seconds(),
		ParentSessionID: sess.ParentSessionID,
		Attempt:         sess.Attempt,
		TokenUsage: CandidateTokenUsage{
			InputTokens:      metadataInt64(sess.Metadata, "input_tokens"),
			OutputTokens:     metadataInt64(sess.Metadata, "output_tokens"),
			CacheReadTokens:  metadataInt64(sess.Metadata, "cache_read_tokens"),
			CacheWriteTokens: metadataInt64(sess.Metadata, "cache_write_tokens"),
			EstimatedCostUSD: metadataFloat64(sess.Metadata, firstNonEmpty("estimated_cost_usd", "cost_usd", sess.Metadata)),
		},
		DiffStats: CandidateDiffStats{
			FilesChanged: diff.FilesChanged,
			LinesAdded:   diff.LinesAdded,
			LinesRemoved: diff.LinesRemoved,
			FilesTouched: append([]string(nil), diff.FilesTouched...),
			DiffPath:     diff.DiffPath,
		},
		TranscriptRef: transcriptRef,
	}
}

func candidateNewer(a, b Candidate) bool {
	if a.EndedAt.Equal(b.EndedAt) {
		return a.SessionID < b.SessionID
	}
	return a.EndedAt.After(b.EndedAt)
}

type EvalPayload struct {
	Scores                map[string]int         `json:"scores"`
	ScoreRationales       map[string]string      `json:"score_rationales"`
	ErrorTaxonomyTags     []string               `json:"error_taxonomy_tags"`
	ImprovementCategories map[string][]string    `json:"improvement_categories"`
	JudgeSummary          string                 `json:"judge_summary"`
	JudgeModel            string                 `json:"judge_model"`
	EvalCost              domain.SessionEvalCost `json:"eval_cost"`
}

type PutMetricParams struct {
	SessionID      string
	JudgeSessionID string
	PromptVersion  string
	Status         string
	ErrorClass     string
	Eval           EvalPayload
}

func ValidateEvalPayload(payload EvalPayload, promptVersion string) error {
	if strings.TrimSpace(promptVersion) == "" {
		return fmt.Errorf("judge_prompt_version is required: %w", domain.ErrInvalid)
	}
	if err := validateScores(payload.Scores); err != nil {
		return err
	}
	if err := validateRationales(payload.ScoreRationales); err != nil {
		return err
	}
	for _, tag := range payload.ErrorTaxonomyTags {
		if !validErrorTag(tag) {
			return fmt.Errorf("error_taxonomy_tags contains invalid tag %q: %w", tag, domain.ErrInvalid)
		}
	}
	if err := validateImprovementCategories(payload.ImprovementCategories); err != nil {
		return err
	}
	if strings.TrimSpace(payload.JudgeSummary) == "" {
		return fmt.Errorf("judge_summary is required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(payload.JudgeModel) == "" {
		return fmt.Errorf("judge_model is required: %w", domain.ErrInvalid)
	}
	return nil
}

func PutMetric(ctx context.Context, st store.Store, ws string, params PutMetricParams) (string, bool, error) {
	if st == nil {
		return "", false, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	sessionID := strings.TrimSpace(params.SessionID)
	promptVersion := strings.TrimSpace(params.PromptVersion)
	if sessionID == "" || promptVersion == "" {
		return "", false, fmt.Errorf("sessionId and promptVersion required: %w", domain.ErrInvalid)
	}
	session, err := st.AgentSessions().Get(ctx, ws, sessionID)
	if err != nil {
		return "", false, fmt.Errorf("get session for eval metric: %w", err)
	}
	evalID := EvalID(sessionID, promptVersion)
	switch strings.TrimSpace(params.Status) {
	case EvalStatusDone:
		if err := ValidateEvalPayload(params.Eval, promptVersion); err != nil {
			return "", false, err
		}
		record := buildSessionEval(ws, session, evalID, promptVersion, params.Eval)
		created := true
		if _, err := st.SessionEvals().Create(ctx, record); err != nil {
			if !isConflict(err) {
				return "", false, fmt.Errorf("create session eval: %w", err)
			}
			created = false
		}
		if err := stampEvalMetadata(ctx, st, ws, session, EvalStatusDone, promptVersion, ""); err != nil {
			return "", false, err
		}
		return evalID, created, nil
	case EvalStatusFailed:
		errorClass := strings.TrimSpace(params.ErrorClass)
		if errorClass == "" {
			return "", false, fmt.Errorf("errorClass required when eval status is failed: %w", domain.ErrInvalid)
		}
		if err := stampEvalMetadata(ctx, st, ws, session, EvalStatusFailed, promptVersion, errorClass); err != nil {
			return "", false, err
		}
		return evalID, false, nil
	default:
		return "", false, fmt.Errorf("eval status must be done or failed: %w", domain.ErrInvalid)
	}
}

func Rejudge(ctx context.Context, st store.Store, ws, sessionID string) error {
	if st == nil {
		return fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("sessionId required: %w", domain.ErrInvalid)
	}
	session, err := st.AgentSessions().Get(ctx, ws, sessionID)
	if err != nil {
		return fmt.Errorf("get session for rejudge: %w", err)
	}
	if session.Kind != domain.AgentSessionKindTask || !session.Status.IsTerminal() {
		return fmt.Errorf("not an eval candidate: session %q is kind=%s status=%s: %w", sessionID, session.Kind, session.Status, domain.ErrInvalid)
	}
	if strings.TrimSpace(session.Metadata[MetadataTranscriptRef]) == "" {
		return fmt.Errorf("not an eval candidate: session %q has no transcript_ref; run loom doctor --fix to repair transcript coverage: %w", sessionID, domain.ErrInvalid)
	}
	if stampedVersion := strings.TrimSpace(session.Metadata[MetadataEvalPromptVersion]); stampedVersion != "" {
		err := st.SessionEvals().Delete(ctx, ws, EvalID(sessionID, stampedVersion))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete existing session eval: %w", err)
		}
	}
	metadata := cloneMetadata(session.Metadata)
	delete(metadata, MetadataEvalStatus)
	delete(metadata, MetadataEvalPromptVersion)
	delete(metadata, MetadataEvalErrorClass)
	metadata[MetadataEvalRequested] = "true"
	if _, err := st.AgentSessions().Update(ctx, ws, sessionID, store.AgentSessionUpdate{Metadata: &metadata}); err != nil {
		return fmt.Errorf("mark session for rejudge: %w", err)
	}
	return nil
}

func EvalID(sessionID, promptVersion string) string {
	return "eval-" + strings.TrimSpace(sessionID) + "-" + strings.TrimSpace(promptVersion)
}

func buildSessionEval(ws string, session *domain.AgentSession, evalID, promptVersion string, payload EvalPayload) *domain.SessionEval {
	endedAt := time.Time{}
	if session.FinishedAt != nil {
		endedAt = session.FinishedAt.UTC()
	}
	return &domain.SessionEval{
		EvalID:                evalID,
		SessionID:             session.SessionID,
		TaskID:                session.TaskID,
		AgentID:               session.AgentID,
		WorkspaceKey:          ws,
		Scores:                toDomainScores(payload.Scores),
		ScoreRationales:       toDomainRationales(payload.ScoreRationales),
		ErrorTaxonomyTags:     append([]string(nil), payload.ErrorTaxonomyTags...),
		ImprovementCategories: toDomainImprovementCategories(payload.ImprovementCategories),
		JudgeSummary:          strings.TrimSpace(payload.JudgeSummary),
		JudgeModel:            strings.TrimSpace(payload.JudgeModel),
		JudgePromptVersion:    promptVersion,
		EvalCost:              payload.EvalCost,
		SessionStartedAt:      session.StartedAt.UTC(),
		SessionEndedAt:        endedAt,
	}
}

// stampEvalMetadata read-modify-writes the session's whole Metadata map
// (AgentSessionUpdate.Metadata replaces, not merges), so a concurrent metadata
// writer between the read and the Update would be clobbered. Acceptable for
// terminal sessions, which are effectively single-writer at this point in
// their lifecycle.
func stampEvalMetadata(ctx context.Context, st store.Store, ws string, session *domain.AgentSession, status, promptVersion, errorClass string) error {
	metadata := cloneMetadata(session.Metadata)
	metadata[MetadataEvalStatus] = status
	metadata[MetadataEvalPromptVersion] = promptVersion
	if status == EvalStatusFailed {
		metadata[MetadataEvalErrorClass] = errorClass
	} else {
		delete(metadata, MetadataEvalErrorClass)
	}
	delete(metadata, MetadataEvalRequested)
	if _, err := st.AgentSessions().Update(ctx, ws, session.SessionID, store.AgentSessionUpdate{Metadata: &metadata}); err != nil {
		return fmt.Errorf("stamp session eval metadata: %w", err)
	}
	return nil
}

func isConflict(err error) bool {
	return errors.Is(err, domain.ErrConflict)
}

var scoreKeys = []string{"outcome_success", "instruction_adherence", "efficiency", "tool_use_quality"}

func validateScores(scores map[string]int) error {
	if err := requireExactKeys("scores", scores, scoreKeys); err != nil {
		return err
	}
	for _, key := range scoreKeys {
		score := scores[key]
		if score < 0 || score > 100 {
			return fmt.Errorf("scores.%s must be between 0 and 100: %w", key, domain.ErrInvalid)
		}
	}
	return nil
}

func validateRationales(rationales map[string]string) error {
	if err := requireExactKeys("score_rationales", rationales, scoreKeys); err != nil {
		return err
	}
	for _, key := range scoreKeys {
		if strings.TrimSpace(rationales[key]) == "" {
			return fmt.Errorf("score_rationales.%s is required: %w", key, domain.ErrInvalid)
		}
	}
	return nil
}

var improvementKeys = []string{"harness", "linter", "prompt", "skill"}

func validateImprovementCategories(categories map[string][]string) error {
	if err := requireExactKeys("improvement_categories", categories, improvementKeys); err != nil {
		return err
	}
	for _, key := range improvementKeys {
		items := categories[key]
		if len(items) > 3 {
			return fmt.Errorf("improvement_categories.%s must contain at most 3 items: %w", key, domain.ErrInvalid)
		}
		for _, item := range items {
			if strings.TrimSpace(item) == "" || strings.ContainsAny(item, "\r\n") {
				return fmt.Errorf("improvement_categories.%s items must be non-empty one-line strings: %w", key, domain.ErrInvalid)
			}
		}
	}
	return nil
}

func requireExactKeys[T any](field string, got map[string]T, want []string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s must contain exactly %s: %w", field, strings.Join(want, ", "), domain.ErrInvalid)
	}
	allowed := map[string]struct{}{}
	for _, key := range want {
		allowed[key] = struct{}{}
		if _, ok := got[key]; !ok {
			return fmt.Errorf("%s.%s is required: %w", field, key, domain.ErrInvalid)
		}
	}
	for key := range got {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s.%s is not allowed: %w", field, key, domain.ErrInvalid)
		}
	}
	return nil
}

var otherTagRE = regexp.MustCompile(`^other:[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

var allowedTags = map[string]struct{}{
	"false_success_claim":       {},
	"incomplete_task":           {},
	"instruction_violation":     {},
	"idle_wait":                 {},
	"redundant_work":            {},
	"tool_misuse":               {},
	"hallucinated_state":        {},
	"scope_creep":               {},
	"env_or_dependency_failure": {},
	"killed_or_truncated":       {},
	"unsafe_operation":          {},
	"verification_skipped":      {},
}

func validErrorTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	if _, ok := allowedTags[tag]; ok {
		return true
	}
	return otherTagRE.MatchString(tag)
}

func toDomainScores(scores map[string]int) domain.SessionEvalScores {
	return domain.SessionEvalScores{
		OutcomeSuccess:       scores["outcome_success"],
		InstructionAdherence: scores["instruction_adherence"],
		Efficiency:           scores["efficiency"],
		ToolUseQuality:       scores["tool_use_quality"],
	}
}

func toDomainRationales(rationales map[string]string) domain.SessionEvalScoreRationales {
	return domain.SessionEvalScoreRationales{
		OutcomeSuccess:       strings.TrimSpace(rationales["outcome_success"]),
		InstructionAdherence: strings.TrimSpace(rationales["instruction_adherence"]),
		Efficiency:           strings.TrimSpace(rationales["efficiency"]),
		ToolUseQuality:       strings.TrimSpace(rationales["tool_use_quality"]),
	}
}

func toDomainImprovementCategories(categories map[string][]string) domain.SessionEvalImprovementCategories {
	return domain.SessionEvalImprovementCategories{
		Harness: cloneTrimmedStrings(categories["harness"]),
		Linter:  cloneTrimmedStrings(categories["linter"]),
		Prompt:  cloneTrimmedStrings(categories["prompt"]),
		Skill:   cloneTrimmedStrings(categories["skill"]),
	}
}

func cloneTrimmedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, strings.TrimSpace(item))
	}
	return out
}

func cloneMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func metadataInt64(metadata map[string]string, key string) int64 {
	if metadata == nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(metadata[key]), 10, 64)
	return n
}

func metadataFloat64(metadata map[string]string, key string) float64 {
	if metadata == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(strings.TrimSpace(metadata[key]), 64)
	return n
}

func firstNonEmpty(primary, fallback string, metadata map[string]string) string {
	if metadata == nil {
		return primary
	}
	if strings.TrimSpace(metadata[primary]) != "" {
		return primary
	}
	return fallback
}
