import { defineConfig, devices } from "@playwright/test"

const isCI = !!process.env.CI
const isIntegration = !!process.env.RUN_INTEGRATION_TESTS

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 2 : 0,
  ...(isCI && { workers: 1 }),
  reporter: isCI ? "github" : "html",
  timeout: 30000,
  expect: {
    timeout: 5000,
    toHaveScreenshot: {
      maxDiffPixels: 100, // Allow minor anti-aliasing differences
      threshold: 0.2, // Pixel color threshold
      animations: "disabled", // Disable CSS animations for consistency
    },
    toMatchSnapshot: {
      threshold: 0.2,
    },
  },

  // Snapshot configuration - platform-agnostic paths for cross-platform CI
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
      // Exclude integration tests from regular runs
      testIgnore: isIntegration ? undefined : "**/*.integration.spec.ts",
    },
    {
      name: "integration",
      testDir: "./tests/e2e/integration",
      testMatch: "**/*.integration.spec.ts",
      use: {
        ...devices["Desktop Chrome"],
        baseURL: "http://localhost:8080",
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
    },
    {
      name: "api",
      testDir: "./tests/e2e/api",
      testMatch: "**/*.api.spec.ts",
      // Skip API tests unless RUN_INTEGRATION_TESTS is set (shares Podman stack with integration tests)
      testIgnore: isIntegration ? undefined : "**/*.api.spec.ts",
      use: {
        baseURL: "http://localhost:8080",
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
    },
  ],

  // Integration tests use Podman Compose stack, no dev server needed
  webServer: isIntegration
    ? undefined
    : {
        command: "npm run dev",
        url: "http://localhost:3000",
        reuseExistingServer: !isCI,
        timeout: 60000,
      },
})
