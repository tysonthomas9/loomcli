import * as fs from "fs";
import * as path from "path";
import * as os from "os";
import { defineConfig, devices } from "@playwright/test";

const isCI = !!process.env.CI;
const isIntegration = !!process.env.RUN_INTEGRATION_TESTS;
const isLocalIntegration = !!process.env.RUN_LOCAL_INTEGRATION_TESTS;
const isLocalServer = !!process.env.LOOM_LOCAL_SERVER;
const chromiumExecutablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
const useFailureVideo = chromiumExecutablePath ? "off" : "retain-on-failure";

// Self-contained mode: Playwright starts loom serve on a dedicated port (auth disabled by default).
// Uses port 8090 to avoid conflict with dev server on 8080.
const isSelfContained = isIntegration && !isLocalServer;
function resolvePort(name: string, fallback: number): number {
  const raw = process.env[name];
  if (!raw) return fallback;
  const port = Number(raw);
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`${name} must be an integer TCP port`);
  }
  return port;
}

const selfContainedPort = resolvePort("E2E_PORT", 8090);
// Vite preview serves the frontend for integration tests (backed by preview.proxy).
const selfContainedFrontendPort = resolvePort("E2E_FRONTEND_PORT", 3100);

/** Resolve API key from env or key file for authenticated test projects. */
function resolveApiKey(): string {
  // Self-contained mode has auth disabled by default, no key needed
  if (isSelfContained) return "";
  if (process.env.LOOM_API_KEY) return process.env.LOOM_API_KEY;
  try {
    return fs
      .readFileSync(path.join(os.homedir(), ".loom", "webui-api-key"), "utf-8")
      .trim();
  } catch {
    return "";
  }
}

const apiKey = resolveApiKey();
const apiBaseURL = isSelfContained
  ? `http://localhost:${selfContainedPort}`
  : process.env.LOOM_BASE_URL || "http://localhost:8080";
// Integration tests navigate to the frontend via Vite preview (self-contained)
// or the dev server / user-managed frontend (local modes).
const frontendBaseURL = isSelfContained
  ? `http://localhost:${selfContainedFrontendPort}`
  : process.env.LOOM_FRONTEND_BASE_URL ||
    process.env.LOOM_BASE_URL ||
    "http://localhost:3000";
const authHeaders: Record<string, string> = apiKey
  ? { Authorization: `Bearer ${apiKey}` }
  : {};

// Propagate base URL to helpers.ts which reads process.env.LOOM_BASE_URL at module load
if (isSelfContained) {
  process.env.LOOM_BASE_URL = apiBaseURL;
}

// Browser specs that predate CI enforcement and currently fail against the
// shipped UI. The Playwright workflow only ever triggered on `main` while this
// repo's trunk moved to v5, so nothing ran the browser suite for months and it
// decayed — most of these break on selectors the June 2026 Aether restyle
// changed, not on anything recent.
//
// The CI browser job runs everything EXCEPT this list, so the suite gates from
// today and any newly added spec is covered by default. Repairing a spec means
// deleting its line here: the list may only shrink. Run the full suite locally
// with `make test-e2e` to see what is still red.
const QUARANTINED_SPECS = [
  "**/agent-review-journey.spec.ts",
  "**/agents-sidebar.spec.ts",
  "**/app.spec.ts",
  "**/assembled-views.spec.ts",
  "**/backlog-column.spec.ts",
  "**/create-workspace-modal.spec.ts",
  "**/dependencies-section.spec.ts",
  "**/dependency-type-filter.spec.ts",
  "**/filter.spec.ts",
  "**/filter-bar.spec.ts",
  "**/filter-url-sync.spec.ts",
  "**/first-run-onboarding.spec.ts",
  "**/groupby-assignee.spec.ts",
  "**/groupby-dropdown.spec.ts",
  "**/groupby-epic.spec.ts",
  "**/groupby-label.spec.ts",
  "**/groupby-none.spec.ts",
  "**/groupby-priority.spec.ts",
  "**/groupby-type.spec.ts",
  "**/groupby-url-sync.spec.ts",
  "**/issue-detail-panel.spec.ts",
  "**/issue-detail-panel-wiring.spec.ts",
  "**/journey-create-triage.spec.ts",
  "**/journey-monitor-drilldown.spec.ts",
  "**/journey-project-status.spec.ts",
  "**/journey-table-sort-select.spec.ts",
  "**/journey-workspace-lifecycle.spec.ts",
  "**/kanban.spec.ts",
  "**/kanban-column-redesign.spec.ts",
  "**/kanban-redesign.spec.ts",
  "**/kanban-ui-redesign.spec.ts",
  "**/keyboard-escape-layers.spec.ts",
  "**/keyboard-focus-management.spec.ts",
  "**/keyboard-global-shortcuts.spec.ts",
  "**/log-streaming.spec.ts",
  "**/monitor-dashboard.spec.ts",
  "**/monitor-degradation.spec.ts",
  "**/monitor-visual-regression.spec.ts",
  "**/multi-workspace-monitoring.spec.ts",
  "**/priority-dropdown.spec.ts",
  "**/prs-page.spec.ts",
  "**/repo-selector.spec.ts",
  "**/search.spec.ts",
  "**/search-filter-journey.spec.ts",
  "**/server-push.spec.ts",
  "**/session-detail-view.spec.ts",
  "**/session-history.spec.ts",
  "**/session-timeline.spec.ts",
  "**/sessions-tab.spec.ts",
  "**/smoke.spec.ts",
  "**/swimlane-board.spec.ts",
  "**/swimlane-wiring.spec.ts",
  "**/terminal-disconnect-reconnect.spec.ts",
  "**/terminal-keyboard-shortcuts.spec.ts",
  "**/terminal-split-view.spec.ts",
  "**/terminal-tab-management.spec.ts",
  "**/type-dropdown.spec.ts",
  "**/unblock-stuck-task.spec.ts",
  "**/view-switcher.spec.ts",
  "**/visual-regression.spec.ts",
  "**/visual-regression-terminal.spec.ts",
  "**/workspace-switcher.spec.ts",
];

/**
 * Determine webServer config:
 * - Self-contained API e2e: start loom serve via script on dedicated port
 * - Local dev API e2e: no webServer, use existing loom serve
 * - Chromium unit tests: start Vite dev server
 */
function resolveWebServer() {
  if (isSelfContained) {
    return {
      command: `E2E_PORT=${selfContainedPort} E2E_FRONTEND_PORT=${selfContainedFrontendPort} bash ../../../scripts/start-e2e-server.sh`,
      // Wait for the Vite preview server — start-e2e-server.sh only starts it
      // after loom serve is healthy, so this single probe confirms both
      // servers are reachable before tests dispatch.
      url: `http://localhost:${selfContainedFrontendPort}/`,
      reuseExistingServer: false,
      timeout: 120_000,
      stdout: "pipe" as const,
    };
  }
  if (isIntegration || isLocalIntegration) {
    // Local server mode or local-integration: user manages server
    return undefined;
  }
  // Chromium unit tests: Vite dev server
  return {
    command: "PLAYWRIGHT_TEST=1 npm run dev",
    url: "http://localhost:3000",
    reuseExistingServer: !isCI,
    timeout: 60_000,
  };
}

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 3 : 0,
  ...(isCI && { workers: 1 }),
  reporter: isCI ? "github" : "html",
  timeout: 30000,
  expect: {
    timeout: 5000,
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.001,
      threshold: 0.2,
      animations: "disabled",
    },
    toMatchSnapshot: {
      threshold: 0.2,
    },
  },

  snapshotDir: "./tests/e2e/screenshots",
  snapshotPathTemplate: "{snapshotDir}/{testFilePath}/{arg}{ext}",

  use: {
    baseURL: "http://localhost:3000",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: useFailureVideo,
  },

  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
      },
      testIgnore: isIntegration ? undefined : "**/*.integration.spec.ts",
    },
    {
      // What CI gates on: the mocked chromium suite minus the quarantine.
      name: "chromium-ci",
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
      },
      testIgnore: ["**/*.integration.spec.ts", ...QUARANTINED_SPECS],
    },
    {
      name: "integration",
      testDir: "./tests/e2e/integration",
      testMatch: "**/*.integration.spec.ts",
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
        baseURL: frontendBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
      workers: 1,
    },
    {
      name: "integration-smoke",
      testDir: "./tests/e2e/integration",
      testMatch: "**/*.integration.spec.ts",
      grep: /@smoke/,
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
        baseURL: frontendBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
      workers: 1,
    },
    {
      name: "integration-regression",
      testDir: "./tests/e2e/integration",
      testMatch: "**/*.integration.spec.ts",
      grep: /@regression/,
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
        baseURL: frontendBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
      workers: 1,
    },
    {
      name: "local-integration",
      testDir: "./tests/e2e/integration",
      testMatch: "**/terminal-parity.integration.spec.ts",
      testIgnore: isLocalIntegration
        ? undefined
        : "**/terminal-parity.integration.spec.ts",
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutablePath
          ? { launchOptions: { executablePath: chromiumExecutablePath } }
          : {}),
        baseURL: frontendBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      timeout: 60000,
    },
    {
      name: "api",
      testDir: "./tests/e2e/api",
      testMatch: "**/*.api.spec.ts",
      testIgnore: isIntegration ? undefined : "**/*.api.spec.ts",
      use: {
        baseURL: apiBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      timeout: 60000,
      workers: 1,
    },
    {
      name: "api-smoke",
      testDir: "./tests/e2e/api",
      testMatch: "**/*.api.spec.ts",
      testIgnore: isIntegration ? undefined : "**/*.api.spec.ts",
      grep: /@smoke/,
      use: {
        baseURL: apiBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      timeout: 60000,
      workers: 1,
    },
    {
      name: "api-regression",
      testDir: "./tests/e2e/api",
      testMatch: "**/*.api.spec.ts",
      testIgnore: isIntegration ? undefined : "**/*.api.spec.ts",
      grep: /@regression/,
      use: {
        baseURL: apiBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      timeout: 60000,
      workers: 1,
    },
  ],

  webServer: resolveWebServer(),
});
