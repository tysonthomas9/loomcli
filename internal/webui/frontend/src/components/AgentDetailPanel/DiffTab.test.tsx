/**
 * @vitest-environment jsdom
 */
import React from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { DiffTab } from "./DiffTab";
import * as useDiffModule from "@/hooks/useDiff";
import type { UseDiffReturn, DiffFile } from "@/hooks/useDiff";

// Mock useDiff
vi.mock("@/hooks/useDiff", () => ({
  useDiff: vi.fn(),
}));

function createFile(path: string): DiffFile {
  return { path, status: "M", additions: 3, deletions: 1 };
}

function createMockUseDiff(
  overrides: Partial<UseDiffReturn> = {},
): UseDiffReturn {
  return {
    files: [],
    isLoading: false,
    error: null,
    patchCache: new Map(),
    patchErrors: new Map(),
    fetchPatch: vi.fn(),
    clearPatchError: vi.fn(),
    ...overrides,
  };
}

describe("DiffTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("per-file patch error does not affect other expanded files", () => {
    const patchErrors = new Map<string, Error>();
    patchErrors.set("src/a.ts", new Error("Failed to fetch src/a.ts"));

    vi.mocked(useDiffModule.useDiff).mockReturnValue(
      createMockUseDiff({
        files: [createFile("src/a.ts"), createFile("src/b.ts")],
        patchErrors,
      }),
    );

    render(<DiffTab agentId="agent-1" toCommit="abc123" />);

    // Expand both files
    fireEvent.click(screen.getByTestId("diff-toggle-src/a.ts"));
    fireEvent.click(screen.getByTestId("diff-toggle-src/b.ts"));

    // File A should show error
    expect(screen.getByTestId("diff-error-src/a.ts")).toBeTruthy();
    expect(screen.getByTestId("diff-error-src/a.ts").textContent).toContain(
      "Failed to fetch src/a.ts",
    );

    // File B should show loading (no error, no cached patch)
    expect(screen.getByTestId("diff-loading-src/b.ts")).toBeTruthy();
    expect(screen.queryByTestId("diff-error-src/b.ts")).toBeNull();
  });

  it("expanded file with no patch and no error shows loading", () => {
    vi.mocked(useDiffModule.useDiff).mockReturnValue(
      createMockUseDiff({
        files: [createFile("src/c.ts")],
      }),
    );

    render(<DiffTab agentId="agent-1" toCommit="abc123" />);

    // Expand the file
    fireEvent.click(screen.getByTestId("diff-toggle-src/c.ts"));

    // Should show loading
    expect(screen.getByTestId("diff-loading-src/c.ts")).toBeTruthy();
    expect(screen.queryByTestId("diff-error-src/c.ts")).toBeNull();
  });

  it("shows file-list error when files is empty and error exists", () => {
    vi.mocked(useDiffModule.useDiff).mockReturnValue(
      createMockUseDiff({
        error: new Error("Network error"),
      }),
    );

    render(<DiffTab agentId="agent-1" toCommit="abc123" />);

    expect(screen.getByTestId("diff-tab-error").textContent).toContain(
      "Network error",
    );
  });

  it("shows patch content when cached", () => {
    const patchCache = new Map();
    patchCache.set("src/d.ts", {
      patch: "@@ -1,3 +1,5 @@\n+new line",
      is_binary: false,
      is_too_large: false,
      additions: 2,
      deletions: 0,
    });

    vi.mocked(useDiffModule.useDiff).mockReturnValue(
      createMockUseDiff({
        files: [createFile("src/d.ts")],
        patchCache,
      }),
    );

    render(<DiffTab agentId="agent-1" toCommit="abc123" />);

    fireEvent.click(screen.getByTestId("diff-toggle-src/d.ts"));

    expect(screen.getByTestId("diff-patch-src/d.ts").textContent).toContain(
      "+new line",
    );
  });
});
