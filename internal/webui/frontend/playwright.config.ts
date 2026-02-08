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
      testIgnore: isIntegration ? undefined : "**/*.api.spec.ts",
      use: {
        baseURL: "http://localhost:8080",
      },
      globalSetup: "./tests/e2e/global-setup.ts",
      globalTeardown: "./tests/e2e/integration/global-teardown.ts",
      timeout: 60000,
    },
  ],

  webServer: isIntegration
    ? undefined
    : {
        command: "npm run dev",
        url: "http://localhost:3000",
        reuseExistingServer: !isCI,
        timeout: 60000,
      },
})
