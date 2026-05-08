/**
 * @vitest-environment jsdom
 */

import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { fetchOnboardingStatus } from "@/api/onboarding";
import type { OnboardingStatusWire } from "@/types/onboarding";

import {
  useOnboardingStatus,
  ONBOARDING_DISMISS_KEY,
} from "../useOnboardingStatus";

vi.mock("@/api/onboarding", () => ({
  fetchOnboardingStatus: vi.fn(),
}));

const mockFetch = vi.mocked(fetchOnboardingStatus);

function makeWire(
  overrides?: Partial<OnboardingStatusWire>,
): OnboardingStatusWire {
  return {
    workspace_id: "ws-1",
    active_repo: "my-app",
    all_complete: false,
    steps: [
      { id: "workspace-repo", status: "complete", action: "open_workspace_repo_wizard" },
      { id: "verify-repo", status: "complete", action: "open_repo_checks" },
      { id: "setup-backend", status: "actionable", action: "open_backend_setup", message: "auth missing" },
      { id: "create-agent", status: "blocked", action: "open_create_agent" },
      { id: "create-issue", status: "blocked", action: "open_create_issue" },
      { id: "run-agent", status: "blocked", action: "start_first_agent" },
    ],
    ...overrides,
  };
}

describe("useOnboardingStatus", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("fetches workspace-scoped status and joins with registry", async () => {
    mockFetch.mockResolvedValueOnce(makeWire());

    const { result } = renderHook(() => useOnboardingStatus("ws-1"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(mockFetch).toHaveBeenCalledWith("ws-1");
    expect(result.current.steps).toHaveLength(6);
    // Order is registry order
    expect(result.current.steps.map((s) => s.id)).toEqual([
      "workspace-repo",
      "verify-repo",
      "setup-backend",
      "create-agent",
      "create-issue",
      "run-agent",
    ]);
    // Registry definition is joined: each step has a label, ctaLabel, action
    const setupStep = result.current.steps.find((s) => s.id === "setup-backend")!;
    expect(setupStep.status).toBe("actionable");
    expect(setupStep.message).toBe("auth missing");
    expect(setupStep.label).toBeTruthy();
    expect(setupStep.ctaLabel).toBeTruthy();
    expect(result.current.activeRepo).toBe("my-app");
    expect(result.current.allComplete).toBe(false);
  });

  it("hits the top-level endpoint when no workspace id is provided", async () => {
    mockFetch.mockResolvedValueOnce({
      all_complete: false,
      steps: [
        { id: "workspace-repo", status: "actionable", action: "open_workspace_repo_wizard" },
      ],
    });

    const { result } = renderHook(() => useOnboardingStatus(undefined));

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockFetch).toHaveBeenCalledWith(undefined);
    // Steps missing from the wire fall back to blocked from the registry.
    const verify = result.current.steps.find((s) => s.id === "verify-repo")!;
    expect(verify.status).toBe("blocked");
  });

  it("dismiss sets the per-workspace flag and updates state", async () => {
    mockFetch.mockResolvedValueOnce(makeWire());

    const { result } = renderHook(() => useOnboardingStatus("ws-1"));
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.isDismissed).toBe(false);
    act(() => result.current.dismiss());
    expect(result.current.isDismissed).toBe(true);
    expect(localStorage.getItem(`loom:ws-1:${ONBOARDING_DISMISS_KEY}`)).toBe("1");
  });

  it("dismiss is a no-op when there is no workspace", async () => {
    mockFetch.mockResolvedValueOnce({
      all_complete: false,
      steps: [
        { id: "workspace-repo", status: "actionable", action: "open_workspace_repo_wizard" },
      ],
    });
    const { result } = renderHook(() => useOnboardingStatus(undefined));
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    act(() => result.current.dismiss());
    expect(result.current.isDismissed).toBe(false);
  });

  it("reads existing dismiss flag on mount", async () => {
    localStorage.setItem(`loom:ws-1:${ONBOARDING_DISMISS_KEY}`, "1");
    mockFetch.mockResolvedValueOnce(makeWire());

    const { result } = renderHook(() => useOnboardingStatus("ws-1"));
    expect(result.current.isDismissed).toBe(true);
  });

  it("surfaces fetch errors", async () => {
    mockFetch.mockRejectedValueOnce(new Error("network down"));
    const { result } = renderHook(() => useOnboardingStatus("ws-1"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("network down");
  });

  it("refetch re-hits the endpoint", async () => {
    mockFetch.mockResolvedValue(makeWire());
    const { result } = renderHook(() => useOnboardingStatus("ws-1"));
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(mockFetch).toHaveBeenCalledTimes(1);
    await act(async () => {
      await result.current.refetch();
    });
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });
});
