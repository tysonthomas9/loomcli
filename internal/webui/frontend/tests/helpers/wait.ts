/**
 * Custom wait/assertion helpers for common async patterns in E2E tests.
 * Uses Playwright's built-in expect().toBeVisible() / waitForSelector() with configurable timeouts.
 */

import { expect, type Page } from '@playwright/test';

const DEFAULT_TIMEOUT = 10_000;

/**
 * Wait for loading skeleton to disappear and kanban columns or table to be visible.
 */
export async function waitForAppLoaded(page: Page, timeout = DEFAULT_TIMEOUT): Promise<void> {
  // Wait for the main heading to appear (app has mounted)
  await expect(page.locator('h1')).toBeVisible({ timeout });

  // Wait for loading skeletons to disappear (if present)
  const skeleton = page.locator('[data-testid="loading-skeleton"], .animate-pulse');
  if (await skeleton.isVisible().catch(() => false)) {
    await expect(skeleton.first()).not.toBeVisible({ timeout });
  }

  // Wait for either kanban columns or table rows to be visible
  const kanbanOrTable = page.locator('section[data-status], table, [role="table"]');
  await expect(kanbanOrTable.first()).toBeVisible({ timeout });
}

/**
 * Wait for the connection status indicator to show connected state.
 */
export async function waitForSSEConnected(page: Page, timeout = DEFAULT_TIMEOUT): Promise<void> {
  await expect(page.locator('[data-state="connected"]')).toBeVisible({ timeout });
}

/**
 * Wait for an issue card with the given ID to appear in the DOM.
 */
export async function waitForIssueCard(page: Page, issueId: string, timeout = DEFAULT_TIMEOUT): Promise<void> {
  await expect(page.locator(`[data-issue-id="${issueId}"], article:has-text("${issueId}")`).first()).toBeVisible({
    timeout,
  });
}

/**
 * Wait for a toast notification matching the text pattern.
 */
export async function waitForToast(page: Page, textPattern: string | RegExp, timeout = DEFAULT_TIMEOUT): Promise<void> {
  const pattern = typeof textPattern === 'string' ? new RegExp(textPattern) : textPattern;
  await expect(page.locator('[role="status"], [data-testid="toast"]').filter({ hasText: pattern })).toBeVisible({
    timeout,
  });
}

/**
 * Wait for the issue detail panel slide-in animation to complete.
 */
export async function waitForPanelOpen(page: Page, timeout = DEFAULT_TIMEOUT): Promise<void> {
  await expect(page.locator('[data-testid="detail-panel"], [role="dialog"], aside').first()).toBeVisible({ timeout });
}

/**
 * Wait for the detail panel to be hidden.
 */
export async function waitForPanelClosed(page: Page, timeout = DEFAULT_TIMEOUT): Promise<void> {
  await expect(page.locator('[data-testid="detail-panel"], [role="dialog"], aside').first()).not.toBeVisible({
    timeout,
  });
}

/**
 * Wait for a specific number of issue cards to be visible in a column.
 */
export async function waitForColumnCount(
  page: Page,
  status: string,
  count: number,
  timeout = DEFAULT_TIMEOUT,
): Promise<void> {
  await expect(page.locator(`section[data-status="${status}"] article`)).toHaveCount(count, { timeout });
}
