/**
 * KanbanPage page object for swim lane board interactions.
 */

import { type Locator, type Page, expect } from '@playwright/test';
import { dragWithPointer } from '../helpers';

/**
 * Kanban column status values.
 * The kanban uses semantic column IDs: ready (open), pending (blocked/deferred),
 * in_progress, review, done (closed).
 */
export type KanbanColumn = 'ready' | 'pending' | 'in_progress' | 'review' | 'done';

export class KanbanPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  /** Returns locator for a status column. */
  getColumn(status: KanbanColumn): Locator {
    return this.page.locator(`section[data-status="${status}"]`);
  }

  /** Returns locator for a specific issue card by ID. */
  getIssueCard(issueId: string): Locator {
    return this.page.locator(`[data-issue-id="${issueId}"], article:has-text("${issueId}")`).first();
  }

  /** Returns all card locators, optionally filtered by column. */
  getIssueCards(status?: KanbanColumn): Locator {
    if (status) {
      return this.getColumn(status).locator('article');
    }
    return this.page.locator('section[data-status] article');
  }

  /** Returns the number of cards in a column. */
  async getColumnCount(status: KanbanColumn): Promise<number> {
    return this.getColumn(status).locator('article').count();
  }

  /** Clicks an issue card to open the detail panel. */
  async clickIssue(issueId: string): Promise<void> {
    await this.getIssueCard(issueId).click();
  }

  /**
   * Drags an issue card from its current column to a target column.
   * Uses the dragWithPointer helper (real CDP pointer events) so @dnd-kit
   * PointerSensor activation fires reliably. See loomcli-7rth3.3.
   */
  async dragIssue(issueId: string, toStatus: KanbanColumn): Promise<void> {
    const card = this.getIssueCard(issueId);
    const targetColumn = this.getColumn(toStatus);
    const dropZone = targetColumn.locator('[data-droppable-id]').first();
    const target = (await dropZone.isVisible().catch(() => false)) ? dropZone : targetColumn;
    await dragWithPointer(this.page, card, target);
  }

  /** Returns the empty state text for a column. */
  async getEmptyColumnMessage(status: KanbanColumn): Promise<string> {
    const column = this.getColumn(status);
    const emptyState = column.locator('.empty-state, [data-testid="empty-column"], p').first();
    return emptyState.innerText();
  }

  /** Wait for a column to have a specific count of cards. */
  async expectColumnCount(status: KanbanColumn, count: number, timeout?: number): Promise<void> {
    await expect(this.getColumn(status).locator('article')).toHaveCount(count, { timeout });
  }

  /** Wait for a specific card to be visible in a column. */
  async expectCardInColumn(issueId: string, status: KanbanColumn, timeout?: number): Promise<void> {
    const card = this.getColumn(status).locator(`[data-issue-id="${issueId}"], article:has-text("${issueId}")`).first();
    await expect(card).toBeVisible({ timeout });
  }
}
