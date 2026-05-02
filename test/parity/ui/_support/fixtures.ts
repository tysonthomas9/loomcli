/**
 * Fleet-db fixture setup via the existing seed.sh. This wrapper exists so
 * specs don't shell out to docker compose directly — all seeding goes
 * through `ensureSeeded()` which is idempotent and caches the call per
 * suite-run.
 */
import { execSync } from "node:child_process";
import * as path from "node:path";
import { PARITY_URLS } from "../playwright.config";
import { composeRun } from "./compose";

const REPO_ROOT = path.resolve(__dirname, "../../../..");

let seededAt: number | null = null;

/**
 * Ensure fleet-db has the seed fixture loaded. Safe to call
 * repeatedly. Called from beforeAll (after preflight) and again from
 * beforeEach if the previous test was destructive.
 */
export async function ensureSeeded(force = false): Promise<void> {
  if (!force && seededAt !== null && Date.now() - seededAt < 60_000) {
    return;
  }
  try {
    execSync(composeRun(`run --rm parity-seed-fleet`), {
      cwd: REPO_ROOT,
      encoding: "utf-8",
      timeout: 120_000,
      stdio: ["ignore", "pipe", "pipe"],
    });
    seededAt = Date.now();
  } catch (e: any) {
    // Seed failure is NOT a silent fallback — it means the stack is
    // not in a testable state. Surface loud.
    throw new Error(
      `seed.sh failed: ${e?.message ?? e}\n` +
        `Confirm the docker-compose stack is up (see ui-test-plan.md §0).`,
    );
  }
}

/**
 * Fixture facts — the exact content the seed script produces. Tests assert
 * against these rather than hard-coding magic numbers all over.
 */
export const SEED_FIXTURE = {
  expectedIssueCount: 13, // 3 epics + 10 children
  epics: ["Epic Alpha", "Epic Beta", "Epic Gamma"],
  children: [
    "Add login flow",
    "Fix checkout NPE",
    "Refactor auth middleware",
    "Onboarding wizard",
    "Cache invalidation bug",
    "Update README",
    "Session timeout edge",
    "Theme toggle",
    "Flaky test: login_e2e",
    "Clarify rate limit docs",
  ],
  bugCount: 3, // Fix checkout NPE, Cache invalidation bug, Session timeout edge
  featureCount: 3,
  taskCount: 4,
  workspaceReference: PARITY_URLS.workspace,
  workspaceFleet: PARITY_URLS.workspace,
};
