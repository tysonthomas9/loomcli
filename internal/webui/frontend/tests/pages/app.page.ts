/**
 * AppPage page object. Encapsulates top-level layout interactions.
 */

import { type Locator, type Page, expect } from '@playwright/test';

export class AppPage {
  readonly page: Page;

  // Navigation rail
  readonly navRail: Locator;
  readonly kanbanNavButton: Locator;
  readonly monitorNavButton: Locator;

  // Search
  readonly searchInput: Locator;

  // Connection status
  readonly connectionStatus: Locator;

  // Sidebar
  readonly sidebar: Locator;

  // Detail panel
  readonly detailPanel: Locator;

  constructor(page: Page) {
    this.page = page;

    this.navRail = page.locator('nav');
    this.kanbanNavButton = page.locator('nav').getByRole('button', { name: /kanban|board/i });
    this.monitorNavButton = page.locator('nav').getByRole('button', { name: /monitor|dashboard/i });

    this.searchInput = page.locator('input[type="search"], input[placeholder*="Search"], [data-testid="search-input"]');

    this.connectionStatus = page.locator('[data-state]');

    this.sidebar = page.locator('aside, [data-testid="sidebar"]');

    this.detailPanel = page.locator('[data-testid="detail-panel"], [role="dialog"], aside').first();
  }

  async switchToKanban(): Promise<void> {
    await this.kanbanNavButton.click();
  }

  async switchToMonitor(): Promise<void> {
    await this.monitorNavButton.click();
  }

  async search(term: string): Promise<void> {
    await this.searchInput.fill(term);
  }

  async clearSearch(): Promise<void> {
    await this.searchInput.clear();
  }

  async getConnectionState(): Promise<string | null> {
    return this.connectionStatus.getAttribute('data-state');
  }

  async isConnected(): Promise<boolean> {
    const state = await this.getConnectionState();
    return state === 'connected';
  }

  async isDetailPanelOpen(): Promise<boolean> {
    return this.detailPanel.isVisible();
  }

  async closeDetailPanel(): Promise<void> {
    // Try clicking close button or pressing Escape
    const closeButton = this.detailPanel.locator('button[aria-label*="Close"], button[aria-label*="close"]');
    if (await closeButton.isVisible().catch(() => false)) {
      await closeButton.click();
    } else {
      await this.page.keyboard.press('Escape');
    }
    await expect(this.detailPanel).not.toBeVisible();
  }

  async getDetailPanelTitle(): Promise<string> {
    return this.detailPanel.locator('h2, h3, [data-testid="panel-title"]').first().innerText();
  }
}
