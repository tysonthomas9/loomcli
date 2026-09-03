export type EvalScoreKey =
  | "outcome_success"
  | "instruction_adherence"
  | "efficiency"
  | "tool_use_quality";

/** Dimension-keyed scores. New rubrics may add dimensions without a UI release. */
export type EvalScores = Record<string, number>;

export interface EvalScoreAverages {
  outcome_success: number;
  instruction_adherence: number;
  efficiency: number;
  tool_use_quality: number;
}

export interface EvalScoreBucket {
  bucket_start: string;
  eval_count: number;
  averages: EvalScoreAverages;
}

export interface EvalTagFrequency {
  tag: string;
  count: number;
}

export interface EvalInsight {
  text: string;
  session_id: string;
  eval_id: string;
  created_at: string;
}

export interface EvalInsightCategories {
  harness: EvalInsight[];
  linter: EvalInsight[];
  prompt: EvalInsight[];
  skill: EvalInsight[];
}

export interface EvalFailureClassCount {
  error_class: string;
  count: number;
}

export interface EvalJudgePromptVersionCount {
  version: string;
  count: number;
}

export interface EvalRollupData {
  since: string;
  until: string;
  eval_count: number;
  score_averages: EvalScoreAverages;
  score_buckets: EvalScoreBucket[];
  tag_frequencies: EvalTagFrequency[];
  insights: EvalInsightCategories;
  failure_classes: EvalFailureClassCount[];
  judge_prompt_versions: EvalJudgePromptVersionCount[];
}

export type SessionEvalStatus = "done" | "failed" | "none";

export interface SessionEvalScoreRationales {
  outcome_success: string;
  instruction_adherence: string;
  efficiency: string;
  tool_use_quality: string;
}

export interface SessionEvalCost {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface SessionEvalRecord {
  eval_id: string;
  session_id: string;
  task_id?: string;
  agent_id: string;
  workspace_key: string;
  scores: EvalScores;
  score_rationales: SessionEvalScoreRationales;
  error_taxonomy_tags: string[];
  improvement_categories: {
    harness: string[];
    linter: string[];
    prompt: string[];
    skill: string[];
  };
  judge_summary: string;
  judge_model: string;
  judge_prompt_version: string;
  /** Judge AgentSession that produced this exact eval record, when available. */
  judge_session_id?: string;
  eval_cost: SessionEvalCost;
  session_started_at: string;
  session_ended_at: string;
  created_at: string;
  updated_at: string;
}

export interface SessionEvalState {
  eval_status: SessionEvalStatus;
  eval_error_class?: string | null;
  eval_prompt_version?: string | null;
  eval_requested: boolean;
  eval: SessionEvalRecord | null;
}

export interface EvalCronState {
  provisioned: boolean;
  enabled: boolean;
  schedule?: string | null;
}

export interface EvalRejudgeResult {
  requested: boolean;
  binding_enabled: boolean;
}
