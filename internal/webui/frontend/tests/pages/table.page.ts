/**
 * TablePage page object for the table view.
 */

import { type Locator, type Page, expect } from '@playwright/test';

export class TablePage {
  readonly page: Page;
  readonly table: Locator;

  constructor(page: Page) {
    this.page = page;
    this.table = page.locator('table, [role="table"]').first();
  }

  /** Returns locator for a table row by issue ID. */
  getRow(issueId: string): Locator {
    return this.table.locator(`tr:has-text("${issueId}"), [role="row"]:has-text("${issueId}")`).first();
  }

  /** Returns all visible data rows (excluding header). */
  getRows(): Locator {
    return this.table.locator('tbody tr, [role="rowgroup"] [role="row"]');
  }

  /** Clicks a row to open the detail panel. */
  async clickRow(issueId: string): Promise<void> {
    await this.getRow(issueId).click();
  }

  /** Toggles checkbox selection for a row. */
  async selectRow(issueId: string): Promise<void> {
    const row = this.getRow(issueId);
    await row.locator('input[type="checkbox"]').click();
  }

  /** Clicks the header checkbox to select all. */
  async selectAll(): Promise<void> {
    await this.table.locator('thead input[type="checkbox"], th input[type="checkbox"]').click();
  }

  /** Clicks a column header to sort by that column. */
  async sortByColumn(columnName: string): Promise<void> {
    await this.table.locator(`th:has-text("${columnName}"), [role="columnheader"]:has-text("${columnName}")`).click();
  }

  /** Returns the bulk action toolbar locator. */
  getBulkToolbar(): Locator {
    return this.page.locator('[data-testid="bulk-toolbar"], [role="toolbar"]');
  }

  /** Wait for a specific number of rows. */
  async expectRowCount(count: number, timeout?: number): Promise<void> {
    await expect(this.getRows()).toHaveCount(count, { timeout });
  }
}
