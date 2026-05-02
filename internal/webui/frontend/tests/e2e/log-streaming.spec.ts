import { expect, test, type Page } from '@playwright/test';

import { setupFleetMocks } from './helpers/fleet';

const mockAgents = [
  {
    name: 'ember',
    status: 'planning: loomcli-5d0d (1m)',
    branch: 'ember',
    task: 'loomcli-5d0d',
    ahead: 0,
    behind: 0,
    last_seen: '2026-02-14T12:00:00Z',
  },
];

const mockStatus = {
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
    ember: {
      id: 'loomcli-5d0d',
      title: 'Show associated commits on task/issue cards',
      priority: 2,
    },
  },
  sync: {
    db_synced: true,
    db_last_sync: '2026-02-14T12:00:00Z',
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 1,
    closed: 5,
    total: 6,
    completion: 83,
    remaining: 1,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  timestamp: '2026-02-14T12:00:00Z',
};

async function setupBaseMocks(page: Page): Promise<void> {
  await setupFleetMocks(page, []);

  await page.route('**/api/monitor/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(mockStatus),
    });
  });
  await page.route('**/localhost:9000/api/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(mockStatus),
    });
  });

  await page.route('**/api/monitor/agents', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: mockAgents }),
    });
  });
  await page.route('**/localhost:9000/api/agents', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: mockAgents }),
    });
  });

  await page.route('**/api/monitor/tasks', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        needs_planning: [],
        ready_to_implement: [],
        in_progress: [],
        needs_review: [],
        blocked: [],
      }),
    });
  });
  await page.route('**/localhost:9000/api/tasks', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        needs_planning: [],
        ready_to_implement: [],
        in_progress: [],
        needs_review: [],
        blocked: [],
      }),
    });
  });
}

async function openEmberLogs(page: Page): Promise<void> {
  await page.goto('/ws/default/monitor');
  const emberAgent = page.getByRole('button', { name: 'Agent: ember' }).first();
  await expect(emberAgent).toBeVisible({ timeout: 10000 });
  await emberAgent.click();

  const panel = page.getByTestId('agent-detail-panel');
  await expect(panel).toHaveAttribute('data-state', 'open');
  await panel.getByRole('tab', { name: 'Logs' }).click();
}

test.describe('Agent Logs Terminal Transport UI', () => {
  test('logs tab exists and opens from agent detail panel', async ({ page }) => {
    await setupBaseMocks(page);
    await openEmberLogs(page);

    const panel = page.getByTestId('agent-detail-panel');
    await expect(panel.getByRole('tab', { name: 'Info' })).toBeVisible();
    await expect(panel.getByRole('tab', { name: 'Logs' })).toBeVisible();
    await expect(panel.getByTestId('log-viewer')).toBeVisible();
  });

  test('archive mode renders with refresh control', async ({ page }) => {
    await setupBaseMocks(page);
    await page.route('**/api/workspaces/default/agents/ember/terminal/info', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { agent: 'ember', mode: 'archive' },
        }),
      });
    });
    await page.route('**/api/workspaces/default/agents/ember/logs**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: ['alpha', 'beta'], line_count: 2 },
        }),
      });
    });

    await openEmberLogs(page);

    const panel = page.getByTestId('agent-detail-panel');
    await expect(panel.getByText('Archive snapshot')).toBeVisible();
    await expect(panel.getByRole('button', { name: 'Refresh' })).toBeVisible();
    await expect(panel.locator('[data-testid="log-viewer"] [data-state="connected"]')).toBeVisible();
  });

  test('archive refresh triggers another archive fetch', async ({ page }) => {
    await setupBaseMocks(page);

    let archiveRequests = 0;
    await page.route('**/api/workspaces/default/agents/ember/terminal/info', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { agent: 'ember', mode: 'archive' },
        }),
      });
    });
    await page.route('**/api/workspaces/default/agents/ember/logs**', async (route) => {
      archiveRequests++;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: [`call-${archiveRequests}`], line_count: archiveRequests },
        }),
      });
    });

    await openEmberLogs(page);
    const refreshButton = page.getByRole('button', { name: 'Refresh' });
    await refreshButton.click();
    await expect.poll(() => archiveRequests).toBeGreaterThanOrEqual(2);
  });

  test('tmux mode renders live badge and token fetch path', async ({ page }) => {
    await setupBaseMocks(page);

    let tokenRequests = 0;
    await page.route('**/api/workspaces/default/agents/ember/terminal/info', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { agent: 'ember', mode: 'tmux' },
        }),
      });
    });
    await page.route('**/api/workspaces/default/agents/ember/terminal/token', async (route) => {
      tokenRequests++;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { token: 'deterministic-token' },
        }),
      });
    });

    await openEmberLogs(page);
    const panel = page.getByTestId('agent-detail-panel');
    await expect(panel.getByText('Live (tmux)')).toBeVisible();
    await expect.poll(() => tokenRequests).toBeGreaterThanOrEqual(1);
  });

  test('archive mode handles null log payload without UI crash', async ({ page }) => {
    await setupBaseMocks(page);
    await page.route('**/api/workspaces/default/agents/ember/terminal/info', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { agent: 'ember', mode: 'archive' },
        }),
      });
    });
    await page.route('**/api/workspaces/default/agents/ember/logs**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: null, line_count: null },
        }),
      });
    });

    await openEmberLogs(page);
    const panel = page.getByTestId('agent-detail-panel');
    await expect(panel.getByText('Archive snapshot')).toBeVisible();
    await expect(panel.getByText('Cannot read properties of null')).not.toBeVisible();
  });
});
