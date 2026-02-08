/**
 * MonitorPage page object for the monitor dashboard.
 */

import { type Locator, type Page, expect } from '@playwright/test';

export class MonitorPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  /** Returns locator for an agent card by name. */
  getAgentCard(name: string): Locator {
    return this.page.locator(`[data-testid="agent-card"]:has-text("${name}"), [data-agent="${name}"]`).first();
  }

  /** Returns locator for the project health panel. */
  getHealthPanel(): Locator {
    return this.page.locator('[data-testid="health-panel"], section:has-text("Health")').first();
  }

  /** Returns locator for the blocking dependencies canvas/graph. */
  getBlockingGraph(): Locator {
    return this.page.locator('[data-testid="blocking-graph"], canvas, .react-flow').first();
  }

  /** Clicks an agent card to open agent detail panel. */
  async clickAgent(name: string): Promise<void> {
    await this.getAgentCard(name).click();
  }

  /** Wait for agent cards to be visible. */
  async expectAgentVisible(name: string, timeout?: number): Promise<void> {
    await expect(this.getAgentCard(name)).toBeVisible({ timeout });
  }
}
