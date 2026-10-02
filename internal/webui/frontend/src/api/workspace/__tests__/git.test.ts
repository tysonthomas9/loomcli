/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for git API functions.
 * Verifies each function calls the correct HTTP method, URL, and passes
 * timeout options where appropriate.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  fetchGitStatus,
  gitPush,
  gitPull,
  gitSync,
  gitCreatePR,
  gitMergePreview,
  gitMergeUpTo,
  gitMergeRequests,
  gitConfirmMergeRequest,
  gitReset,
  gitResetPreview,
  gitUpdateTarget,
} from "../git";

// Mock the API client
const mockGet = vi.fn();
const mockPost = vi.fn();
const mockPatch = vi.fn();

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: (...args: unknown[]) => mockGet(...args),
    post: (...args: unknown[]) => mockPost(...args),
    patch: (...args: unknown[]) => mockPatch(...args),
  };
});

describe("git API functions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("fetchGitStatus", () => {
    it("calls GET with correct URL", async () => {
      const mockStatus = { branch: "main", ahead: 0, behind: 0 };
      mockGet.mockResolvedValue(mockStatus);

      const result = await fetchGitStatus("test-ws-id", "nova");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/status",
      );
      expect(result).toEqual(mockStatus);
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({});

      await fetchGitStatus("test-ws-id", "my agent");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/my%20agent/git/status",
      );
    });

    it("does not pass timeout options", async () => {
      mockGet.mockResolvedValue({});

      await fetchGitStatus("test-ws-id", "nova");

      // fetchGitStatus only passes the URL, no options
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/status",
      );
      expect(mockGet.mock.calls[0]).toHaveLength(1);
    });
  });

  describe("gitPush", () => {
    it("calls POST with correct URL, body, and timeout", async () => {
      const mockResult = {
        success: true,
        message: "Pushed",
        already_up_to_date: false,
      };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitPush("test-ws-id", "nova", "main");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/push",
        { target: "main" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined target when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitPush("test-ws-id", "nova");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/push",
        { target: undefined },
        { timeout: 60000 },
      );
    });

    it("encodes agent name in URL", async () => {
      mockPost.mockResolvedValue({});

      await gitPush("test-ws-id", "special/agent");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/special%2Fagent/git/push",
        { target: undefined },
        { timeout: 60000 },
      );
    });
  });

  describe("gitPull", () => {
    it("calls POST with correct URL, body, and timeout", async () => {
      const mockResult = {
        success: true,
        message: "Pulled",
        already_up_to_date: false,
      };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitPull("test-ws-id", "falcon", "origin/main");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/falcon/git/pull",
        { source: "origin/main" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined source when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitPull("test-ws-id", "falcon");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/falcon/git/pull",
        { source: undefined },
        { timeout: 60000 },
      );
    });
  });

  describe("gitSync", () => {
    it("calls POST with correct URL, empty body, and timeout", async () => {
      const mockResult = {
        push_result: { success: true },
        pull_result: { success: true },
      };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitSync("test-ws-id", "ember");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/git/sync",
        {},
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });
  });

  describe("gitCreatePR", () => {
    it("calls POST with correct URL, body, and timeout", async () => {
      const mockResult = {
        url: "https://github.com/repo/pull/1",
        created: true,
        already_exists: false,
        no_commits: false,
      };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitCreatePR("test-ws-id", "nova", "C-1");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/pr",
        { change_id: "C-1" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("requires the recorded change ID", async () => {
      mockPost.mockResolvedValue({});

      await gitCreatePR("test-ws-id", "nova", "C-2");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/pr",
        { change_id: "C-2" },
        { timeout: 60000 },
      );
    });
  });

  describe("gitMergeUpTo", () => {
    it("previews and submits the exact four confirmed heads", async () => {
      const preview = {
        stack_id: "feature",
        target: "C",
        backend: "loom",
        phase: "",
        layers: [
          { change: "A", head: "a", pr_url: "", state: "pending" },
          { change: "B", head: "b", pr_url: "", state: "pending" },
          { change: "C", head: "c", pr_url: "", state: "pending" },
          { change: "D", head: "d", pr_url: "", state: "pending" },
        ],
      };
      mockGet.mockResolvedValue(preview);
      mockPost.mockResolvedValue({ ...preview, phase: "ready" });
      await gitMergePreview("W", "L", "feature", "C");
      await gitMergeUpTo("W", "L", preview);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/W/agents/L/git/merge-up-to?stack_id=feature&target=C",
      );
      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/W/agents/L/git/merge-up-to",
        {
          stack_id: "feature",
          target: "C",
          heads: ["a", "b", "c", "d"],
          actor: { kind: "human", id: "local-user" },
        },
      );
    });

    it("lists merge requests and confirms one as a human", async () => {
      mockGet.mockResolvedValue([]);
      mockPost.mockResolvedValue({ phase: "ready" });
      await gitMergeRequests("W", "L");
      await gitConfirmMergeRequest("W", "L", "R1");
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/W/agents/L/git/merge-requests",
      );
      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/W/agents/L/git/merge-requests/R1/confirm",
        { actor: { kind: "human", id: "local-user" } },
      );
    });
  });

  describe("gitReset", () => {
    it("calls POST with correct URL, body with branch and force, and timeout", async () => {
      const mockResult = { success: true, message: "Reset to main" };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitReset("test-ws-id", "nova", "main", true);

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/reset",
        { branch: "main", force: true },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined branch and force when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitReset("test-ws-id", "nova");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/reset",
        { branch: undefined, force: undefined },
        { timeout: 60000 },
      );
    });
  });

  describe("gitResetPreview", () => {
    it("fetches ignored paths and sizes before reset", async () => {
      const preview = { ignored: [{ path: "build/output.bin", size: 2048 }] };
      mockGet.mockResolvedValue(preview);

      expect(await gitResetPreview("test-ws-id", "nova")).toEqual(preview);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/reset-preview",
      );
    });
  });

  describe("gitUpdateTarget", () => {
    it("calls PATCH with correct URL and body", async () => {
      const mockResult = { success: true, branch: "develop" };
      mockPatch.mockResolvedValue(mockResult);

      const result = await gitUpdateTarget("test-ws-id", "nova", "develop");

      expect(mockPatch).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/target",
        {
          branch: "develop",
        },
      );
      expect(result).toEqual(mockResult);
    });

    it("does not pass timeout option", async () => {
      mockPatch.mockResolvedValue({});

      await gitUpdateTarget("test-ws-id", "nova", "main");

      // patch is called with only URL and body, no options
      expect(mockPatch).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/nova/git/target",
        {
          branch: "main",
        },
      );
      expect(mockPatch.mock.calls[0]).toHaveLength(2);
    });
  });
});
