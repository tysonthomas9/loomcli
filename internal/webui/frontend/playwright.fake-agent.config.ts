/**
 * The AgentChat browser proof on the real Agent API and the 1.3 fake harness.
 * It starts TestFakeServeForE2E (internal/cli/serve/agentwire) on 127.0.0.1
 * and its own Vite dev server proxying /api to it, on ports of their own, and
 * stops both when the run ends. Run:
 *   npx playwright test -c playwright.fake-agent.config.ts
 * FAKE_AGENT_API_PORT and FAKE_AGENT_UI_PORT override the ports.
 */
import { defineConfig, devices } from "@playwright/test";

const apiPort = Number(process.env.FAKE_AGENT_API_PORT || 18431);
const uiPort = Number(process.env.FAKE_AGENT_UI_PORT || 3431);
process.env.FAKE_AGENT_API = `http://127.0.0.1:${apiPort}`;

export default defineConfig({
  testDir: "./tests/fake-agent",
  workers: 1,
  reporter: "line",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://127.0.0.1:${uiPort}`,
  },
  webServer: [
    {
      command: `cd ../../.. && LOOM_E2E_FAKE_ADDR=127.0.0.1:${apiPort} go test ./internal/cli/serve/agentwire -run '^TestFakeServeForE2E$' -count=1 -timeout 0`,
      url: `http://127.0.0.1:${apiPort}/api/workspaces/w1/v1/presets`,
      reuseExistingServer: false,
      timeout: 300_000,
    },
    {
      command: `env -u PLAYWRIGHT_TEST VITE_API_BASE_URL=http://127.0.0.1:${apiPort} npx vite --host 127.0.0.1 --port ${uiPort} --strictPort`,
      url: `http://127.0.0.1:${uiPort}`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
