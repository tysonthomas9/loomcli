// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue, LoomAgentStatus } from "@/types";

import {
  PRReviewWorkspace,
  agentsLinkedToIssue,
  resolveDiffAgentForIssue,
} from "../PRReviewWorkspace";

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  onBack: vi.fn(),
  onLinkedTicket: vi.fn(),
  createIssue: vi.fn(),
  updateIssue: vi.fn(),
  startAgent: vi.fn(),
  getPullRequestDetail: vi.fn(),
  diffRefreshKeys: [] as Array<number | undefined>,
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
  createIssue: mocks.createIssue,
  updateIssue: mocks.updateIssue,
}));

vi.mock("@/api/workspace/prReview", () => ({
  getPullRequestDetail: mocks.getPullRequestDetail,
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

vi.mock("@/components/PRDiscussionPanel", () => ({
  PRDiscussionPanel: ({
    onStaleSubject,
  }: {
    onStaleSubject?: () => void | Promise<void>;
  }) => (
    <div data-testid="pr-discussion-panel">
      <button
        type="button"
        data-testid="pr-discussion-stale-subject"
        onClick={() => void onStaleSubject?.()}
      >
        Stale subject
      </button>
    </div>
  ),
}));

vi.mock("@/components/IssueDetailPanel", () => ({
  PRCompareDiffPane: ({ refreshKey }: { refreshKey?: number }) => {
    mocks.diffRefreshKeys.push(refreshKey);
    return <div data-testid="pr-compare-diff-pane" />;
  },
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
    source_repo: "hello",
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
    issue: Issue | null;
    pullRequest: GitPullRequest | null;
    onBack: () => void;
    onLinkedTicket: (issueId: string) => void;
  }> = {},
) {
  const issue =
    props.issue === null ? undefined : (props.issue ?? makePullRequestIssue());
  const pullRequest =
    props.pullRequest === null
      ? undefined
      : (props.pullRequest ?? makePullRequest());
  return render(
    <PRReviewWorkspace
      {...(issue ? { issue } : {})}
      {...(pullRequest ? { pullRequest } : {})}
      onBack={props.onBack ?? mocks.onBack}
      onLinkedTicket={props.onLinkedTicket ?? mocks.onLinkedTicket}
    />,
  );
}

describe("PRReviewWorkspace", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.data.agents = [];
    mocks.data.issues = [];
    mocks.diffRefreshKeys.length = 0;
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
    mocks.createIssue.mockResolvedValue({
      issue: makeIssue({ id: "TASK-99" }),
      softDuplicate: false,
    });
    mocks.updateIssue.mockResolvedValue(makeIssue({ id: "TASK-99" }));
    mocks.actions.updateIssueStatus.mockResolvedValue(undefined);
  });

  it("toggles the PR discussion panel", async () => {
    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    fireEvent.click(screen.getByTestId("pr-discuss-button"));

    expect(screen.getByTestId("pr-discussion-panel")).toBeInTheDocument();
    expect(mocks.navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("pr-discuss-button"));

    expect(screen.queryByTestId("pr-discussion-panel")).not.toBeInTheDocument();
  });

  it("exposes Discuss for an issue-only PR URL", async () => {
    renderWorkspace({
      issue: makePullRequestIssue(),
      pullRequest: null,
    });

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });
    fireEvent.click(screen.getByTestId("pr-discuss-button"));
    expect(screen.getByTestId("pr-discussion-panel")).toBeInTheDocument();
  });

  it("shows the stale banner and refreshes the PR after reviewer ensure reports stale", async () => {
    renderWorkspace();

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledTimes(1);
    });
    fireEvent.click(screen.getByTestId("pr-discuss-button"));
    fireEvent.click(screen.getByTestId("pr-discussion-stale-subject"));

    expect(
      await screen.findByTestId("pr-review-stale-banner"),
    ).toHaveTextContent("This PR was updated after you opened it.");
    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledTimes(2);
      expect(mocks.diffRefreshKeys).toContain(1);
    });
    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "The PR changed since you loaded it — refreshed. Continue reviewing the latest head.",
      { type: "warning" },
    );
  });

  it("does not render decision controls for a ticketless pull request", async () => {
    renderWorkspace({ issue: null });

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    expect(screen.queryByTestId("pr-review-comment")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /request changes/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^approve$/i }),
    ).not.toBeInTheDocument();
  });

  it("renders the issue decision bar for an issue-backed review", async () => {
    renderWorkspace({ issue: makePullRequestIssue() });

    await waitFor(() => {
      expect(mocks.getPullRequestDetail).toHaveBeenCalledWith(
        "WS",
        "octocat",
        "hello",
        7,
      );
    });

    expect(screen.queryByTestId("pr-review-comment")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /request changes/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /approve/i }),
    ).toBeInTheDocument();
  });

  it("creates and links a ticket for a ticketless pull request", async () => {
    const onLinkedTicket = vi.fn();
    mocks.createIssue.mockResolvedValueOnce({
      issue: makeIssue({ id: "TASK-99" }),
      softDuplicate: false,
    });

    renderWorkspace({ issue: null, onLinkedTicket });

    fireEvent.click(screen.getByTestId("pr-create-ticket"));

    await waitFor(() => {
      expect(mocks.createIssue).toHaveBeenCalledWith(
        "WS",
        expect.objectContaining({
          title: "Review PR",
          external_ref: "https://github.com/octocat/hello/pull/7",
          source_repo: "hello",
          issue_type: "task",
          priority: 3,
        }),
      );
    });
    expect(mocks.updateIssue).toHaveBeenCalledWith("WS", "TASK-99", {
      status: "review",
    });
    // Must refetch before linking so the new issue is loaded when the parent
    // navigates to ?review=<newId> (else the review gate misses and bounces).
    expect(mocks.actions.refetch).toHaveBeenCalled();
    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "Created TASK-99 for this pull request",
    );
    expect(onLinkedTicket).toHaveBeenCalledWith("TASK-99");
  });

  it("warns when the created ticket cannot move to Review", async () => {
    mocks.updateIssue.mockRejectedValueOnce(new Error("status update failed"));

    renderWorkspace({ issue: null });
    fireEvent.click(screen.getByTestId("pr-create-ticket"));

    await waitFor(() => {
      expect(mocks.actions.showToast).toHaveBeenCalledWith(
        "Ticket TASK-99 created, but moving it to Review failed — set it manually",
        { type: "warning" },
      );
    });
    expect(mocks.actions.showToast).toHaveBeenCalledWith(
      "Created TASK-99 for this pull request",
    );
    expect(mocks.onLinkedTicket).toHaveBeenCalledWith("TASK-99");
  });

  it("omits source_repo when the pull request has no workspace repo mapping", async () => {
    renderWorkspace({
      issue: null,
      pullRequest: makePullRequest({ source_repo: "" }),
    });

    fireEvent.click(screen.getByTestId("pr-create-ticket"));

    await waitFor(() => {
      expect(mocks.createIssue).toHaveBeenCalledTimes(1);
    });
    const request = mocks.createIssue.mock.calls[0]?.[1];
    expect(request).toEqual(
      expect.objectContaining({
        title: "Review PR",
        external_ref: "https://github.com/octocat/hello/pull/7",
        issue_type: "task",
        priority: 3,
      }),
    );
    expect(request).not.toHaveProperty("source_repo");
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
