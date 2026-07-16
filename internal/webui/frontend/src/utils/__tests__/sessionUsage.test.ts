import { describe, expect, it } from "vitest";

import { sessionTotalTokens } from "../sessionUsage";

describe("sessionTotalTokens", () => {
  it("does not add cached input to provider input totals", () => {
    expect(
      sessionTotalTokens({
        input_tokens: 4200,
        output_tokens: 880,
        cache_read_tokens: 2100,
        cache_write_tokens: 0,
      }),
    ).toBe(5080);
  });
});
