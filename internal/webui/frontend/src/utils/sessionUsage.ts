import type { SessionRecord } from "@/types/agent";

/**
 * Return provider-reported token consumption for a session.
 *
 * Provider input totals already include cached input tokens. Cache read/write
 * fields are useful breakdowns, but adding them again inflates Codex totals.
 */
export function sessionTotalTokens(
  session: Pick<
    SessionRecord,
    | "input_tokens"
    | "output_tokens"
    | "cache_read_tokens"
    | "cache_write_tokens"
  >,
): number {
  return (session.input_tokens ?? 0) + (session.output_tokens ?? 0);
}

/** Compact token count for run summaries: 1234 → "1.2K", 1500000 → "1.5M". */
export function formatTokens(count: number): string {
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}K`;
  return String(count);
}

/** USD cost for run summaries; sub-cent amounts collapse to "<$0.01". */
export function formatCost(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}
