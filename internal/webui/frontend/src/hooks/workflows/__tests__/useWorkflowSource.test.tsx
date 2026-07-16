/** @vitest-environment jsdom */

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  clearLocalWorkflowLifecycleSession,
  exchangeLocalOperatorLaunch,
  getLocalWorkflowLifecycleBearer,
} from "@/api/workflows/localOperatorSession";
import { useWorkflowSource } from "../useWorkflowSource";

const launchCode = "ab".repeat(32);
const accessToken = "cd".repeat(32);

beforeEach(() => {
  clearLocalWorkflowLifecycleSession();
  localStorage.clear();
  sessionStorage.clear();
  vi.restoreAllMocks();
});

afterEach(() => {
  clearLocalWorkflowLifecycleSession();
});

describe("useWorkflowSource Desktop authority scope", () => {
  it("disables lifecycle management and clears authority after a workspace switch", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
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

    const { result, rerender } = renderHook(
      ({ workspaceId }) => useWorkflowSource(workspaceId),
      { initialProps: { workspaceId: "TEST" } },
    );
    expect(result.current.canManageVersions).toBe(true);

    rerender({ workspaceId: "OTHER" });

    expect(result.current.canManageVersions).toBe(false);
    expect(getLocalWorkflowLifecycleBearer("TEST")).toBeNull();
  });
});
