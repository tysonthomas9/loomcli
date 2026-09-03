package domain

import "time"

// SessionEvalScores is dimension-keyed. Legacy fixed-key records used the same
// JSON object representation, so decoding them into this map is compatible.
type SessionEvalScores map[string]int

// SessionEvalScoreRationales is keyed by the corresponding score dimension.
type SessionEvalScoreRationales map[string]string

type SessionEvalImprovementCategories struct {
	Harness []string `json:"harness"`
	Linter  []string `json:"linter"`
	Prompt  []string `json:"prompt"`
	Skill   []string `json:"skill"`
}

type SessionEvalCost struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type SessionEval struct {
	EvalID                string                           `json:"eval_id"`
	SessionID             string                           `json:"session_id"`
	TaskID                string                           `json:"task_id,omitempty"`
	AgentID               string                           `json:"agent_id"`
	WorkspaceKey          string                           `json:"workspace_key"`
	Scores                SessionEvalScores                `json:"scores"`
	ScoreRationales       SessionEvalScoreRationales       `json:"score_rationales"`
	ErrorTaxonomyTags     []string                         `json:"error_taxonomy_tags"`
	ImprovementCategories SessionEvalImprovementCategories `json:"improvement_categories"`
	JudgeSummary          string                           `json:"judge_summary"`
	JudgeModel            string                           `json:"judge_model"`
	JudgePromptVersion    string                           `json:"judge_prompt_version"`
	JudgeSessionID        string                           `json:"judge_session_id,omitempty"`
	EvalCost              SessionEvalCost                  `json:"eval_cost"`
	SessionStartedAt      time.Time                        `json:"session_started_at"`
	SessionEndedAt        time.Time                        `json:"session_ended_at"`
	CreatedAt             time.Time                        `json:"created_at"`
	UpdatedAt             time.Time                        `json:"updated_at"`
}
