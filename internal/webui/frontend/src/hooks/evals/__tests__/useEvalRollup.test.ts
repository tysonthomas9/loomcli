/**
 * @vitest-environment jsdom
 */
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchEvalRollup } from "@/api/evals";

import { useEvalRollup } from "../useEvalRollup";

vi.mock("@/api/evals", async () => {
  const actual =
    await vi.importActual<typeof import("@/api/evals")>("@/api/evals");
  return {
    ...actual,
    fetchEvalRollup: vi.fn(),
  };
});

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "WS" }),
  };
});

const mockFetchEvalRollup = vi.mocked(fetchEvalRollup);

const emptyRollup = {
  since: "2026-07-10T12:00:00.000Z",
  until: "2026-07-17T12:00:00.000Z",
  eval_count: 0,
  score_averages: {
    outcome_success: 0,
    instruction_adherence: 0,
    efficiency: 0,
    tool_use_quality: 0,
  },
  score_buckets: [],
  tag_frequencies: [],
  insights: { harness: [], linter: [], prompt: [], skill: [] },
  failure_classes: [],
  judge_prompt_versions: [],
};

describe("useEvalRollup", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-07-17T12:00:00.000Z"));
    mockFetchEvalRollup.mockResolvedValue(emptyRollup);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("builds a 7d since/until window for the rollup fetch", async () => {
    renderHook(() => useEvalRollup(7, { pollInterval: 0 }));

    await waitFor(() => expect(mockFetchEvalRollup).toHaveBeenCalledTimes(1));
    expect(mockFetchEvalRollup).toHaveBeenCalledWith("WS", {
      since: "2026-07-10T12:00:00.000Z",
      until: "2026-07-17T12:00:00.000Z",
    });
  });

  it("builds a 30d since/until window for the rollup fetch", async () => {
    renderHook(() => useEvalRollup(30, { pollInterval: 0 }));

    await waitFor(() => expect(mockFetchEvalRollup).toHaveBeenCalledTimes(1));
    expect(mockFetchEvalRollup).toHaveBeenCalledWith("WS", {
      since: "2026-06-17T12:00:00.000Z",
      until: "2026-07-17T12:00:00.000Z",
    });
  });
});
