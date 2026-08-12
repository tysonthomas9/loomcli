import { describe, expect, it } from "vitest";

import { formatCost, formatTokens, sessionTotalTokens } from "../sessionUsage";

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

describe("formatTokens", () => {
  it("keeps small counts exact", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(999)).toBe("999");
  });

  it("abbreviates thousands and millions", () => {
    expect(formatTokens(1_200)).toBe("1.2K");
    expect(formatTokens(999_999)).toBe("1000.0K");
    expect(formatTokens(1_500_000)).toBe("1.5M");
  });
});

describe("formatCost", () => {
  it("renders zero and sub-cent costs distinctly", () => {
    expect(formatCost(0)).toBe("$0.00");
    expect(formatCost(0.004)).toBe("<$0.01");
  });

  it("renders cents", () => {
    expect(formatCost(1.239)).toBe("$1.24");
  });
});
