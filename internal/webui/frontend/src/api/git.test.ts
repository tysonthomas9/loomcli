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
  gitReset,
  gitUpdateTarget,
} from "@/api/git";

// Mock the API client
const mockGet = vi.fn();
const mockPost = vi.fn();
const mockPatch = vi.fn();

vi.mock("@/api/client", () => ({
  get: (...args: unknown[]) => mockGet(...args),
  post: (...args: unknown[]) => mockPost(...args),
  patch: (...args: unknown[]) => mockPatch(...args),
}));

describe("git API functions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("fetchGitStatus", () => {
    it("calls GET with correct URL", async () => {
      const mockStatus = { branch: "main", ahead: 0, behind: 0 };
      mockGet.mockResolvedValue(mockStatus);

      const result = await fetchGitStatus("nova");

      expect(mockGet).toHaveBeenCalledWith("/api/agents/nova/git/status");
      expect(result).toEqual(mockStatus);
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({});

      await fetchGitStatus("my agent");

      expect(mockGet).toHaveBeenCalledWith("/api/agents/my%20agent/git/status");
    });

    it("does not pass timeout options", async () => {
      mockGet.mockResolvedValue({});

      await fetchGitStatus("nova");

      // fetchGitStatus only passes the URL, no options
      expect(mockGet).toHaveBeenCalledWith("/api/agents/nova/git/status");
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

      const result = await gitPush("nova", "main");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/push",
        { target: "main" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined target when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitPush("nova");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/push",
        { target: undefined },
        { timeout: 60000 },
      );
    });

    it("encodes agent name in URL", async () => {
      mockPost.mockResolvedValue({});

      await gitPush("special/agent");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/special%2Fagent/git/push",
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

      const result = await gitPull("falcon", "origin/main");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/falcon/git/pull",
        { source: "origin/main" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined source when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitPull("falcon");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/falcon/git/pull",
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

      const result = await gitSync("ember");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/ember/git/sync",
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

      const result = await gitCreatePR("nova", "develop");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/pr",
        { target: "develop" },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined target when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitCreatePR("nova");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/pr",
        { target: undefined },
        { timeout: 60000 },
      );
    });
  });

  describe("gitReset", () => {
    it("calls POST with correct URL, body with branch and force, and timeout", async () => {
      const mockResult = { success: true, message: "Reset to main" };
      mockPost.mockResolvedValue(mockResult);

      const result = await gitReset("nova", "main", true);

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/reset",
        { branch: "main", force: true },
        { timeout: 60000 },
      );
      expect(result).toEqual(mockResult);
    });

    it("passes undefined branch and force when not specified", async () => {
      mockPost.mockResolvedValue({});

      await gitReset("nova");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/agents/nova/git/reset",
        { branch: undefined, force: undefined },
        { timeout: 60000 },
      );
    });
  });

  describe("gitUpdateTarget", () => {
    it("calls PATCH with correct URL and body", async () => {
      const mockResult = { success: true, branch: "develop" };
      mockPatch.mockResolvedValue(mockResult);

      const result = await gitUpdateTarget("nova", "develop");

      expect(mockPatch).toHaveBeenCalledWith("/api/agents/nova/git/target", {
        branch: "develop",
      });
      expect(result).toEqual(mockResult);
    });

    it("does not pass timeout option", async () => {
      mockPatch.mockResolvedValue({});

      await gitUpdateTarget("nova", "main");

      // patch is called with only URL and body, no options
      expect(mockPatch).toHaveBeenCalledWith("/api/agents/nova/git/target", {
        branch: "main",
      });
      expect(mockPatch.mock.calls[0]).toHaveLength(2);
    });
  });
});
