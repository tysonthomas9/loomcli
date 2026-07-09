// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import { ApiError } from "@/types/common/errors";

import {
  PRReviewWorkspace,
  agentsLinkedToIssue,
  resolveDiffAgentForIssue,
} from "../PRReviewWorkspace";

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  onBack: vi.fn(),
  updateIssue: vi.fn(),
  startAgent: vi.fn(),
  getPullRequestDetail: vi.fn(),
  postPullRequestReview: vi.fn(),
  data: {
    agents: [] as unknown[],
    issues: [] as unknown[],
  },
  actions: {
    refetch: vi.fn(),
    showToast: vi.fn(),
    updateIssueStatus: vi.fn(),
    handleIssueClick: vi.fn(),
  },
  workspaceContext: {
    repos: [] as unknown[],
    workspaceId: "WS",
  },
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => mocks.navigate,
  };
});

vi.mock("@/api", () => ({
  updateIssue: mocks.updateIssue,
}));

vi.mock("@/api/workspace/prReview", () => ({
  getPullRequestDetail: mocks.getPullRequestDetail,
  postPullRequestReview: mocks.postPullRequestReview,
}));

vi.mock("@/hooks/api", () => ({
  startAgent: mocks.startAgent,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => mocks.workspaceContext,
}));

vi.mock("@/contexts/WorkspaceViewContext", () => ({
  useWorkspaceViewData: () => mocks.data,
  useWorkspaceViewActions: () => mocks.actions,
}));

vi.mock("@/components/CreateAgentModal/CreateAgentModal", () => ({
  CreateAgentModal: () => <div data-testid="create-agent-modal" />,
}));

vi.mock("@/components/IssueDetailPanel", () => ({
  PRCompareDiffPane: () => <div data-testid="pr-compare-diff" />,
  PRFilesTab: () => <div data-testid="pr-files-tab" />,
}));

vi.mock("@/components/IssueDetailPanel/sessions/TaskSessionDiffPane", () => ({
  TaskSessionDiffPane: () => <div data-testid="task-session-diff" />,
}));

vi.mock("@/components/AgentWorkPanel/AgentWorkPanel", () => ({
  buildWorkerByTaskId: (
    agents: Array<{ name: string; task_id?: string | null }>,
  ) => {
    const byTask = new Map<string, { name: string; task_id?: string | null }>();
    for (const agent of agents) {
      if (agent.task_id) byTask.set(agent.task_id, agent);
    }
    return byTask;
  },
  isWorkerTerminalOpenable: () => true,
}));

function makeAgent(
  overrides: Partial<LoomAgentStatus> & Pick<LoomAgentStatus, "name">,
): LoomAgentStatus {
  return {
    status: "idle",
    desired_state: "stopped",
    mode: "persistent",
    ...overrides,
  } as LoomAgentStatus;
}

function makeIssue(overrides: Partial<Issue> & Pick<Issue, "id">): Issue {
  return {
    title: "Test issue",
    status: "review",
    ...overrides,
  } as Issue;
}

function makePullRequest(
  overrides: Partial<GitPullRequest> = {},
): GitPullRequest {
  return {
    number: 7,
    title: "Review PR",
    url: "https://github.com/octocat/hello/pull/7",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feature",
    base_ref_name: "main",
    repo_name: "octocat/hello",
    ...overrides,
  };
}

function makePullRequestIssue(overrides: Partial<Issue> = {}): Issue {
  return makeIssue({
    id: "TASK-1",
    title: "Review PR",
    external_ref: "https://github.com/octocat/hello/pull/7",
    ...overrides,
  });
}

function renderWorkspace(
  props: Partial<{
    issue: Issue;
    pullRequest: GitPullRequest;
    onBack: () => void;
  }> = {},
) {
  return render(
    <PRReviewWorkspace
      issue={props.issue ?? makePullRequestIssue()}
      pullRequest={props.pullRequest ?? makePullRequest()}
      onBack={props.onBack ?? mocks.onBack}
    />,
  );
}

describe("PRReviewWorkspace decisions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.data.agents = [];
    mocks.data.issues = [];
    mocks.workspaceContext.workspaceId = "WS";
    mocks.getPullRequestDetail.mockResolvedValue({
      number: 7,
      state: "OPEN",
      title: "Review PR",
      is_draft: false,
      head_ref_name: "feature",
      base_ref_name: "main",
      head_sha: "sha-old",
      merged: false,
    });
    mocks.postPullRequestReview.mockResolvedValue({
      review_id: 123,
      state: "APPROVED",
    });
    mocks.actions.updateIssueStatus.mockResolvedValue(undefined);
  });

  it("approves on GitHub before closing the ticket", async () => {
    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    await waitFor(() => {
      expect(mocks.postPullRequestReview).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
        {
          event: "approve",
          expected_head_sha: "sha-old",
        },
      );
    });
    expect(mocks.actions.updateIssueStatus).toHaveBeenCalledWith(
      "TASK-1",
      "closed",
    );
    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "Approved on GitHub — ticket closed",
    );
    expect(mocks.onBack).toHaveBeenCalledTimes(1);
  });

  it("shows stale banner on stale GitHub review and does not flip the ticket", async () => {
    mocks.getPullRequestDetail
      .mockResolvedValueOnce({
        number: 7,
        state: "OPEN",
        title: "Review PR",
        is_draft: false,
        head_ref_name: "feature",
        base_ref_name: "main",
        head_sha: "sha-old",
        merged: false,
      })
      .mockResolvedValueOnce({
        number: 7,
        state: "OPEN",
        title: "Review PR",
        is_draft: false,
        head_ref_name: "feature",
        base_ref_name: "main",
        head_sha: "sha-new",
        merged: false,
      });
    mocks.postPullRequestReview.mockRejectedValueOnce(
      new ApiError(409, "Conflict", { error: "stale subject" }),
    );

    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    expect(
      await screen.findByTestId("pr-review-stale-banner"),
    ).toHaveTextContent("This PR was updated after you opened it.");
    expect(mocks.actions.updateIssueStatus).not.toHaveBeenCalled();
    expect(mocks.onBack).not.toHaveBeenCalled();
    expect(mocks.getPullRequestDetail).toHaveBeenLastCalledWith(
      "WS",
      "octocat",
      "hello",
      7,
    );
    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "The PR changed since you loaded it — refreshed. Review again.",
      { type: "warning" },
    );
  });

  it("requires a comment before requesting changes", async () => {
    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    fireEvent.click(screen.getByRole("button", { name: /request changes/i }));

    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "Add a comment to request changes",
      { type: "warning" },
    );
    expect(mocks.postPullRequestReview).not.toHaveBeenCalled();
    expect(mocks.actions.updateIssueStatus).not.toHaveBeenCalled();
    expect(mocks.onBack).not.toHaveBeenCalled();
  });

  it("does not silently flip the ticket when the PR head can't be verified", async () => {
    // A real PR is present but its head sha never loads (detail fetch fails).
    // The decision must hard-fail rather than fall back to a local-only flip.
    mocks.getPullRequestDetail.mockRejectedValue(new Error("boom"));

    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalled();
    });

    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    await waitFor(() => {
      expect(mocks.actions.showToast).toHaveBeenCalledWith(
        "Couldn't verify the PR's current head — try again.",
        { type: "error" },
      );
    });
    expect(mocks.postPullRequestReview).not.toHaveBeenCalled();
    expect(mocks.actions.updateIssueStatus).not.toHaveBeenCalled();
    expect(mocks.onBack).not.toHaveBeenCalled();
  });

  it("fetches the head sha on demand when the initial load hasn't resolved", async () => {
    // Effect's fetch fails (headSha still null), but the click resolves the
    // sha on demand and posts a REAL review — never a local-only flip.
    mocks.getPullRequestDetail
      .mockRejectedValueOnce(new Error("slow"))
      .mockResolvedValueOnce({
        number: 7,
        state: "OPEN",
        title: "Review PR",
        is_draft: false,
        head_ref_name: "feature",
        base_ref_name: "main",
        head_sha: "sha-fresh",
        merged: false,
      });

    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledTimes(1);
    });

    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    await waitFor(() => {
      expect(mocks.postPullRequestReview).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
        {
          event: "approve",
          expected_head_sha: "sha-fresh",
        },
      );
    });
    expect(mocks.actions.updateIssueStatus).toHaveBeenCalledWith(
      "TASK-1",
      "closed",
    );
    expect(mocks.onBack).toHaveBeenCalledTimes(1);
  });
});

describe("resolveDiffAgentForIssue", () => {
  it("prefers the worker agent bound to the issue id", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-mode" });
    const worker = makeAgent({
      name: "local-coder",
      task_id: "LOCALMODE-1",
      mode: "ephemeral",
    });
    const agents = [worker, makeAgent({ name: "local-mode" })];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("local-coder");
  });

  it("prefers a linked agent with commits ahead over a clean assignee", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-planner" });
    const agents = [
      makeAgent({ name: "local-planner", task_id: "LOCALMODE-1", ahead: 0 }),
      makeAgent({
        name: "local-coder",
        task_id: "LOCALMODE-1",
        ahead: 12,
        mode: "ephemeral",
      }),
    ];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("local-coder");
  });

  it("collects all agents linked to an issue", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-planner" });
    const agents = [
      makeAgent({ name: "local-planner", task_id: "LOCALMODE-1" }),
      makeAgent({ name: "local-coder", task_id: "LOCALMODE-1" }),
    ];

    expect(
      agentsLinkedToIssue(issue, agents)
        .map((a) => a.name)
        .sort(),
    ).toEqual(["local-coder", "local-planner"]);
  });

  it("falls back to a direct agent assignee when no worker is bound", () => {
    const issue = makeIssue({ id: "TASK-9", assignee: "review-bot" });
    const agents = [makeAgent({ name: "review-bot" })];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("review-bot");
  });

  it("returns undefined when assignee is not an agent and no worker exists", () => {
    const issue = makeIssue({ id: "TASK-9", assignee: "local-mode" });

    expect(resolveDiffAgentForIssue(issue, [])).toBeUndefined();
  });
});
