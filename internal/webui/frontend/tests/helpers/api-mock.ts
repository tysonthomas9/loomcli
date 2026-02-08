/**
 * API route mock handler factory for Playwright E2E tests.
 * Uses page.route() to intercept fetch requests and return typed JSON responses.
 * Each mock returns a call tracker so tests can assert on request bodies/params.
 */

import type { Page, Request } from '@playwright/test';
import type { Issue, IssueDetails, BlockedIssue, GraphIssue } from '../../src/types/issue';
import type { Statistics } from '../../src/types/statistics';
import { createIssue, createStats, createBlockedIssue } from './test-data';

export interface RequestInfo {
  url: string;
  method: string;
  postData: string | null;
  headers: Record<string, string>;
}

export interface MockTracker {
  calls: RequestInfo[];
}

function trackRequest(request: Request): RequestInfo {
  return {
    url: request.url(),
    method: request.method(),
    postData: request.postData(),
    headers: request.headers(),
  };
}

function apiResponse<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

export interface ApiMockHandler {
  mockReady(issues: Issue[]): Promise<MockTracker>;
  mockStats(stats: Statistics): Promise<MockTracker>;
  mockIssue(id: string, details: IssueDetails): Promise<MockTracker>;
  mockBlocked(issues: BlockedIssue[]): Promise<MockTracker>;
  mockGraph(issues: GraphIssue[]): Promise<MockTracker>;
  mockCreateIssue(response: Issue): Promise<MockTracker>;
  mockUpdateIssue(id: string, response: Issue): Promise<MockTracker>;
  mockCloseIssue(id: string): Promise<MockTracker>;
  mockHealth(status?: 'ok' | 'degraded'): Promise<MockTracker>;
  mockAuth(token?: string): Promise<MockTracker>;
  mockAll(options?: MockAllOptions): Promise<MockAllTrackers>;
}

export interface MockAllOptions {
  issues?: Issue[];
  stats?: Statistics;
  blocked?: BlockedIssue[];
  healthStatus?: 'ok' | 'degraded';
  authToken?: string;
}

export interface MockAllTrackers {
  ready: MockTracker;
  stats: MockTracker;
  blocked: MockTracker;
  health: MockTracker;
  auth: MockTracker;
}

export function createApiMockHandler(page: Page): ApiMockHandler {
  return {
    async mockReady(issues: Issue[]): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/ready', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: apiResponse(issues),
        });
      });
      return tracker;
    },

    async mockStats(stats: Statistics): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/stats', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: apiResponse(stats),
        });
      });
      return tracker;
    },

    async mockIssue(id: string, details: IssueDetails): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route(`**/api/issues/${id}`, async (route) => {
        if (route.request().method() === 'GET') {
          tracker.calls.push(trackRequest(route.request()));
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: apiResponse(details),
          });
        } else {
          await route.fallback();
        }
      });
      return tracker;
    },

    async mockBlocked(issues: BlockedIssue[]): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/blocked', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: apiResponse(issues),
        });
      });
      return tracker;
    },

    async mockGraph(issues: GraphIssue[]): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/issues/graph', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: apiResponse(issues),
        });
      });
      return tracker;
    },

    async mockCreateIssue(response: Issue): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/issues', async (route) => {
        if (route.request().method() === 'POST') {
          tracker.calls.push(trackRequest(route.request()));
          await route.fulfill({
            status: 201,
            contentType: 'application/json',
            body: apiResponse(response),
          });
        } else {
          await route.fallback();
        }
      });
      return tracker;
    },

    async mockUpdateIssue(id: string, response: Issue): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route(`**/api/issues/${id}`, async (route) => {
        if (route.request().method() === 'PATCH') {
          tracker.calls.push(trackRequest(route.request()));
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: apiResponse(response),
          });
        } else {
          await route.fallback();
        }
      });
      return tracker;
    },

    async mockCloseIssue(id: string): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route(`**/api/issues/${id}/close`, async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: apiResponse({ id, status: 'closed' }),
        });
      });
      return tracker;
    },

    async mockHealth(status: 'ok' | 'degraded' = 'ok'): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      const body = JSON.stringify({ status, daemon: status === 'ok' });
      await page.route('**/api/health', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({ status: 200, contentType: 'application/json', body });
      });
      await page.route('**/health', async (route) => {
        if (route.request().url().includes('/api/')) {
          await route.fallback();
          return;
        }
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({ status: 200, contentType: 'application/json', body });
      });
      return tracker;
    },

    async mockAuth(token: string = 'test-token-e2e'): Promise<MockTracker> {
      const tracker: MockTracker = { calls: [] };
      await page.route('**/api/auth/token', async (route) => {
        tracker.calls.push(trackRequest(route.request()));
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ token }),
        });
      });
      return tracker;
    },

    async mockAll(options: MockAllOptions = {}): Promise<MockAllTrackers> {
      const {
        issues = [createIssue(), createIssue({ status: 'in_progress' }), createIssue({ status: 'closed' })],
        stats = createStats(),
        blocked = [createBlockedIssue()],
        healthStatus = 'ok',
        authToken = 'test-token-e2e',
      } = options;

      const [ready, statsTracker, blockedTracker, health, auth] = await Promise.all([
        this.mockReady(issues),
        this.mockStats(stats),
        this.mockBlocked(blocked),
        this.mockHealth(healthStatus),
        this.mockAuth(authToken),
      ]);

      return {
        ready,
        stats: statsTracker,
        blocked: blockedTracker,
        health,
        auth,
      };
    },
  };
}
