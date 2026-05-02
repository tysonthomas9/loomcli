/**
 * Smoke test for browser test helpers.
 * Verifies the fixture/mock/POM pipeline works end-to-end.
 */

import { test, expect } from '@playwright/test';
import { createIssue, createKanbanData, resetIdCounter } from '../helpers';
import { KanbanPage } from '../pages';
import { setupFleetMocks, workspacePath } from './helpers/fleet';

test.beforeEach(() => {
  resetIdCounter();
});

test.describe('Test helpers smoke test', () => {
  test('fleet helper renders workspace issues', async ({ page }) => {
    await setupFleetMocks(page, [
      createIssue({ id: 'smoke-1', title: 'Smoke Test Issue', status: 'open' }),
    ]);

    await page.goto(workspacePath('/'));
    await page.waitForLoadState('domcontentloaded');

    // Verify the app renders with our mocked data
    await expect(page.getByText('Smoke Test Issue')).toBeVisible({ timeout: 10_000 });
  });

  test('page chrome works on a fleet workspace route', async ({ page }) => {
    await setupFleetMocks(page, []);
    await page.goto(workspacePath('/'));
    await page.waitForLoadState('domcontentloaded');

    await expect(page.locator('h1')).toHaveText('Aether');
    await expect(
      page.getByRole('status', { name: /Connection status/i }),
    ).toBeVisible({ timeout: 10_000 });
  });

  test('kanban board renders with factory data', async ({ page }) => {
    const { issues, blocked } = createKanbanData();

    await setupFleetMocks(page, issues, undefined, { blockedIssues: blocked });

    await page.goto(workspacePath('/'));
    await page.waitForLoadState('domcontentloaded');

    // Use KanbanPage POM to verify board structure
    const kanban = new KanbanPage(page);

    // Wait for columns to appear
    await expect(kanban.getColumn('ready')).toBeVisible({ timeout: 10_000 });
    await expect(kanban.getColumn('in_progress')).toBeVisible();
    await expect(kanban.getColumn('done')).toBeVisible();

    // Verify cards rendered in correct columns
    // ready column should have the 3 open issues
    const readyCount = await kanban.getColumnCount('ready');
    expect(readyCount).toBe(3);
  });

  test('fleet API request tracker records workspace requests', async ({ page }) => {
    const requests: { url: string; method: string }[] = [];
    page.on('request', (request) => {
      const pathname = new URL(request.url()).pathname;
      if (pathname === '/api/workspaces/default/issues') {
        requests.push({ url: request.url(), method: request.method() });
      }
    });

    await setupFleetMocks(page, [createIssue()]);
    await Promise.all([
      page.waitForResponse(
        (res) => new URL(res.url()).pathname === '/api/workspaces/default/issues',
      ),
      page.goto(workspacePath('/')),
    ]);

    expect(requests.length).toBeGreaterThanOrEqual(1);
    expect(requests[0]!.method).toBe('GET');
  });

  test('page exposes connection state', async ({ page }) => {
    await setupFleetMocks(page, []);

    await page.goto(workspacePath('/'));
    await page.waitForLoadState('domcontentloaded');

    await expect(
      page.getByRole('status', { name: /Connection status/i }),
    ).toBeVisible({ timeout: 10_000 });
  });
});
