/**
 * Playwright globalTeardown: flush coverage + summary after all specs run.
 *
 * Warns on missing routes; only fails the suite when
 * FLEETDB_COVERAGE_STRICT=1 is set. The default-warn posture exists because
 * the coverage tracker only sees URLs the BROWSER TABS request — most of
 * the fleetdb-regression specs use host-side fetch() (Node) for assertions, which the
 * tracker can't observe. Strict-mode is reserved for environments that
 * have been wired with full request capture (e.g. via HAR replay).
 */
import { writeCoverageReport } from "./coverage";

export default async function globalTeardown(): Promise<void> {
  const report = await writeCoverageReport();
  // eslint-disable-next-line no-console
  console.log(
    `[global-teardown] coverage: ${report.covered.length} routes covered; ${report.missing.length} missing`,
  );
  if (report.missing.length === 0) return;

  const waived = (process.env.FLEETDB_COVERAGE_WAIVE ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  const hardMissing = report.missing.filter((r) => !waived.includes(r));

  // eslint-disable-next-line no-console
  console.warn(
    `[global-teardown] MISSING ROUTES (browser-tab observation only — host-side fetch()s aren't tracked):\n` +
      `  ${report.missing.join(", ")}\n` +
      `  waived: ${waived.join(", ") || "(none)"}\n` +
      `  See artifacts/reports/coverage.json`,
  );
  if (process.env.FLEETDB_COVERAGE_STRICT === "1" && hardMissing.length > 0) {
    throw new Error(
      `coverage gate failed (strict mode): ${hardMissing.length} required route(s) not exercised: ${hardMissing.join(", ")}`,
    );
  }
}
