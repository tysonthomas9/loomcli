import { expect, test, type Page } from "@playwright/test";

import {
  capturePaneChecksum,
  cleanupSession,
  cleanupSessionsMatching,
  isTmuxAvailable,
  seedParitySessions,
} from "../../helpers/terminal-seed";
import { resolveWorkspaceId } from "./helpers";

const skipLocalIntegration = !process.env.RUN_LOCAL_INTEGRATION_TESTS;
test.skip(
  skipLocalIntegration,
  "Local integration tests require RUN_LOCAL_INTEGRATION_TESTS=1",
);

test.describe.configure({ mode: "serial" });

const LAYOUT_SETTLE_MS = 400;
const PARITY_AGENT = "parity";

const mockAgents = [
  {
    name: PARITY_AGENT,
    status: "planning: loomcli-5d0d (1m)",
    branch: PARITY_AGENT,
    task: "loomcli-5d0d",
    ahead: 0,
    behind: 0,
    last_seen: "2026-02-14T12:00:00Z",
  },
  {
    name: "nova",
    status: "1 changes",
    branch: "nova",
    task: "",
    ahead: 0,
    behind: 0,
    last_seen: "2026-02-14T12:00:00Z",
  },
];

const mockWorkspaceAgents = mockAgents.map((agent) => ({
  name: agent.name,
  repos: [],
  repo_groups: [],
  cross_repo: true,
  role_name: agent.name === PARITY_AGENT ? "planner" : "coder",
  backend: "codex",
}));

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 1,
    in_progress: 0,
    need_review: 0,
    blocked: 0,
  },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    blocked: [],
  },
  agent_tasks: {
    [PARITY_AGENT]: {
      id: "loomcli-5d0d",
      title: "Show associated commits on task/issue cards",
      priority: 2,
    },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-02-14T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 1,
    closed: 212,
    total: 213,
    completion: 99,
    remaining: 1,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  timestamp: "2026-02-14T12:00:00Z",
};

async function mockLoomEndpoints(page: Page): Promise<void> {
  const backendConfig = {
    backend: "codex",
    source: "test",
    available: ["codex"],
    agents: [],
  };
  const tasksPayload = {
    needs_planning: [],
    ready_to_implement: [],
    in_progress: [],
    needs_review: [],
    blocked: [],
  };

  await page.addInitScript((config) => {
    window.localStorage.setItem(
      "cortex:config:backend",
      JSON.stringify(config),
    );
  }, backendConfig);

  await page.route("**/api/monitor/status**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    });
  });
  await page.route("**/localhost:9000/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    });
  });

  await page.route(
    (url) => /^\/api\/workspaces\/[^/]+\/monitor\/status$/.test(url.pathname),
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockLoomStatus),
      });
    },
  );

  await page.route("**/api/monitor/agents**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: mockAgents }),
    });
  });
  await page.route("**/localhost:9000/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: mockAgents }),
    });
  });

  await page.route("**/api/monitor/tasks**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(tasksPayload),
    });
  });
  await page.route("**/localhost:9000/api/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(tasksPayload),
    });
  });

  await page.route("**/api/backends", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: [
          {
            name: "codex",
            display_name: "Codex",
            available: true,
            installed: true,
            api_key_set: true,
          },
        ],
      }),
    });
  });

  await page.route(
    (url) => /^\/api\/workspaces\/[^/]+\/config\/backend$/.test(url.pathname),
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: backendConfig,
        }),
      });
    },
  );

  await page.route(
    (url) => /^\/api\/workspaces\/[^/]+$/.test(url.pathname),
    async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as {
        success?: boolean;
        data?: { agents?: unknown[] };
      };
      if (body.success && body.data) {
        body.data.agents = mockWorkspaceAgents;
      }
      await route.fulfill({
        status: response.status(),
        headers: {
          ...response.headers(),
          "content-type": "application/json",
        },
        body: JSON.stringify(body),
      });
    },
  );
}

async function forceTerminalViewportSize(
  selector: ReturnType<Page["locator"]>,
): Promise<void> {
  await selector.evaluate((el) => {
    const element = el as HTMLElement;
    element.style.width = "920px";
    element.style.height = "420px";
  });
}

test.describe("Deterministic Terminal Parity", () => {
  test("talk-to-lead and ember logs render the same terminal content", async ({
    page,
  }) => {
    if (!(await isTmuxAvailable())) {
      test.skip();
      return;
    }

    const workspaceId = await resolveWorkspaceId();
    const workspaceShort = workspaceId ? workspaceId.slice(0, 8) : "default";
    const { talkSession, agentSession } = await seedParitySessions(
      "8080",
      PARITY_AGENT,
      workspaceShort,
    );
    try {
      await page.setViewportSize({ width: 1600, height: 1000 });
      await mockLoomEndpoints(page);
      await page.goto("/");

      const talkButton = page.getByTestId("talk-to-lead-button");
      await expect(talkButton).toBeVisible({ timeout: 10000 });
      await talkButton.click();

      const terminalView = page.getByTestId("terminal-view");
      await expect(terminalView).toBeVisible({ timeout: 10000 });
      const talkStatus = terminalView
        .locator('[data-testid^="terminal-tab-status-"]')
        .first();
      await expect(talkStatus).toHaveAttribute("data-status", "connected", {
        timeout: 10000,
      });

      const talkTerminal = terminalView.getByTestId("terminal-wrapper").first();
      await forceTerminalViewportSize(talkTerminal);
      await page.waitForTimeout(LAYOUT_SETTLE_MS);
      await expect(talkTerminal.locator(".xterm")).toHaveCount(1);
      await expect(
        talkTerminal.locator('[class*="transcriptOverlay"]'),
      ).toHaveCount(0);
      await expect(talkTerminal.locator(".xterm")).not.toContainText("?2004");
      const talkShot = await talkTerminal.screenshot({
        animations: "disabled",
      });

      await page.getByRole("button", { name: "Monitor" }).click();

      const emberAgent = page.getByRole("button", {
        name: `Agent: ${PARITY_AGENT}`,
      });
      await expect(emberAgent).toBeVisible({ timeout: 10000 });
      await emberAgent.click();

      const agentPanel = page.getByTestId("agent-detail-panel");
      await expect(agentPanel).toHaveAttribute("data-state", "open");
      await agentPanel.getByRole("tab", { name: "Logs" }).click();
      await expect(agentPanel.getByText("Live (tmux)")).toBeVisible({
        timeout: 10000,
      });

      const emberStatus = agentPanel
        .getByTestId("log-viewer")
        .getByTestId("connection-dot");
      await expect(emberStatus).toHaveAttribute("data-state", "connected", {
        timeout: 10000,
      });

      const emberTerminal = agentPanel
        .getByTestId("embedded-terminal")
        .getByTestId("terminal-wrapper");
      await forceTerminalViewportSize(emberTerminal);
      await page.waitForTimeout(LAYOUT_SETTLE_MS);
      await expect(emberTerminal.locator(".xterm")).toHaveCount(1);
      const emberShot = await emberTerminal.screenshot({
        animations: "disabled",
      });

      const samePixels = emberShot.equals(talkShot);
      await test.info().attach("talk-terminal.png", {
        body: talkShot,
        contentType: "image/png",
      });
      await test.info().attach("ember-terminal.png", {
        body: emberShot,
        contentType: "image/png",
      });
      await test.info().attach("pixel-parity.json", {
        body: JSON.stringify({ samePixels }, null, 2),
        contentType: "application/json",
      });

      const talkChecksum = await capturePaneChecksum(talkSession);
      const emberChecksum = await capturePaneChecksum(agentSession);
      expect(emberChecksum).toBe(talkChecksum);
    } finally {
      await cleanupSession(talkSession);
      await cleanupSession(agentSession);
      await cleanupSessionsMatching(
        new RegExp(
          `^loom-[a-zA-Z0-9_-]+-[a-zA-Z0-9_-]+-${PARITY_AGENT}-[0-9]+$`,
        ),
      );
    }
  });

  test("ember archive mode is stable when live tmux session is absent", async ({
    page,
  }) => {
    if (!(await isTmuxAvailable())) {
      test.skip();
      return;
    }

    await cleanupSessionsMatching(
      new RegExp(`^loom-[a-zA-Z0-9_-]+-[a-zA-Z0-9_-]+-${PARITY_AGENT}-[0-9]+$`),
    );
    await mockLoomEndpoints(page);

    await page.route(
      `**/agents/${PARITY_AGENT}/terminal/info`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { agent: PARITY_AGENT, mode: "archive" },
          }),
        });
      },
    );

    await page.route(`**/api/agents/${PARITY_AGENT}/logs**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { lines: null, line_count: null },
        }),
      });
    });

    await page.goto("/");
    const emberAgent = page.getByRole("button", {
      name: `Agent: ${PARITY_AGENT}`,
    });
    await expect(emberAgent).toBeVisible({ timeout: 10000 });
    await emberAgent.click();

    const agentPanel = page.getByTestId("agent-detail-panel");
    await agentPanel.getByRole("tab", { name: "Logs" }).click();

    await expect(agentPanel.getByText("Archive snapshot")).toBeVisible({
      timeout: 10000,
    });
    await expect(
      agentPanel.getByRole("button", { name: "Refresh" }),
    ).toBeVisible();
    await expect(
      agentPanel.getByText("Cannot read properties of null"),
    ).not.toBeVisible();

    await agentPanel.getByRole("button", { name: "Refresh" }).click();
    await expect(
      agentPanel.getByText("Cannot read properties of null"),
    ).not.toBeVisible();
  });
});
