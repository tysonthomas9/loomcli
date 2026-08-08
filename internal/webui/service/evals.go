package service

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// EvalAdminService defines the webui-facing session evaluation administration
// operations. Handlers own HTTP parsing; implementations own store/evals
// orchestration and return typed service errors.
type EvalAdminService interface {
	GetRollup(ctx context.Context, wsID string, opts EvalRollupOptions) (*EvalRollupData, error)
	GetSessionEvalState(ctx context.Context, wsID, sessionID string) (*SessionEvalState, error)
	RejudgeSession(ctx context.Context, wsID, sessionID string) (*EvalRejudgeResult, error)
	GetCron(ctx context.Context, wsID string) (*EvalCronState, error)
	SetCronEnabled(ctx context.Context, wsID string, enabled bool) (*EvalCronState, error)
}

type EvalRollupOptions struct {
	Since time.Time
	Until time.Time
}

type EvalScoreAverages struct {
	OutcomeSuccess       float64 `json:"outcome_success"`
	InstructionAdherence float64 `json:"instruction_adherence"`
	Efficiency           float64 `json:"efficiency"`
	ToolUseQuality       float64 `json:"tool_use_quality"`
}

type EvalScoreBucket struct {
	BucketStart time.Time         `json:"bucket_start"`
	EvalCount   int               `json:"eval_count"`
	Averages    EvalScoreAverages `json:"averages"`
}

type EvalCountByTag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type EvalInsight struct {
	Text      string    `json:"text"`
	SessionID string    `json:"session_id"`
	EvalID    string    `json:"eval_id"`
	CreatedAt time.Time `json:"created_at"`
}

type EvalInsightCategories struct {
	Harness []EvalInsight `json:"harness"`
	Linter  []EvalInsight `json:"linter"`
	Prompt  []EvalInsight `json:"prompt"`
	Skill   []EvalInsight `json:"skill"`
}

type EvalFailureClassCount struct {
	ErrorClass string `json:"error_class"`
	Count      int    `json:"count"`
}

type EvalJudgePromptVersionCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

type EvalRollupData struct {
	Since               time.Time                     `json:"since"`
	Until               time.Time                     `json:"until"`
	EvalCount           int                           `json:"eval_count"`
	ScoreAverages       EvalScoreAverages             `json:"score_averages"`
	ScoreBuckets        []EvalScoreBucket             `json:"score_buckets"`
	TagFrequencies      []EvalCountByTag              `json:"tag_frequencies"`
	Insights            EvalInsightCategories         `json:"insights"`
	FailureClasses      []EvalFailureClassCount       `json:"failure_classes"`
	JudgePromptVersions []EvalJudgePromptVersionCount `json:"judge_prompt_versions"`
}

type SessionEvalState struct {
	EvalStatus        string              `json:"eval_status"`
	EvalErrorClass    *string             `json:"eval_error_class"`
	EvalPromptVersion *string             `json:"eval_prompt_version"`
	EvalRequested     bool                `json:"eval_requested"`
	Eval              *domain.SessionEval `json:"eval"`
}

type EvalRejudgeResult struct {
	Requested      bool `json:"requested"`
	BindingEnabled bool `json:"binding_enabled"`
}

type EvalCronState struct {
	Provisioned bool    `json:"provisioned"`
	Enabled     bool    `json:"enabled"`
	Schedule    *string `json:"schedule"`
}
