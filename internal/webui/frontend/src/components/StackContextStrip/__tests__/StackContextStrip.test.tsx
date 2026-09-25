/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import {
  isExternalMember,
  resolveStackContext,
} from "@/hooks/workspace/useStackContext";

import { StackContextStrip } from "../StackContextStrip";

const mocks = vi.hoisted(() => ({
  getDeliveryGroupByPr: vi.fn(),
  fetchPullRequests: vi.fn(),
}));

vi.mock("@/api/workspace/deliveryGroups", () => ({
  getDeliveryGroupByPr: mocks.getDeliveryGroupByPr,
}));

vi.mock("@/api/workspace/pullRequests", () => ({
  fetchPullRequests: mocks.fetchPullRequests,
}));

vi.mock("@/components/StackedPRWorkspace/ReadinessBadge", () => ({
  ReadinessBadge: () => <span data-testid="readiness-badge">Ready</span>,
}));

function makeGroup(
  overrides: Partial<DeliveryGroupView> = {},
): DeliveryGroupView {
  return {
    workspace_key: "WS",
    id: "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
    title: "Cross-repo delivery",
    state: "active",
    revision: 1,
    members: [
      {
        pr_key: "github:octocat/hello#1",
        repo_name: "hello",
        pr_number: 1,
        source: "loom_task",
        task_id: "TASK-1",
        added_at: "2026-09-24T00:00:00Z",
        readiness: {
          pr_key: "github:octocat/hello#1",
          freshness: "fresh",
          age_seconds: 0,
          current_verdict: "ready",
          current_reasons: [],
          snapshot: {
            pr_key: "github:octocat/hello#1",
            head_sha: "a",
            head_ref: "feat-a",
            base_ref: "main",
            base_sha: "b",
            observed_at: "2026-09-24T00:00:00Z",
            facts: {} as never,
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        },
      },
      {
        pr_key: "github:octocat/hello#2",
        repo_name: "hello",
        pr_number: 2,
        source: "manual",
        added_at: "2026-09-24T00:00:00Z",
        readiness: {
          pr_key: "github:octocat/hello#2",
          freshness: "fresh",
          age_seconds: 0,
          current_verdict: "waiting",
          current_reasons: [],
          snapshot: {
            pr_key: "github:octocat/hello#2",
            head_sha: "c",
            head_ref: "feat-b",
            base_ref: "feat-a",
            base_sha: "d",
            observed_at: "2026-09-24T00:00:00Z",
            facts: {} as never,
            verdict: "waiting",
            reasons: [],
            fingerprint: "fp2",
          },
        },
      },
    ],
    last_op_id: "op",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T00:00:00Z",
    ...overrides,
  };
}

function renderStrip(prKey = "github:octocat/hello#2") {
  return render(
    <MemoryRouter>
      <StackContextStrip
        workspaceId="WS"
        prKey={prKey}
        onBack={() => undefined}
      />
    </MemoryRouter>,
  );
}

describe("StackContextStrip", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders grouped strip with prev/next deep links and ancestry cue", async () => {
    mocks.getDeliveryGroupByPr.mockResolvedValue({ group: makeGroup() });
    renderStrip();

    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "grouped",
    );
    expect(screen.getByText("Cross-repo delivery")).toBeInTheDocument();
    expect(screen.getByText("2 of 2")).toBeInTheDocument();
    expect(screen.getByText("based on prev branch")).toBeInTheDocument();
    expect(screen.getByTestId("stack-context-external")).toBeInTheDocument();

    const prev = screen.getByRole("link", { name: /Prev/i });
    const openStack = screen.getByRole("link", { name: /Open stack/i });
    expect(prev.getAttribute("href")).toContain("review-pr=");
    expect(openStack.getAttribute("href")).toContain(
      "group=dg_01HABCDEFGHJKLMNPQRSTUVWXY",
    );
    expect(openStack.getAttribute("href")).toContain(
      "pr=github%3Aoctocat%2Fhello%232",
    );
    expect(mocks.fetchPullRequests).not.toHaveBeenCalled();
  });

  it("labels verified standalone when continuation is complete", async () => {
    const { ApiError } = await import("@/api/common");
    mocks.getDeliveryGroupByPr.mockRejectedValue(
      new ApiError(404, "Not Found"),
    );
    mocks.fetchPullRequests.mockResolvedValue({
      pullRequests: [],
      warnings: [],
      deliveryGroups: [],
      deliveryGroupsCount: 0,
      deliveryGroupsHasMore: false,
      standaloneContinuation: { repos: [], has_more: false, complete: true },
      githubViewer: { status: "unavailable", source: "none" },
    });
    renderStrip("github:octocat/hello#9");
    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "standalone",
    );
    expect(screen.getByText(/Standalone/)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Open in Pull Requests/i }),
    ).toHaveAttribute("href", "/ws/WS/prs");
  });

  it("labels membership unverified when continuation is incomplete", async () => {
    const { ApiError } = await import("@/api/common");
    mocks.getDeliveryGroupByPr.mockRejectedValue(
      new ApiError(404, "Not Found"),
    );
    mocks.fetchPullRequests.mockResolvedValue({
      pullRequests: [],
      warnings: [],
      deliveryGroups: [{ id: "should-not-use" }],
      deliveryGroupsCount: 1,
      deliveryGroupsHasMore: true,
      standaloneContinuation: { repos: [], has_more: false, complete: false },
      githubViewer: { status: "unavailable", source: "none" },
    });
    renderStrip("github:octocat/hello#8");
    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "unverified",
    );
    expect(screen.getByText("Membership unverified")).toBeInTheDocument();
    expect(screen.queryByText("Standalone")).not.toBeInTheDocument();
  });

  it("shows context unavailable on 503 without blocking", async () => {
    const { ApiError } = await import("@/api/common");
    mocks.getDeliveryGroupByPr.mockRejectedValue(
      new ApiError(503, "Unavailable"),
    );
    renderStrip();
    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "unavailable",
    );
    expect(screen.getByText("Context unavailable")).toBeInTheDocument();
  });

  it("treats a 200 group that omits the requested PR as unavailable", async () => {
    mocks.getDeliveryGroupByPr.mockResolvedValue({
      group: makeGroup({
        members: [
          {
            pr_key: "github:octocat/hello#1",
            repo_name: "hello",
            pr_number: 1,
            source: "loom_task",
            task_id: "TASK-1",
            added_at: "2026-09-24T00:00:00Z",
          },
        ],
      }),
    });
    renderStrip("github:octocat/hello#99");
    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "unavailable",
    );
    expect(screen.getByText("Context unavailable")).toBeInTheDocument();
    expect(screen.queryByText("1 of 1")).not.toBeInTheDocument();
  });

  it("does not label lineage_adopt with task_id as External", async () => {
    mocks.getDeliveryGroupByPr.mockResolvedValue({
      group: makeGroup({
        members: [
          {
            pr_key: "github:octocat/hello#2",
            repo_name: "hello",
            pr_number: 2,
            source: "lineage_adopt",
            task_id: "TASK-2",
            added_at: "2026-09-24T00:00:00Z",
          },
        ],
      }),
    });
    renderStrip("github:octocat/hello#2");
    expect(await screen.findByTestId("stack-context-strip")).toHaveAttribute(
      "data-state",
      "grouped",
    );
    expect(
      screen.queryByTestId("stack-context-external"),
    ).not.toBeInTheDocument();
  });
});

describe("resolveStackContext", () => {
  it("does not treat delivery group list payloads as membership", async () => {
    const { ApiError } = await import("@/api/common");
    mocks.getDeliveryGroupByPr.mockRejectedValue(
      new ApiError(404, "Not Found"),
    );
    mocks.fetchPullRequests.mockResolvedValue({
      pullRequests: [],
      warnings: [],
      deliveryGroups: [
        makeGroup({
          members: [
            {
              pr_key: "github:octocat/hello#2",
              repo_name: "hello",
              pr_number: 2,
              source: "manual",
              added_at: "2026-09-24T00:00:00Z",
            },
          ],
        }),
      ],
      deliveryGroupsCount: 1,
      deliveryGroupsHasMore: false,
      standaloneContinuation: { repos: [], has_more: false, complete: false },
      githubViewer: { status: "unavailable", source: "none" },
    });
    const state = await resolveStackContext("WS", "github:octocat/hello#2");
    expect(state.kind).toBe("unverified");
  });

  it("refuses arbitrary member fallback when by-pr members omit the PR", async () => {
    mocks.getDeliveryGroupByPr.mockResolvedValue({
      group: makeGroup(),
    });
    const state = await resolveStackContext("WS", "github:octocat/other#9");
    expect(state).toEqual({ kind: "unavailable" });
  });

  it("matches legacy owner/repo#N keys against canonical member keys", async () => {
    mocks.getDeliveryGroupByPr.mockResolvedValue({
      group: makeGroup(),
    });
    const state = await resolveStackContext("WS", "octocat/hello#2");
    expect(state.kind).toBe("grouped");
    if (state.kind === "grouped") {
      expect(state.index).toBe(1);
    }
  });
});

describe("isExternalMember", () => {
  it("treats lineage_adopt with task_id as internal", () => {
    expect(
      isExternalMember({ source: "lineage_adopt", task_id: "TASK-1" }),
    ).toBe(false);
  });

  it("treats explicit external sources and missing task_id as external", () => {
    expect(isExternalMember({ source: "manual", task_id: "TASK-1" })).toBe(
      true,
    );
    expect(
      isExternalMember({
        source: "native_stack_suggestion",
        task_id: "TASK-1",
      }),
    ).toBe(true);
    expect(
      isExternalMember({ source: "identity_heal", task_id: "TASK-1" }),
    ).toBe(true);
    expect(isExternalMember({ source: "loom_task" })).toBe(true);
    expect(isExternalMember({ source: "loom_task", task_id: "TASK-1" })).toBe(
      false,
    );
  });
});
