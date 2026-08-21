/**
 * @vitest-environment jsdom
 */

import type { ReactNode } from "react";
import { createElement } from "react";
import { renderHook, act, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { DiffCommit } from "@/api/issues";
import { agentQueryKeys } from "@/hooks/queryKeys";
import { createTestQueryClient } from "@/test-utils/queryClient";

import { useExpandedCommits } from "./useExpandedCommits";

const mockApi = vi.hoisted(() => ({
  fetchDiffCommits: vi.fn(),
}));

vi.mock("@/hooks/api", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/api")>("@/hooks/api");
  return {
    ...actual,
    fetchDiffCommits: mockApi.fetchDiffCommits,
  };
});

const commits: DiffCommit[] = [
  {
    hash: "abcdef123456",
    short_hash: "abcdef1",
    subject: "Add query cache",
    author: "Nova",
    email: "nova@example.com",
    date: "2026-01-01T00:00:00Z",
  },
];

let queryClient: ReturnType<typeof createTestQueryClient>;

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: queryClient }, children);
}

describe("useExpandedCommits", () => {
  beforeEach(() => {
    queryClient = createTestQueryClient();
    mockApi.fetchDiffCommits.mockReset();
  });

  afterEach(() => {
    queryClient.clear();
  });

  it("fetches commits lazily when expanded", async () => {
    mockApi.fetchDiffCommits.mockResolvedValue(commits);

    const { result } = renderHook(
      () => useExpandedCommits("test-ws-id", "nova"),
      { wrapper },
    );

    expect(result.current.expandedCommits).toBeNull();
    expect(result.current.loadingCommits).toBe(false);
    expect(mockApi.fetchDiffCommits).not.toHaveBeenCalled();

    await act(async () => {
      await result.current.handleShowAll();
    });

    await waitFor(() => {
      expect(result.current.expandedCommits).toEqual([
        { hash: "abcdef1", message: "Add query cache" },
      ]);
    });
    expect(mockApi.fetchDiffCommits).toHaveBeenCalledWith(
      "test-ws-id",
      "nova",
    );
  });

  it("uses the shared diffCommits query key", async () => {
    queryClient.setQueryData(
      agentQueryKeys.diffCommits("test-ws-id", "nova"),
      commits,
    );
    mockApi.fetchDiffCommits.mockImplementation(() => new Promise(() => {}));

    const { result } = renderHook(
      () => useExpandedCommits("test-ws-id", "nova"),
      { wrapper },
    );

    await act(async () => {
      await result.current.handleShowAll();
    });

    expect(result.current.expandedCommits).toEqual([
      { hash: "abcdef1", message: "Add query cache" },
    ]);
    expect(
      queryClient.getQueryData(
        agentQueryKeys.diffCommits("test-ws-id", "nova"),
      ),
    ).toBe(commits);
  });
});
