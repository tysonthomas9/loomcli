/**
 * Smoke test for browser test helpers.
 * Verifies the fixture/mock/POM pipeline works end-to-end.
 */

import { test, expect } from '../fixtures';
import { createIssue, createStats, createKanbanData, resetIdCounter } from '../helpers';
import { AppPage, KanbanPage } from '../pages';

test.beforeEach(() => {
  resetIdCounter();
});

test.describe('Test helpers smoke test', () => {
  test('fixtures provide mockApi and mockSSE', async ({ page, mockApi, mockSSE }) => {
    // Set up mocks before navigation
    await mockApi.mockAuth();
    await mockApi.mockReady([
      createIssue({ id: 'smoke-1', title: 'Smoke Test Issue', status: 'open' }),
    ]);
    await mockSSE.connect();

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    mockSSE.sendConnected();

    // Verify the app renders with our mocked data
    await expect(page.getByText('Smoke Test Issue')).toBeVisible({ timeout: 10_000 });
  });

  test('appPage fixture provides a fully set up page', async ({ appPage, mockApi }) => {
    // appPage already has auth and SSE set up — just need API data
    // Note: appPage navigates before mockReady is set up, so for data-dependent
    // tests, set up mockApi.mockReady before using appPage fixture
    await expect(appPage.locator('h1')).toHaveText('Cortex');
  });

  test('kanban board renders with factory data', async ({ page, mockApi, mockSSE }) => {
    const { issues, stats } = createKanbanData();

    await mockApi.mockAuth();
    await mockApi.mockReady(issues);
    await mockApi.mockStats(stats);
    await mockSSE.connect();

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    mockSSE.sendConnected();

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

  test('API mock tracker records requests', async ({ page, mockApi, mockSSE }) => {
    await mockApi.mockAuth();
    await mockSSE.connect();

    const tracker = await mockApi.mockReady([createIssue()]);

    // Wait for both navigation and the API response
    await Promise.all([
      page.waitForResponse((res) => res.url().includes('/api/ready')),
      page.goto('/'),
    ]);

    // The app should have called /api/ready at least once
    expect(tracker.calls.length).toBeGreaterThanOrEqual(1);
    expect(tracker.calls[0]!.method).toBe('GET');
  });

  test('AppPage POM provides connection state', async ({ page, mockApi, mockSSE }) => {
    await mockApi.mockAuth();
    await mockApi.mockReady([]);
    await mockSSE.connect();

    await page.goto('/');
    await page.waitForLoadState('domcontentloaded');
    mockSSE.sendConnected();

    const app = new AppPage(page);
    await expect(app.connectionStatus).toBeVisible({ timeout: 10_000 });
  });
});
