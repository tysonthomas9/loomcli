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
