/**
 * Unit tests for the git API client (git.ts).
 *
 * These tests verify that fetchGitStatus calls the correct URL
 * and handles responses properly.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import { fetchGitStatus } from "../git";
import { get } from "../client";

vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
  return {
    ...actual,
    get: vi.fn(),
  };
});

const mockGet = get as ReturnType<typeof vi.fn>;

describe("fetchGitStatus", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls correct URL for agent name", async () => {
    const gitStatus = {
      branch: "feature-x",
      target_branch: "main",
      is_clean: true,
      ahead: 0,
      behind: 0,
      changed_files: [],
      conflicted_files: [],
      has_conflicts: false,
      stash_count: 0,
    };
    mockGet.mockResolvedValue(gitStatus);

    const result = await fetchGitStatus("ember");

    expect(result).toEqual(gitStatus);
    expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/git/status");
  });

  it("encodes agent name with special characters in URL", async () => {
    mockGet.mockResolvedValue({
      branch: "main",
      target_branch: "main",
      is_clean: true,
      ahead: 0,
      behind: 0,
      changed_files: [],
      conflicted_files: [],
      has_conflicts: false,
      stash_count: 0,
    });

    await fetchGitStatus("agent/with spaces");

    expect(mockGet).toHaveBeenCalledWith(
      "/api/agents/agent%2Fwith%20spaces/git/status",
    );
  });

  it("returns full git status object", async () => {
    const gitStatus = {
      branch: "feature-branch",
      target_branch: "main",
      is_clean: false,
      ahead: 3,
      behind: 1,
      changed_files: ["src/main.go", "README.md"],
      conflicted_files: ["src/conflict.go"],
      has_conflicts: true,
      stash_count: 2,
    };
    mockGet.mockResolvedValue(gitStatus);

    const result = await fetchGitStatus("nova");

    expect(result.branch).toBe("feature-branch");
    expect(result.target_branch).toBe("main");
    expect(result.is_clean).toBe(false);
    expect(result.ahead).toBe(3);
    expect(result.behind).toBe(1);
    expect(result.changed_files).toEqual(["src/main.go", "README.md"]);
    expect(result.conflicted_files).toEqual(["src/conflict.go"]);
    expect(result.has_conflicts).toBe(true);
    expect(result.stash_count).toBe(2);
  });

  it("propagates errors from the client", async () => {
    mockGet.mockRejectedValue(
      new Error("API Error: 500 Internal Server Error"),
    );

    await expect(fetchGitStatus("ember")).rejects.toThrow(
      "API Error: 500 Internal Server Error",
    );
  });

  it("propagates network errors", async () => {
    mockGet.mockRejectedValue(new Error("Network error"));

    await expect(fetchGitStatus("ember")).rejects.toThrow("Network error");
  });
});
