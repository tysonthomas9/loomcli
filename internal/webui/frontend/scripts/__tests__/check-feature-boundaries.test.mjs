import { describe, expect, it } from "vitest";

import { scanAll, scanFile } from "../check-feature-boundaries.mjs";

describe("frontend feature boundaries", () => {
  it("allows application code to consume a public feature entry", () => {
    expect(
      scanFile(
        "src/router.tsx",
        'import("@/features/observability").then((module) => module.ObservabilityPage);',
      ),
    ).toEqual([]);
  });

  it("rejects application imports of feature internals", () => {
    expect(
      scanFile(
        "src/views/Probe.tsx",
        'import { fetchObservabilityMetrics } from "@/features/observability/api";',
      ),
    ).toEqual([
      expect.objectContaining({
        reason: "import observability through @/features/observability",
      }),
    ]);
  });

  it("rejects dependencies between frontend features", () => {
    expect(
      scanFile(
        "src/features/work-items/Probe.tsx",
        'import { ObservabilityPage } from "@/features/observability";',
      ),
    ).toEqual([
      expect.objectContaining({
        reason: "feature work-items cannot import feature observability",
      }),
    ]);
  });

  it("accepts the checked source tree", () => {
    const result = scanAll(process.cwd());
    expect(result.violations).toEqual([]);
    expect(result.scannedCount).toBeGreaterThan(0);
  });
});
