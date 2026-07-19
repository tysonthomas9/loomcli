import type { EvalScoreBucket, EvalScoreKey } from "@/types";

export const SCORE_DIMENSIONS: Array<{
  key: EvalScoreKey;
  label: string;
  shortLabel: string;
}> = [
  {
    key: "outcome_success",
    label: "Outcome success",
    shortLabel: "Outcome",
  },
  {
    key: "instruction_adherence",
    label: "Instruction adherence",
    shortLabel: "Adherence",
  },
  {
    key: "efficiency",
    label: "Efficiency",
    shortLabel: "Efficiency",
  },
  {
    key: "tool_use_quality",
    label: "Tool use quality",
    shortLabel: "Tool use",
  },
];

export interface ScoreTrendBar {
  key: EvalScoreKey;
  label: string;
  value: number;
  heightPct: number;
}

export interface ScoreTrendBucketGeometry {
  bucketStart: string;
  label: string;
  evalCount: number;
  bars: ScoreTrendBar[];
}

export function scoreToBarHeight(score: number): number {
  if (!Number.isFinite(score)) return 0;
  return Math.max(0, Math.min(100, score));
}

export function formatBucketLabel(bucketStart: string): string {
  const date = new Date(bucketStart);
  if (isNaN(date.getTime())) return "";
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

export function buildScoreTrendGeometry(
  buckets: EvalScoreBucket[],
): ScoreTrendBucketGeometry[] {
  return buckets.map((bucket) => ({
    bucketStart: bucket.bucket_start,
    label: formatBucketLabel(bucket.bucket_start),
    evalCount: bucket.eval_count,
    bars: SCORE_DIMENSIONS.map((dimension) => {
      const value = bucket.averages[dimension.key] ?? 0;
      return {
        key: dimension.key,
        label: dimension.label,
        value,
        heightPct: scoreToBarHeight(value),
      };
    }),
  }));
}
