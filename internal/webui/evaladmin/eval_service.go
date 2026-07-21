// Package evaladmin implements the eval administration service: rollups,
// per-session eval state, re-judge, and the cron enable/pause surface.
package evaladmin

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	evalcore "github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	evalRollupInsightCap = 50
	evalStatusNone       = "none"
)

var _ service.EvalAdminService = (*evalAdminService)(nil)

type evalAdminService struct {
	store store.Store
}

func NewEvalAdminService(st store.Store) service.EvalAdminService {
	return &evalAdminService{store: st}
}

func (s *evalAdminService) GetRollup(ctx context.Context, wsID string, opts service.EvalRollupOptions) (*service.EvalRollupData, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("eval store not available")
	}
	since := opts.Since.UTC()
	until := opts.Until.UTC()
	if since.IsZero() || until.IsZero() {
		return nil, service.ErrValidation("since and until are required")
	}
	if since.After(until) {
		return nil, service.ErrValidation("since must be before until")
	}

	// V1 rollups consistently use SessionEval.CreatedAt for both filtering
	// and bucket placement. SessionEndedAt would better describe session
	// trend time, but the store filter is created_at-based; mixing predicates
	// would silently drop valid records at the window edges.
	evals, err := s.store.SessionEvals().List(ctx, wsID, store.SessionEvalFilter{
		Since: &since,
		Until: &until,
	})
	if err != nil {
		return nil, mapEvalServiceError("failed to list session evals", err)
	}

	evals = filterSessionEvalsByCreatedAt(evals, since, until)
	sort.SliceStable(evals, func(i, j int) bool {
		if evals[i].CreatedAt.Equal(evals[j].CreatedAt) {
			return evals[i].EvalID < evals[j].EvalID
		}
		return evals[i].CreatedAt.After(evals[j].CreatedAt)
	})

	agg := aggregateEvalRollup(evals, until.Sub(since) <= 48*time.Hour)

	failures, err := s.failureClasses(ctx, wsID, since, until)
	if err != nil {
		return nil, err
	}

	return &service.EvalRollupData{
		Since:               since,
		Until:               until,
		EvalCount:           agg.totals.count,
		ScoreAverages:       agg.totals.averages(),
		ScoreBuckets:        scoreBucketsFromMap(agg.buckets),
		TagFrequencies:      sortedTagCounts(agg.tagCounts),
		Insights:            agg.insights,
		FailureClasses:      failures,
		JudgePromptVersions: sortedVersionCounts(agg.versionCounts),
	}, nil
}

type evalRollupAggregate struct {
	totals        scoreAccumulator
	tagCounts     map[string]int
	versionCounts map[string]int
	buckets       map[time.Time]*scoreAccumulator
	insights      service.EvalInsightCategories
}

func aggregateEvalRollup(evals []*domain.SessionEval, bucketHourly bool) evalRollupAggregate {
	agg := evalRollupAggregate{
		tagCounts:     map[string]int{},
		versionCounts: map[string]int{},
		buckets:       map[time.Time]*scoreAccumulator{},
		insights:      emptyEvalInsights(),
	}
	for _, eval := range evals {
		if eval == nil {
			continue
		}
		agg.totals.add(eval.Scores)
		for _, tag := range eval.ErrorTaxonomyTags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				agg.tagCounts[tag]++
			}
		}
		version := strings.TrimSpace(eval.JudgePromptVersion)
		if version != "" {
			agg.versionCounts[version]++
		}
		bucketStart := evalBucketStart(eval.CreatedAt, bucketHourly)
		acc := agg.buckets[bucketStart]
		if acc == nil {
			acc = &scoreAccumulator{}
			agg.buckets[bucketStart] = acc
		}
		acc.add(eval.Scores)
		appendEvalInsights(&agg.insights, eval)
	}
	return agg
}

func (s *evalAdminService) GetSessionEvalState(ctx context.Context, wsID, sessionID string) (*service.SessionEvalState, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("eval store not available")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, service.ErrValidation("session ID is required")
	}
	session, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
	if err != nil {
		return nil, mapEvalServiceError("session not found", err)
	}
	metadata := session.Metadata
	status := evalStatusNone
	switch strings.TrimSpace(metadata[evalcore.MetadataEvalStatus]) {
	case evalcore.EvalStatusDone:
		status = evalcore.EvalStatusDone
	case evalcore.EvalStatusFailed:
		status = evalcore.EvalStatusFailed
	}
	state := &service.SessionEvalState{
		EvalStatus:    status,
		EvalRequested: strings.EqualFold(strings.TrimSpace(metadata[evalcore.MetadataEvalRequested]), "true"),
	}
	if promptVersion := strings.TrimSpace(metadata[evalcore.MetadataEvalPromptVersion]); promptVersion != "" {
		state.EvalPromptVersion = stringPtr(promptVersion)
	}
	if status == evalcore.EvalStatusFailed {
		if errorClass := strings.TrimSpace(metadata[evalcore.MetadataEvalErrorClass]); errorClass != "" {
			state.EvalErrorClass = stringPtr(errorClass)
		}
	}
	if status == evalcore.EvalStatusDone && state.EvalPromptVersion != nil {
		eval, err := s.store.SessionEvals().Get(ctx, wsID, evalcore.EvalID(sessionID, *state.EvalPromptVersion))
		if err != nil {
			return nil, mapEvalServiceError("session eval record not found", err)
		}
		state.Eval = eval
	}
	return state, nil
}

func (s *evalAdminService) RejudgeSession(ctx context.Context, wsID, sessionID string) (*service.EvalRejudgeResult, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("eval store not available")
	}
	if err := evalcore.Rejudge(ctx, s.store, wsID, sessionID); err != nil {
		return nil, mapEvalServiceError("rejudge request failed", err)
	}
	enabled, err := s.evalCronBindingEnabled(ctx, wsID)
	if err != nil {
		return nil, err
	}
	return &service.EvalRejudgeResult{Requested: true, BindingEnabled: enabled}, nil
}

func (s *evalAdminService) GetCron(ctx context.Context, wsID string) (*service.EvalCronState, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("eval store not available")
	}
	binding, err := s.store.TriggerBindings().GetByRouteKey(ctx, wsID, evalcore.EvalCronRouteKey)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &service.EvalCronState{Provisioned: false, Enabled: false, Schedule: nil}, nil
		}
		return nil, mapEvalServiceError("failed to load eval cron binding", err)
	}
	return evalCronStateFromBinding(binding), nil
}

func (s *evalAdminService) SetCronEnabled(ctx context.Context, wsID string, enabled bool) (*service.EvalCronState, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("eval store not available")
	}
	binding, err := s.store.TriggerBindings().GetByRouteKey(ctx, wsID, evalcore.EvalCronRouteKey)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, mapEvalServiceError("failed to load eval cron binding", err)
		}
		if !enabled {
			return &service.EvalCronState{Provisioned: false, Enabled: false, Schedule: nil}, nil
		}
		if _, err := evalcore.EnsureEvalCron(ctx, s.store, wsID, ""); err != nil {
			return nil, mapEvalServiceError("ensure eval cron binding", err)
		}
		return s.GetCron(ctx, wsID)
	}
	if enabled {
		// Enabling goes through the same idempotent ensure function as
		// `loom evals enable` (LOOMCLI-58): it re-pins DriverVersionID to the
		// active builtin and corrects schedule drift while preserving the
		// operator's Enabled state, which is then set explicitly below.
		// Disabling is a pure pause and must not re-pin.
		if _, err := evalcore.EnsureEvalCron(ctx, s.store, wsID, binding.Schedule); err != nil {
			return nil, mapEvalServiceError("ensure eval cron binding", err)
		}
	}
	updated, err := s.store.TriggerBindings().Update(ctx, wsID, binding.BindingID, store.TriggerBindingUpdate{Enabled: &enabled})
	if err != nil {
		return nil, mapEvalServiceError("update eval cron binding", err)
	}
	return evalCronStateFromBinding(updated), nil
}

func (s *evalAdminService) evalCronBindingEnabled(ctx context.Context, wsID string) (bool, error) {
	binding, err := s.store.TriggerBindings().GetByRouteKey(ctx, wsID, evalcore.EvalCronRouteKey)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, mapEvalServiceError("failed to load eval cron binding", err)
	}
	return binding.Enabled, nil
}

func (s *evalAdminService) failureClasses(ctx context.Context, wsID string, since, until time.Time) ([]service.EvalFailureClassCount, error) {
	counts := map[string]int{}
	for _, status := range []domain.AgentSessionStatus{
		domain.AgentSessionCompleted,
		domain.AgentSessionFailed,
		domain.AgentSessionCancelled,
		domain.AgentSessionExpired,
	} {
		// Dashboard failure-class counts are an operational aid, not billable
		// accounting. AgentSession filtering is started_at-based, so this scan
		// uses started_at as an approximation for terminal task sessions whose
		// eval stamp says the judge failed.
		items, total, err := s.store.AgentSessions().ListPage(ctx, wsID, store.AgentSessionFilter{
			Kind:   domain.AgentSessionKindTask,
			Status: status,
			Since:  &since,
			Until:  &until,
		})
		if err != nil {
			return nil, mapEvalServiceError("failed to list eval failure stamps", err)
		}
		if total > len(items) {
			return nil, service.ErrBadGateway("fleet-db capped eval failure scan without pagination")
		}
		for _, session := range items {
			if session == nil || strings.TrimSpace(session.Metadata[evalcore.MetadataEvalStatus]) != evalcore.EvalStatusFailed {
				continue
			}
			errorClass := strings.TrimSpace(session.Metadata[evalcore.MetadataEvalErrorClass])
			if errorClass == "" {
				errorClass = "unknown"
			}
			counts[errorClass]++
		}
	}
	return sortedFailureCounts(counts), nil
}

type scoreAccumulator struct {
	count                int
	outcomeSuccess       int
	instructionAdherence int
	efficiency           int
	toolUseQuality       int
}

func (a *scoreAccumulator) add(scores domain.SessionEvalScores) {
	a.count++
	a.outcomeSuccess += scores["outcome_success"]
	a.instructionAdherence += scores["instruction_adherence"]
	a.efficiency += scores["efficiency"]
	a.toolUseQuality += scores["tool_use_quality"]
}

func (a scoreAccumulator) averages() service.EvalScoreAverages {
	if a.count == 0 {
		return service.EvalScoreAverages{}
	}
	count := float64(a.count)
	return service.EvalScoreAverages{
		OutcomeSuccess:       float64(a.outcomeSuccess) / count,
		InstructionAdherence: float64(a.instructionAdherence) / count,
		Efficiency:           float64(a.efficiency) / count,
		ToolUseQuality:       float64(a.toolUseQuality) / count,
	}
}

func filterSessionEvalsByCreatedAt(in []*domain.SessionEval, since, until time.Time) []*domain.SessionEval {
	out := make([]*domain.SessionEval, 0, len(in))
	for _, eval := range in {
		if eval == nil {
			continue
		}
		created := eval.CreatedAt.UTC()
		if created.Before(since) || created.After(until) {
			continue
		}
		eval.CreatedAt = created
		out = append(out, eval)
	}
	return out
}

func evalBucketStart(createdAt time.Time, hourly bool) time.Time {
	t := createdAt.UTC()
	if hourly {
		return t.Truncate(time.Hour)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func scoreBucketsFromMap(in map[time.Time]*scoreAccumulator) []service.EvalScoreBucket {
	starts := make([]time.Time, 0, len(in))
	for start := range in {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	out := make([]service.EvalScoreBucket, 0, len(starts))
	for _, start := range starts {
		acc := in[start]
		out = append(out, service.EvalScoreBucket{
			BucketStart: start,
			EvalCount:   acc.count,
			Averages:    acc.averages(),
		})
	}
	return out
}

func sortedTagCounts(counts map[string]int) []service.EvalCountByTag {
	out := make([]service.EvalCountByTag, 0, len(counts))
	for tag, count := range counts {
		out = append(out, service.EvalCountByTag{Tag: tag, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Tag < out[j].Tag
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func sortedFailureCounts(counts map[string]int) []service.EvalFailureClassCount {
	out := make([]service.EvalFailureClassCount, 0, len(counts))
	for errorClass, count := range counts {
		out = append(out, service.EvalFailureClassCount{ErrorClass: errorClass, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].ErrorClass < out[j].ErrorClass
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func sortedVersionCounts(counts map[string]int) []service.EvalJudgePromptVersionCount {
	out := make([]service.EvalJudgePromptVersionCount, 0, len(counts))
	for version, count := range counts {
		out = append(out, service.EvalJudgePromptVersionCount{Version: version, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Version < out[j].Version
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func emptyEvalInsights() service.EvalInsightCategories {
	return service.EvalInsightCategories{
		Harness: []service.EvalInsight{},
		Linter:  []service.EvalInsight{},
		Prompt:  []service.EvalInsight{},
		Skill:   []service.EvalInsight{},
	}
}

func appendEvalInsights(out *service.EvalInsightCategories, eval *domain.SessionEval) {
	if out == nil || eval == nil {
		return
	}
	appendCategory := func(dst *[]service.EvalInsight, texts []string) {
		for _, text := range texts {
			if len(*dst) >= evalRollupInsightCap {
				return
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			*dst = append(*dst, service.EvalInsight{
				Text:      text,
				SessionID: eval.SessionID,
				EvalID:    eval.EvalID,
				CreatedAt: eval.CreatedAt.UTC(),
			})
		}
	}
	appendCategory(&out.Harness, eval.ImprovementCategories.Harness)
	appendCategory(&out.Linter, eval.ImprovementCategories.Linter)
	appendCategory(&out.Prompt, eval.ImprovementCategories.Prompt)
	appendCategory(&out.Skill, eval.ImprovementCategories.Skill)
}

func evalCronStateFromBinding(binding *domain.TriggerBinding) *service.EvalCronState {
	if binding == nil {
		return &service.EvalCronState{Provisioned: false, Enabled: false, Schedule: nil}
	}
	schedule := binding.Schedule
	return &service.EvalCronState{Provisioned: true, Enabled: binding.Enabled, Schedule: &schedule}
}

func stringPtr(value string) *string {
	return &value
}

func mapEvalServiceError(message string, err error) *service.ServiceError {
	switch {
	case errors.Is(err, store.ErrServerCapability):
		return service.ErrBadGateway("fleet-db must be upgraded for eval administration")
	case errors.Is(err, domain.ErrNotFound):
		return service.ErrNotFound(message)
	case errors.Is(err, domain.ErrInvalid):
		return service.ErrValidation(err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed), errors.Is(err, domain.ErrInvalidTransition):
		return service.ErrConflict(err.Error())
	default:
		return service.ErrInternal(message, err)
	}
}
