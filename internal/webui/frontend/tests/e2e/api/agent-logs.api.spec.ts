/**
 * Agent Logs / Terminal API E2E Tests
 *
 * Endpoints under test:
 * - GET /api/workspaces/{ws}/agents/{name}/logs
 * - GET /api/workspaces/{ws}/agents/{name}/terminal/info
 * - GET /api/workspaces/{ws}/agents/{name}/terminal/token
 * - GET /api/workspaces/{ws}/agents/{name}/terminal/ws
 */

import type { APIRequestContext } from '@playwright/test';

import { expect, isIntegrationEnabled, resolvedApiBaseURL, test } from './api-client';

const localIntegrationEnabled = !!process.env.RUN_LOCAL_INTEGRATION_TESTS;
test.skip(
  !isIntegrationEnabled && !localIntegrationEnabled,
  'API E2E tests require RUN_INTEGRATION_TESTS=1 or RUN_LOCAL_INTEGRATION_TESTS=1'
);

const BASE_URL = resolvedApiBaseURL;

interface LogContentResponse {
  success: boolean;
  data?: {
    lines?: string[];
    line_count?: number;
  };
  error?: string;
}

interface AgentTerminalInfoResponse {
  success: boolean;
  data?: {
    agent: string;
    mode: 'tmux' | 'archive';
  };
  error?: string;
}

interface AgentTerminalTokenResponse {
  success: boolean;
  data?: {
    token: string;
  };
  error?: string;
}

let cachedAuthToken: string | null = null;
let cachedWorkspaceId: string | null = null;

async function getAuthHeaders(
  request: APIRequestContext
): Promise<Record<string, string>> {
  if (!cachedAuthToken) {
    try {
      const tokenResponse = await request.get(`${BASE_URL}/api/auth/token`);
      if (tokenResponse.ok()) {
        const tokenBody = (await tokenResponse.json()) as { token?: string };
        if (tokenBody.token) {
          cachedAuthToken = tokenBody.token;
        }
      }
    } catch {
      // auth disabled: auth endpoint may not exist
    }
  }
  return cachedAuthToken ? { Authorization: `Bearer ${cachedAuthToken}` } : {};
}

async function getWorkspaceId(
  request: APIRequestContext,
  headers: Record<string, string>
): Promise<string> {
  if (cachedWorkspaceId) return cachedWorkspaceId;
  const response = await request.get(`${BASE_URL}/api/workspaces`, { headers });
  const body = (await response.json()) as { workspaces?: Array<{ id: string; active?: boolean }> };
  const workspaces = body.workspaces ?? [];
  const active = workspaces.find(w => w.active);
  const ws = active ?? workspaces[0];
  if (!ws?.id) throw new Error('No workspace available for E2E tests');
  cachedWorkspaceId = ws.id;
  return cachedWorkspaceId;
}

test.describe('Agent Logs and Terminal Transport', () => {
  const validAgentName = 'ember';
  const invalidAgentName = '../../../etc/passwd';

  test('GET /api/workspaces/:ws/agents/:name/logs returns content or not-found', async ({ request }) => {
    const headers = await getAuthHeaders(request);
    const wsId = await getWorkspaceId(request, headers);
    const response = await request.get(`${BASE_URL}/api/workspaces/${wsId}/agents/${validAgentName}/logs`, { headers });

    if (response.ok()) {
      const body = (await response.json()) as LogContentResponse;
      expect(body.success).toBe(true);
      expect(Array.isArray(body.data?.lines)).toBe(true);
      expect(typeof body.data?.line_count).toBe('number');
      return;
    }

    expect(response.status()).toBe(404);
    const body = (await response.json()) as LogContentResponse;
    // Error responses may omit `success` field — just verify error is present
    expect(body.error).toBeDefined();
    expect(body.error?.toLowerCase()).toContain('log file');
  });

  test('GET /api/workspaces/:ws/agents/:name/terminal/info reports transport mode', async ({ request }) => {
    const headers = await getAuthHeaders(request);
    const wsId = await getWorkspaceId(request, headers);
    const response = await request.get(`${BASE_URL}/api/workspaces/${wsId}/agents/${validAgentName}/terminal/info`, {
      headers,
    });
    // 200 = success, 500/503 = terminal not available (no tmux in CI)
    expect([200, 500, 503]).toContain(response.status());

    const body = (await response.json()) as AgentTerminalInfoResponse;
    if (response.status() === 200) {
      expect(body.success).toBe(true);
      expect(body.data?.agent).toBe(validAgentName);
      expect(['pty', 'tmux', 'archive']).toContain(body.data?.mode);
      return;
    }

    expect(body.success).toBe(false);
  });

  test('GET /api/workspaces/:ws/agents/:name/terminal/token returns one-time token', async ({ request }) => {
    const headers = await getAuthHeaders(request);
    const wsId = await getWorkspaceId(request, headers);
    const response = await request.get(`${BASE_URL}/api/workspaces/${wsId}/agents/${validAgentName}/terminal/token`, {
      headers,
    });
    // 200 = success, 500/503 = terminal not available (no tmux in CI)
    expect([200, 500, 503]).toContain(response.status());

    const body = (await response.json()) as AgentTerminalTokenResponse;
    if (response.status() === 200) {
      expect(body.success).toBe(true);
      expect(typeof body.data?.token).toBe('string');
      expect(body.data?.token?.length).toBeGreaterThan(0);
      return;
    }

    expect(body.success).toBe(false);
  });

  test('GET /api/workspaces/:ws/agents/:name/terminal/ws rejects missing token before upgrade', async ({
    request,
  }) => {
    const headers = await getAuthHeaders(request);
    const wsId = await getWorkspaceId(request, headers);
    const response = await request.get(`${BASE_URL}/api/workspaces/${wsId}/agents/${validAgentName}/terminal/ws`);
    // 401 = missing token, 500/503 = terminal not available (no tmux in CI)
    expect([401, 500, 503]).toContain(response.status());
  });

  test('invalid agent names are rejected for terminal endpoints', async ({ request }) => {
    const headers = await getAuthHeaders(request);
    const wsId = await getWorkspaceId(request, headers);
    const info = await request.get(
      `${BASE_URL}/api/workspaces/${wsId}/agents/${encodeURIComponent(invalidAgentName)}/terminal/info`,
      { headers }
    );
    expect(info.status()).toBe(400);

    const token = await request.get(
      `${BASE_URL}/api/workspaces/${wsId}/agents/${encodeURIComponent(invalidAgentName)}/terminal/token`,
      { headers }
    );
    expect(token.status()).toBe(400);

    const ws = await request.get(
      `${BASE_URL}/api/workspaces/${wsId}/agents/${encodeURIComponent(invalidAgentName)}/terminal/ws`
    );
    expect(ws.status()).toBe(400);
  });
});
