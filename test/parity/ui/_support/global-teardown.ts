/**
 * Playwright globalTeardown: flush coverage + summary after all specs run.
 *
 * If any required route wasn't exercised, throw — suite fails.
 */
import { writeCoverageReport } from "./coverage";

export default async function globalTeardown(): Promise<void> {
    const report = await writeCoverageReport();
    // eslint-disable-next-line no-console
    console.log(
        `[global-teardown] coverage: ${report.covered.length} routes covered; ${report.missing.length} missing`,
    );
    if (report.missing.length > 0) {
        // Per ui-test-plan.md §5: suite fails if any required route was not
        // exercised by at least one test. Operators who knowingly want to
        // waive a route can set PARITY_COVERAGE_WAIVE="route1,route2".
        const waived = (process.env.PARITY_COVERAGE_WAIVE ?? "")
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean);
        const hardMissing = report.missing.filter((r) => !waived.includes(r));
        // eslint-disable-next-line no-console
        console.warn(
            `[global-teardown] MISSING ROUTES: ${report.missing.join(", ")}\n` +
                `  waived: ${waived.join(", ") || "(none)"}\n` +
                `  See artifacts/reports/coverage.json`,
        );
        if (hardMissing.length > 0) {
            throw new Error(
                `coverage gate failed: ${hardMissing.length} required route(s) not exercised: ${hardMissing.join(", ")}`,
            );
        }
    }
}
