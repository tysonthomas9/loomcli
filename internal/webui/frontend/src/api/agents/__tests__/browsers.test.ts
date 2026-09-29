/**
 * @vitest-environment jsdom
 */

/**
 * Browser inventory client: dedicated fetch wrapper, operator-session
 * manager, and native-bridge handling. fetch and the Tauri invoke bridge are
 * stubbed directly; /api/config is module-mocked.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAuthToken, setAuthToken } from "@/api/common/client";

import {
  __resetBrowserOperatorSessionsForTest,
  BrowserApiError,
  listAgentBrowsers,
  onBrowserOperatorSessionLost,
  refreshBrowserOperatorSessions,
  revokeBrowserOperatorSessions,
  selectAgentBrowser,
} from "../browsers";

const config = vi.hoisted(() => ({ mode: "open" as "open" | "oidc" }));

vi.mock("@/api/common/appConfig", () => ({
  AUTH_MODE_OPEN: "open",
  AUTH_MODE_OIDC: "oidc",
  fetchAppConfig: () => Promise.resolve({ mode: config.mode }),
}));

const BROWSER = {
  id: "11111111-1111-4111-8111-111111111111",
  workspace_key: "E2E",
  owner_agent_id: "lead",
  created_by: "lead",
  name: "Research",
  desired_state: "running",
  status: "starting",
  request_id: "req-1",
  selected: false,
  created_at: "2026-09-23T10:00:00Z",
  updated_at: "2026-09-23T10:00:00Z",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

let fetchMock: ReturnType<typeof vi.fn>;
let invokeMock: ReturnType<typeof vi.fn>;
let tokenCounter = 0;

function installBridge() {
  invokeMock = vi.fn(
    async (_cmd: string, args: { action: string; token?: string | null }) => {
      if (args.action === "issue") {
        tokenCounter += 1;
        return { ok: true, token: `secret-token-${tokenCounter}` };
      }
      return { ok: true };
    },
  );
  window.__TAURI_INTERNALS__ = { invoke: invokeMock as never };
}

function headerOf(call: unknown[], name: string): string | null {
  const init = call[1] as RequestInit;
  return new Headers(init.headers).get(name);
}

function assertTokenNotPersisted(token: string) {
  for (const storage of [window.localStorage, window.sessionStorage]) {
    for (let i = 0; i < storage.length; i += 1) {
      const key = storage.key(i) ?? "";
      expect(key).not.toContain(token);
      expect(storage.getItem(key) ?? "").not.toContain(token);
    }
  }
  expect(document.cookie).not.toContain(token);
  expect(window.location.href).not.toContain(token);
  for (const call of fetchMock.mock.calls) {
    expect(String(call[0])).not.toContain(token);
  }
}

beforeEach(() => {
  config.mode = "open";
  tokenCounter = 0;
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  delete window.__TAURI_INTERNALS__;
  delete window.__TAURI__;
  __resetBrowserOperatorSessionsForTest();
  setAuthToken(null);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete window.__TAURI_INTERNALS__;
  __resetBrowserOperatorSessionsForTest();
});

describe("local desktop mode", () => {
  it("sends the native-issued bearer only in the operator header", async () => {
    installBridge();
    const logSpies = (["log", "info", "warn", "error", "debug"] as const).map(
      (level) => vi.spyOn(console, level).mockImplementation(() => {}),
    );
    fetchMock.mockResolvedValue(jsonResponse({ browsers: [BROWSER] }));

    const browsers = await listAgentBrowsers("E2E", "lead");

    expect(browsers).toEqual([BROWSER]);
    expect(invokeMock).toHaveBeenCalledWith("browser_operator_session", {
      action: "issue",
      workspace: "E2E",
      token: null,
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0]!;
    expect(call[0]).toBe("/api/workspaces/E2E/agents/lead/browsers");
    expect(headerOf(call, "X-Loom-Operator-Session")).toBe("secret-token-1");
    expect(headerOf(call, "Authorization")).toBeNull();
    assertTokenNotPersisted("secret-token-1");
    for (const spy of logSpies) {
      for (const args of spy.mock.calls) {
        expect(JSON.stringify(args)).not.toContain("secret-token");
      }
    }
  });

  it("reuses the in-memory bearer across calls", async () => {
    installBridge();
    fetchMock.mockImplementation(async () =>
      jsonResponse({ browsers: [BROWSER] }),
    );
    await listAgentBrowsers("E2E", "lead");
    await listAgentBrowsers("E2E", "lead");
    expect(
      invokeMock.mock.calls.filter((c) => c[1].action === "issue"),
    ).toHaveLength(1);
  });

  it("requires Loom Desktop without a bridge and makes no request", async () => {
    await expect(listAgentBrowsers("E2E", "lead")).rejects.toMatchObject({
      status: 401,
      code: "browser_desktop_required",
      message: "Open this workspace in Loom Desktop to view agent browsers",
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("drops the bearer on 401, re-issues once via IPC, and retries", async () => {
    installBridge();
    setAuthToken("app-user-token");
    const lost = vi.fn();
    onBrowserOperatorSessionLost(lost);
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ browsers: [] }))
      .mockResolvedValueOnce(
        jsonResponse(
          { error: "expired", code: "browser_operator_session_expired" },
          401,
        ),
      )
      .mockResolvedValueOnce(jsonResponse({ browsers: [BROWSER] }));

    await listAgentBrowsers("E2E", "lead");
    const browsers = await listAgentBrowsers("E2E", "lead");

    expect(browsers).toEqual([BROWSER]);
    const issues = invokeMock.mock.calls.filter((c) => c[1].action === "issue");
    expect(issues).toHaveLength(2);
    expect(headerOf(fetchMock.mock.calls[1]!, "X-Loom-Operator-Session")).toBe(
      "secret-token-1",
    );
    expect(headerOf(fetchMock.mock.calls[2]!, "X-Loom-Operator-Session")).toBe(
      "secret-token-2",
    );
    expect(lost).toHaveBeenCalledWith("E2E");
    // A browser 401 is never a global sign-out.
    expect(getAuthToken()).toBe("app-user-token");
  });

  it("gives up after one re-issue when the retry is also rejected", async () => {
    installBridge();
    fetchMock.mockImplementation(async () =>
      jsonResponse(
        { error: "required", code: "browser_operator_session_required" },
        401,
      ),
    );
    await expect(listAgentBrowsers("E2E", "lead")).rejects.toMatchObject({
      status: 401,
      code: "browser_operator_session_required",
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      invokeMock.mock.calls.filter((c) => c[1].action === "issue"),
    ).toHaveLength(2);
  });

  it("surfaces a failed native issue as a bridge error", async () => {
    invokeMock = vi.fn(async () => ({
      ok: false,
      error: "runtime not running",
      code: "browser_operator_bridge_unavailable",
    }));
    window.__TAURI_INTERNALS__ = { invoke: invokeMock as never };
    await expect(listAgentBrowsers("E2E", "lead")).rejects.toMatchObject({
      code: "browser_operator_bridge_unavailable",
      message: "runtime not running",
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refreshes held bearers and drops ones the shell rejects", async () => {
    installBridge();
    fetchMock.mockResolvedValue(jsonResponse({ browsers: [] }));
    await listAgentBrowsers("E2E", "lead");
    const lost = vi.fn();
    onBrowserOperatorSessionLost(lost);

    invokeMock.mockResolvedValueOnce({
      ok: false,
      code: "browser_operator_session_expired",
    });
    await refreshBrowserOperatorSessions();
    expect(invokeMock).toHaveBeenLastCalledWith("browser_operator_session", {
      action: "refresh",
      workspace: "E2E",
      token: "secret-token-1",
    });
    expect(lost).toHaveBeenCalledWith("E2E");

    fetchMock.mockResolvedValue(jsonResponse({ browsers: [] }));
    await listAgentBrowsers("E2E", "lead");
    expect(headerOf(fetchMock.mock.calls[1]!, "X-Loom-Operator-Session")).toBe(
      "secret-token-2",
    );
  });

  it("revokes held bearers on pagehide", async () => {
    installBridge();
    fetchMock.mockResolvedValue(jsonResponse({ browsers: [] }));
    await listAgentBrowsers("E2E", "lead");
    window.dispatchEvent(new Event("pagehide"));
    expect(invokeMock).toHaveBeenLastCalledWith("browser_operator_session", {
      action: "revoke",
      workspace: "E2E",
      token: "secret-token-1",
    });
    revokeBrowserOperatorSessions();
    expect(
      invokeMock.mock.calls.filter((c) => c[1].action === "revoke"),
    ).toHaveLength(1);
  });

  it("posts an empty body to select", async () => {
    installBridge();
    fetchMock.mockResolvedValue(jsonResponse({ ...BROWSER, selected: true }));
    const selected = await selectAgentBrowser("E2E", "lead", BROWSER.id);
    expect(selected.selected).toBe(true);
    const [url, init] = fetchMock.mock.calls[0]! as [string, RequestInit];
    expect(url).toBe(
      `/api/workspaces/E2E/agents/lead/browsers/${BROWSER.id}/select`,
    );
    expect(init.method).toBe("POST");
    expect(init.body).toBe("{}");
  });
});

describe("remote (oidc) mode", () => {
  it("uses the app bearer, no operator header, no native bridge", async () => {
    config.mode = "oidc";
    installBridge();
    setAuthToken("app-user-token");
    fetchMock.mockResolvedValue(jsonResponse({ browsers: [] }));

    await listAgentBrowsers("E2E", "lead");

    const call = fetchMock.mock.calls[0]!;
    expect(headerOf(call, "Authorization")).toBe("Bearer app-user-token");
    expect(headerOf(call, "X-Loom-Operator-Session")).toBeNull();
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("does not sign the user out on a browser 401", async () => {
    config.mode = "oidc";
    setAuthToken("app-user-token");
    fetchMock.mockResolvedValue(
      jsonResponse({ error: "no", code: "browser_forbidden" }, 401),
    );
    const err = await listAgentBrowsers("E2E", "lead").catch((e) => e);
    expect(err).toBeInstanceOf(BrowserApiError);
    expect((err as BrowserApiError).isAuthFailure).toBe(true);
    expect(getAuthToken()).toBe("app-user-token");
  });

  it("maps error JSON and FleetDB outages to typed errors", async () => {
    config.mode = "oidc";
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: "FleetDB down", code: "browser_unavailable" }, 503),
    );
    await expect(listAgentBrowsers("E2E", "lead")).rejects.toMatchObject({
      status: 503,
      code: "browser_unavailable",
      message: "FleetDB down",
    });
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(listAgentBrowsers("E2E", "lead")).rejects.toMatchObject({
      status: 0,
      code: "browser_unavailable",
    });
  });
});
