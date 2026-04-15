/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GitTab component.
 * Covers rendering with ahead/behind/commits/changes, fallback mode,
 * conflict banner, and commit expand button.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { GitStatus } from "@/api/workspace";
import type { LoomAgentStatus } from "@/types";

import { GitTab } from "./GitTab";

// Track the most recent useGitStatus call options for assertions
let lastGitStatusOptions: { agentName: string | null; enabled: boolean };
let mockGitStatusReturn: {
  status: GitStatus | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
};

const mockRefetch = vi.fn().mockResolvedValue(undefined);

// Mock useGitActions to provide a no-op actions object
const mockActions = {
  push: vi.fn(),
  pull: vi.fn(),
  sync: vi.fn(),
  createPR: vi.fn(),
  reset: vi.fn(),
  updateTarget: vi.fn(),
  pushState: { isLoading: false, error: null },
  pullState: { isLoading: false, error: null },
  syncState: { isLoading: false, error: null },
  prState: { isLoading: false, error: null },
  resetState: { isLoading: false, error: null },
  targetState: { isLoading: false, error: null },
  anyLoading: false,
};

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useGitStatus: (opts: { agentName: string | null; enabled: boolean }) => {
      lastGitStatusOptions = opts;
      return mockGitStatusReturn;
    },
    useGitActions: () => mockActions,
  };
});

// Mock fetchDiffCommits to avoid real API calls
let mockDiffCommitsResult: Promise<unknown>;

vi.mock("@/api/issues", () => ({
  fetchDiffCommits: () => mockDiffCommitsResult,
}));

/** Helper to build a minimal agent object. */
function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "ember",
    branch: "feature-x",
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

function resetMocks() {
  mockGitStatusReturn = {
    status: null,
    loading: false,
    error: null,
    refetch: mockRefetch,
  };
  mockDiffCommitsResult = Promise.resolve([]);
}

/** Render GitTab and flush the async fetchDiffCommits effect. */
async function renderGitTab(agent: LoomAgentStatus) {
  let result: ReturnType<typeof render>;
  await act(async () => {
    result = render(<GitTab agent={agent} />);
  });
  return result!;
}

describe("GitTab", () => {
  beforeEach(() => {
    resetMocks();
  });

  describe("branch header", () => {
    it("displays branch and target branch from git status", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "develop",
          is_clean: true,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(screen.getByText("feature-x")).toBeInTheDocument();
      expect(screen.getByText("develop")).toBeInTheDocument();
    });

    it("falls back to agent.branch when gitStatus is null", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent({ branch: "fallback-branch" }));

      expect(screen.getByText("fallback-branch")).toBeInTheDocument();
    });
  });

  describe("ahead/behind badges", () => {
    it("shows ahead badge when commits are ahead", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 5,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(screen.getByText("+5 ahead")).toBeInTheDocument();
    });

    it("shows behind badge when commits are behind", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 0,
          behind: 3,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(screen.getByText("-3 behind")).toBeInTheDocument();
    });

    it("shows both ahead and behind badges", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 2,
          behind: 4,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(screen.getByText("+2 ahead")).toBeInTheDocument();
      expect(screen.getByText("-4 behind")).toBeInTheDocument();
    });

    it("shows 'In sync' when both ahead and behind are 0", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent({ ahead: 0, behind: 0 }));

      expect(screen.getByText("In sync")).toBeInTheDocument();
    });

    it("falls back to agent ahead/behind when gitStatus is null", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent({ ahead: 7, behind: 2 }));

      expect(screen.getByText("+7 ahead")).toBeInTheDocument();
      expect(screen.getByText("-2 behind")).toBeInTheDocument();
    });
  });

  describe("fallback mode", () => {
    it("shows fallback note when gitStatus has error and status is null", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: new Error("Connection refused"),
      };

      await renderGitTab(makeAgent());

      expect(
        screen.getByText("Git status unavailable — showing cached data."),
      ).toBeInTheDocument();
    });

    it("does not show fallback note when gitStatus is available", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(
        screen.queryByText("Git status unavailable — showing cached data."),
      ).not.toBeInTheDocument();
    });

    it("does not show fallback note when there is no error", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(
        screen.queryByText("Git status unavailable — showing cached data."),
      ).not.toBeInTheDocument();
    });
  });

  describe("conflict banner", () => {
    it("shows conflict banner when has_conflicts is true with conflicted files", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: false,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: ["src/main.go", "src/util.go"],
          has_conflicts: true,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(screen.getByText("Merge conflicts detected")).toBeInTheDocument();
      expect(screen.getByText("src/main.go")).toBeInTheDocument();
      expect(screen.getByText("src/util.go")).toBeInTheDocument();
    });

    it("does not show conflict banner when has_conflicts is false", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(
        screen.queryByText("Merge conflicts detected"),
      ).not.toBeInTheDocument();
    });

    it("does not show conflict banner when conflicted_files is empty", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: false,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: true,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      // has_conflicts is true but conflicted_files is empty, so banner hidden
      expect(
        screen.queryByText("Merge conflicts detected"),
      ).not.toBeInTheDocument();
    });

    it("does not show conflict banner in fallback mode (gitStatus is null)", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent());

      expect(
        screen.queryByText("Merge conflicts detected"),
      ).not.toBeInTheDocument();
    });
  });

  describe("commit list", () => {
    it("renders commits from agent data when diffCommits are not available", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      // Diff commits fetch fails — should fall back to agent.commits
      mockDiffCommitsResult = Promise.reject(new Error("not found"));

      const agent = makeAgent({
        ahead: 2,
        commits: [
          {
            hash: "abc1234",
            message: "First commit",
            url: "https://example.com/abc1234",
          },
          { hash: "def5678", message: "Second commit" },
        ],
      });

      await renderGitTab(agent);

      expect(screen.getByText("abc1234")).toBeInTheDocument();
      expect(screen.getByText("First commit")).toBeInTheDocument();
      expect(screen.getByText("def5678")).toBeInTheDocument();
      expect(screen.getByText("Second commit")).toBeInTheDocument();
    });

    it("shows 'In sync with main' when no commits and ahead is 0", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 0,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent({ ahead: 0, commits: [] }));

      expect(screen.getByText("In sync with main")).toBeInTheDocument();
    });

    it("shows 'No commit data' when no commits but ahead > 0", async () => {
      mockGitStatusReturn = {
        status: {
          branch: "feature-x",
          target_branch: "main",
          is_clean: true,
          ahead: 3,
          behind: 0,
          changed_files: [],
          conflicted_files: [],
          has_conflicts: false,
          stash_count: 0,
        },
        loading: false,
        error: null,
      };

      await renderGitTab(makeAgent({ ahead: 3, commits: [] }));

      expect(screen.getByText("No commit data")).toBeInTheDocument();
    });

    it("shows expand button when more than 10 commits", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      // Diff commits never resolves so diffCommits stays null, falling back to agent.commits
      mockDiffCommitsResult = new Promise(() => {});

      const commits = Array.from({ length: 15 }, (_, i) => ({
        hash: `hash${i}`,
        message: `Commit message ${i}`,
      }));

      await renderGitTab(makeAgent({ ahead: 15, commits }));

      // Only first 10 should be visible
      expect(screen.getByText("hash0")).toBeInTheDocument();
      expect(screen.getByText("hash9")).toBeInTheDocument();
      expect(screen.queryByText("hash10")).not.toBeInTheDocument();

      // Expand button should be present
      expect(screen.getByText("Show all 15 commits")).toBeInTheDocument();
    });

    it("expands to show all commits when expand button is clicked", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      // Diff commits never resolves so diffCommits stays null, falling back to agent.commits
      mockDiffCommitsResult = new Promise(() => {});

      const commits = Array.from({ length: 12 }, (_, i) => ({
        hash: `hash${i}`,
        message: `Commit message ${i}`,
      }));

      await renderGitTab(makeAgent({ ahead: 12, commits }));

      // Click expand button
      fireEvent.click(screen.getByText("Show all 12 commits"));

      // All commits should now be visible
      expect(screen.getByText("hash11")).toBeInTheDocument();

      // Expand button should be gone
      expect(screen.queryByText("Show all 12 commits")).not.toBeInTheDocument();
    });

    it("does not show expand button when 10 or fewer commits", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      // Diff commits never resolves so diffCommits stays null, falling back to agent.commits
      mockDiffCommitsResult = new Promise(() => {});

      const commits = Array.from({ length: 10 }, (_, i) => ({
        hash: `hash${i}`,
        message: `Commit message ${i}`,
      }));

      await renderGitTab(makeAgent({ ahead: 10, commits }));

      expect(screen.queryByText(/Show all/)).not.toBeInTheDocument();
    });

    it("renders commit links when URL is provided", async () => {
      mockGitStatusReturn = {
        status: null,
        loading: false,
        error: null,
      };

      // Diff commits never resolves so diffCommits stays null, falling back to agent.commits
      mockDiffCommitsResult = new Promise(() => {});

      await renderGitTab(
        makeAgent({
          ahead: 1,
          commits: [
            {
              hash: "abc1234",
              message: "Fix bug",
              url: "https://github.com/repo/commit/abc1234",
            },
          ],
        }),
      );

      const link = screen.getByText("abc1234").closest("a");
      expect(link).toHaveAttribute(
        "href",
        "https://github.com/repo/commit/abc1234",
      );
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });
  });

  describe("hook invocation", () => {
    it("passes agent name and enabled=true to useGitStatus", async () => {
      await renderGitTab(makeAgent({ name: "nova" }));

      expect(lastGitStatusOptions.agentName).toBe("nova");
      expect(lastGitStatusOptions.enabled).toBe(true);
    });
  });
});
