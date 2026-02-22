import * as fs from "fs"
import * as path from "path"
import * as os from "os"
import { defineConfig, devices } from "@playwright/test"

const isCI = !!process.env.CI
const isIntegration = !!process.env.RUN_INTEGRATION_TESTS
const isLocalIntegration = !!process.env.RUN_LOCAL_INTEGRATION_TESTS
const isLocalServer = !!process.env.LOOM_LOCAL_SERVER

// Self-contained mode: Playwright starts loom serve on a dedicated port (auth disabled by default).
// Uses port 8090 to avoid conflict with dev server on 8080.
const isSelfContained = isIntegration && !isLocalServer
const selfContainedPort = 8090

/** Resolve API key from env or key file for authenticated test projects. */
function resolveApiKey(): string {
  // Self-contained mode has auth disabled by default, no key needed
  if (isSelfContained) return ""
  if (process.env.LOOM_API_KEY) return process.env.LOOM_API_KEY
  try {
    return fs.readFileSync(path.join(os.homedir(), ".loom", "webui-api-key"), "utf-8").trim()
  } catch {
    return ""
  }
}

const apiKey = resolveApiKey()
const apiBaseURL = isSelfContained
  ? `http://localhost:${selfContainedPort}`
  : process.env.LOOM_BASE_URL || "http://localhost:8080"
const authHeaders: Record<string, string> = apiKey ? { Authorization: `Bearer ${apiKey}` } : {}

/**
 * Determine webServer config:
 * - Self-contained API e2e: start loom serve via script on dedicated port
 * - Local dev API e2e: no webServer, use existing loom serve
 * - Chromium unit tests: start Vite dev server
 */
function resolveWebServer() {
  if (isSelfContained) {
    return {
      command: `E2E_PORT=${selfContainedPort} bash ../../../scripts/start-e2e-server.sh`,
      url: `http://localhost:${selfContainedPort}/health`,
      reuseExistingServer: false,
      timeout: 120_000,
      stdout: "pipe" as const,
    }
  }
  if (isIntegration || isLocalIntegration) {
    // Local server mode or local-integration: user manages server
    return undefined
  }
  // Chromium unit tests: Vite dev server
  return {
    command: "PLAYWRIGHT_TEST=1 npm run dev",
    url: "http://localhost:3000",
    reuseExistingServer: !isCI,
    timeout: 60_000,
  }
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
      maxDiffPixels: 100,
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
    video: "retain-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
      testIgnore: isIntegration ? undefined : "**/*.integration.spec.ts",
    },
    {
      name: "integration",
      testDir: "./tests/e2e/integration",
      testMatch: "**/*.integration.spec.ts",
      use: {
        ...devices["Desktop Chrome"],
        baseURL: apiBaseURL,
        extraHTTPHeaders: authHeaders,
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
    },
    {
      name: "local-integration",
      testDir: "./tests/e2e/integration",
      testMatch: "**/terminal-parity.integration.spec.ts",
      testIgnore: isLocalIntegration ? undefined : "**/terminal-parity.integration.spec.ts",
      use: {
        ...devices["Desktop Chrome"],
        baseURL: apiBaseURL,
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
  ],

  webServer: resolveWebServer(),
})
