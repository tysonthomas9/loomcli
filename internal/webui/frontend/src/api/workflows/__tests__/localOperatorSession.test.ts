/** @vitest-environment jsdom */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  captureLocalOperatorLaunchFromFragment,
  clearLocalWorkflowLifecycleSession,
  exchangeLocalOperatorLaunch,
  getLocalWorkflowLifecycleBearer,
} from "../localOperatorSession";

const launchCode = "ab".repeat(32);
const accessToken = "cd".repeat(32);

beforeEach(() => {
  clearLocalWorkflowLifecycleSession();
  window.history.replaceState({}, "", "/ws/TEST/automations");
  vi.restoreAllMocks();
});

afterEach(() => {
  clearLocalWorkflowLifecycleSession();
});

describe("local Workflow Catalog Desktop authorization", () => {
  it("erases launch material synchronously before exchange", () => {
    window.history.replaceState(
      { retained: true },
      "",
      `/ws/TEST/automations?tab=one#section=versions&loom_launch=${launchCode}&loom_workspace=TEST`,
    );

    const launch = captureLocalOperatorLaunchFromFragment();

    expect(launch).toEqual({ launchCode, workspace: "TEST" });
    expect(window.location.hash).toBe("#section=versions");
    expect(window.location.href).not.toContain(launchCode);
    expect(window.history.state).toEqual({ retained: true });
  });

  it("exchanges once and keeps the bearer only in module memory", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          access_token: accessToken,
          token_type: "Bearer",
          workspace: "TEST",
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    await exchangeLocalOperatorLaunch({ launchCode, workspace: "TEST" });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/workspaces/TEST/operator-sessions/exchange",
      expect.objectContaining({
        method: "POST",
        cache: "no-store",
        body: JSON.stringify({ launch_code: launchCode }),
      }),
    );
    expect(getLocalWorkflowLifecycleBearer("TEST")).toBe(accessToken);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(document.cookie).toBe("");
  });

  it("clears the session when another workspace requests its bearer", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          access_token: accessToken,
          token_type: "Bearer",
          workspace: "TEST",
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await exchangeLocalOperatorLaunch({ launchCode, workspace: "TEST" });

    expect(getLocalWorkflowLifecycleBearer("OTHER")).toBeNull();
    expect(getLocalWorkflowLifecycleBearer("TEST")).toBeNull();
  });

  it("fails closed and still erases malformed fragment material", () => {
    window.history.replaceState(
      {},
      "",
      "/#loom_launch=not-a-token&loom_workspace=TEST",
    );
    expect(() => captureLocalOperatorLaunchFromFragment()).toThrow(
      /invalid workflow authorization launch/,
    );
    expect(window.location.hash).toBe("");
  });

  it("rejects expired or malformed exchange responses", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          access_token: "bad",
          token_type: "Bearer",
          workspace: "TEST",
          expires_at: new Date(Date.now() - 1).toISOString(),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await expect(
      exchangeLocalOperatorLaunch({ launchCode, workspace: "TEST" }),
    ).rejects.toThrow(/invalid workflow authorization/);
    expect(getLocalWorkflowLifecycleBearer("TEST")).toBeNull();
  });
});
