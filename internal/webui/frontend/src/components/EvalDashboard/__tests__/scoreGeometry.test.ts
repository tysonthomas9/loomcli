import { describe, expect, it } from "vitest";

import { buildScoreTrendGeometry, scoreToBarHeight } from "../scoreGeometry";

describe("scoreGeometry", () => {
  it("clamps score values to bar-height percentages", () => {
    expect(scoreToBarHeight(-4)).toBe(0);
    expect(scoreToBarHeight(42)).toBe(42);
    expect(scoreToBarHeight(140)).toBe(100);
    expect(scoreToBarHeight(Number.NaN)).toBe(0);
  });

  it("maps score buckets into one bar per score dimension", () => {
    const geometry = buildScoreTrendGeometry([
      {
        bucket_start: "2026-07-17T00:00:00Z",
        eval_count: 3,
        averages: {
          outcome_success: 95,
          instruction_adherence: 80,
          efficiency: 65,
          tool_use_quality: 110,
        },
      },
    ]);

    expect(geometry).toHaveLength(1);
    expect(geometry[0]?.evalCount).toBe(3);
    expect(geometry[0]?.bars.map((bar) => bar.key)).toEqual([
      "outcome_success",
      "instruction_adherence",
      "efficiency",
      "tool_use_quality",
    ]);
    expect(geometry[0]?.bars.map((bar) => bar.heightPct)).toEqual([
      95, 80, 65, 100,
    ]);
  });
});
