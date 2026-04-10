/**
 * SSE stream mock for simulating real-time events in Playwright E2E tests.
 * Uses page.route() to intercept /api/events and stream SSE data back.
 */

import type { Page, Route } from '@playwright/test';
import type { MutationPayload } from '../../src/api/sse';

export interface SSEMockController {
  /** Intercepts GET /api/events and holds the response open as an SSE stream. */
  connect(): Promise<void>;
  /** Pushes a `event: mutation` frame to the connected stream. */
  sendMutation(payload: MutationPayload): void;
  /** Pushes the initial `event: connected` frame. */
  sendConnected(): void;
  /** Closes the SSE stream (simulates server disconnect). */
  close(): void;
  /** Number of SSE connections received (useful for verifying reconnection). */
  connectionCount: number;
}

/**
 * Creates an SSE mock controller for a Playwright page.
 *
 * Usage:
 *   const sse = createSSEMock(page);
 *   await sse.connect();
 *   await page.goto('/');
 *   sse.sendConnected();
 *   sse.sendMutation({ type: 'update', issue_id: 'test-001', timestamp: '...' });
 *   sse.close();
 */
const SSE_PATTERN = '**/workspaces/*/events**';

export function createSSEMock(page: Page): SSEMockController {
  let pendingRoutes: Array<{ route: Route; resolve: () => void }> = [];
  let buffer: string[] = [];
  let connected = false;
  let connectionCount = 0;
  // Store handler ref so close() can unroute to prevent handler accumulation
  let routeHandler: ((route: Route) => Promise<void>) | null = null;

  function flushBuffer(): void {
    if (pendingRoutes.length === 0 || buffer.length === 0) return;

    // Fulfill all pending routes with accumulated events and close them.
    // The browser will reconnect via EventSource, which we handle with the next route.
    const body = buffer.join('');
    buffer = [];

    for (const { route, resolve } of pendingRoutes) {
      route
        .fulfill({
          status: 200,
          contentType: 'text/event-stream',
          headers: {
            'Cache-Control': 'no-cache',
            Connection: 'keep-alive',
          },
          body,
        })
        .then(resolve);
    }
    pendingRoutes = [];
  }

  function pushEvent(event: string, data: string, id?: string): void {
    let frame = '';
    if (id) frame += `id: ${id}\n`;
    frame += `event: ${event}\ndata: ${data}\n\n`;
    buffer.push(frame);
    // Auto-flush if there's a pending route
    flushBuffer();
  }

  const controller: SSEMockController = {
    connectionCount: 0,

    async connect(): Promise<void> {
      // Unroute previous handler if connect() is called again after close()
      if (routeHandler) {
        await page.unroute(SSE_PATTERN, routeHandler);
        routeHandler = null;
      }

      connected = true;
      routeHandler = async (route: Route) => {
        // Let events/token requests fall through to the mockSseToken handler
        if (route.request().url().includes('/events/token')) {
          await route.fallback();
          return;
        }

        connectionCount++;
        controller.connectionCount = connectionCount;

        if (!connected) {
          await route.abort();
          return;
        }

        // Hold the route open until we have data to send
        const routePromise = new Promise<void>((resolve) => {
          pendingRoutes.push({ route, resolve });
        });

        // If there's buffered data, flush immediately
        flushBuffer();

        await routePromise;
      };
      await page.route(SSE_PATTERN, routeHandler);
    },

    sendMutation(payload: MutationPayload): void {
      const id = String(Date.now());
      pushEvent('mutation', JSON.stringify(payload), id);
    },

    sendConnected(): void {
      pushEvent('connected', JSON.stringify({ message: 'connected' }));
    },

    close(): void {
      connected = false;
      // Fulfill any pending routes with empty body to close the stream
      for (const { route, resolve } of pendingRoutes) {
        route
          .fulfill({
            status: 200,
            contentType: 'text/event-stream',
            headers: { 'Cache-Control': 'no-cache' },
            body: '',
          })
          .then(resolve);
      }
      pendingRoutes = [];
      buffer = [];
      // Note: routeHandler is cleaned up in connect() if called again.
      // Page teardown automatically cleans up all routes.
    },
  };

  return controller;
}
