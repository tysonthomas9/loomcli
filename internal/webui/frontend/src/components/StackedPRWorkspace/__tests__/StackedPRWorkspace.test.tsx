/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { MemoryRouter } from "react-router-dom";

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import { StackedPRWorkspace } from "../StackedPRWorkspace";

vi.mock("@/hooks/workspace/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "STACKED-PRS" }),
}));

vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => ({ user: { id: "1", name: "tyson", email: "t@example.com" } }),
}));

vi.mock("@/hooks", async () => {
  const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
  return {
    ...actual,
    useRegisterEscapeLayer: vi.fn(),
  };
});

vi.mock("@/hooks/workspace/useDeliveryGroupPreview", () => ({
  useDeliveryGroupPreview: () => ({
    preview: null,
    loading: false,
    error: null,
    refresh: vi.fn(),
    clear: vi.fn(),
  }),
}));

vi.mock("@/hooks/workspace/useDeliveryGroupMembers", () => ({
  useDeliveryGroupMembers: () => ({
    saving: false,
    lastError: null,
    setMembers: vi.fn().mockResolvedValue({ group: null, error: null }),
    clearError: vi.fn(),
  }),
}));

vi.mock("@/hooks/api", () => ({
  fetchPullRequestReadiness: vi.fn().mockResolvedValue({
    server_now: "2026-09-25T00:00:00Z",
    fresh_for_s: 60,
    stale_after_s: 300,
    pull_requests: [],
    repo_errors: [],
  }),
}));

function pr(
  n: number,
  overrides: Partial<GitPullRequest> = {},
): GitPullRequest {
  return {
    number: n,
    pr_key: `github:acme/loomcli#${n}`,
    title: `PR ${n}`,
    url: `https://github.com/acme/loomcli/pull/${n}`,
    state: "OPEN",
    is_draft: false,
    head_ref_name: `feat/${n}`,
    base_ref_name: "main",
    repo_name: "acme/loomcli",
    source_repo: "loomcli",
    author_login: "tyson",
    ...overrides,
  };
}

function group(): DeliveryGroupView {
  return {
    workspace_key: "STACKED-PRS",
    id: "dg_test1",
    title: "Cross-repo delivery",
    epic_id: "STACKED-PRS-13",
    state: "active",
    revision: 2,
    members: [
      {
        pr_key: "github:acme/fleet-db#1",
        repo_name: "acme/fleet-db",
        pr_number: 1,
        source: "manual",
        added_at: "2026-09-24T00:00:00Z",
        readiness: {
          pr_key: "github:acme/fleet-db#1",
          freshness: "fresh",
          age_seconds: 12,
          current_verdict: "ready",
          current_reasons: [],
        },
      },
      {
        pr_key: "github:acme/loomcli#2",
        repo_name: "acme/loomcli",
        pr_number: 2,
        source: "manual",
        added_at: "2026-09-24T01:00:00Z",
        readiness: {
          pr_key: "github:acme/loomcli#2",
          freshness: "stale",
          age_seconds: 900,
          current_verdict: "unknown",
          current_reasons: ["stale_observation"],
          snapshot: {
            pr_key: "github:acme/loomcli#2",
            head_sha: "abc",
            head_ref: "feat/2",
            base_ref: "main",
            base_sha: "def",
            observed_at: "2026-09-24T10:00:00Z",
            facts: {
              lifecycle: { status: "known", value: "open" },
              conflicts: { status: "known", value: "clean" },
              merge_state: { status: "known", value: "clean" },
              review: { status: "known", value: "approved" },
              required_checks: { status: "known", value: "passing" },
              required_check_counts: {
                passed: 1,
                pending: 0,
                failed: 0,
                total: 1,
              },
              optional_checks: { status: "known", value: "passing" },
              optional_check_counts: {
                passed: 0,
                pending: 0,
                failed: 0,
                total: 0,
              },
              queue: { status: "known", value: "none" },
            },
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        },
      },
    ],
    last_op_id: "op1",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T02:00:00Z",
  };
}

function renderWorkspace(
  overrides: Partial<ComponentProps<typeof StackedPRWorkspace>> = {},
) {
  const onOpenReview = vi.fn();
  render(
    <MemoryRouter>
      <StackedPRWorkspace
        issues={[]}
        pullRequests={[pr(3)]}
        deliveryGroups={[group()]}
        warnings={[]}
        loading={false}
        error={null}
        onOpenReview={onOpenReview}
        {...overrides}
      />
    </MemoryRouter>,
  );
  return { onOpenReview };
}

describe("StackedPRWorkspace", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders delivery groups and standalone sections", () => {
    renderWorkspace();
    expect(screen.getByTestId("stacked-pr-workspace")).toBeInTheDocument();
    expect(screen.getByText("Cross-repo delivery")).toBeInTheDocument();
    expect(screen.getByTestId("stacked-pr-path")).toBeInTheDocument();
    expect(screen.getByText("PR 3")).toBeInTheDocument();
  });

  it("never labels stale evidence Ready", () => {
    renderWorkspace();
    const badges = screen.getAllByTestId("readiness-badge");
    const labels = badges.map((b) => b.textContent ?? "");
    expect(labels.some((l) => /Stale/i.test(l))).toBe(true);
    expect(labels.filter((l) => l === "Ready").length).toBeGreaterThanOrEqual(
      1,
    );
    // Stale row must not present bare Ready
    const stale = badges.find((b) => b.getAttribute("data-key") === "stale");
    expect(stale?.textContent).not.toBe("Ready");
  });

  it("dims cross-repo members when a repo filter is selected", () => {
    renderWorkspace();
    const checkboxes = screen.getAllByRole("checkbox");
    const loomcliBox = checkboxes.find((el) =>
      el.parentElement?.textContent?.includes("acme/loomcli"),
    );
    expect(loomcliBox).toBeTruthy();
    fireEvent.click(loomcliBox!);
    const dimmed = document.querySelectorAll("[data-dimmed]");
    expect(dimmed.length).toBeGreaterThan(0);
  });

  it("exposes available diff stats in the selected PR summary", async () => {
    renderWorkspace({
      pullRequests: [pr(3, { additions: 12, deletions: 4, changed_files: 3 })],
    });
    fireEvent.click(screen.getByTestId("pr-row-acme/loomcli#3"));
    expect(
      await screen.findByTestId("selected-pr-changes-summary"),
    ).toHaveTextContent("3 files · +12 / −4");
  });

  it("shows visible focus styles for row, search, and primary controls", () => {
    renderWorkspace();
    const row = screen.getByTestId("pr-row-acme/loomcli#3");
    row.focus();
    expect(row).toHaveFocus();
    expect(row.className).toMatch(/row/);

    const search = screen.getByRole("searchbox", {
      name: /search pull requests/i,
    });
    search.focus();
    expect(search).toHaveFocus();
    expect(search.closest("label")?.className).toMatch(/search/);

    fireEvent.click(row);
    const openReview = screen.getByRole("button", { name: /Open review/i });
    openReview.focus();
    expect(openReview).toHaveFocus();
    expect(openReview.className).toMatch(/btnPrimary/);
  });

  it("opens review for a selected standalone PR", async () => {
    const { onOpenReview } = renderWorkspace();
    fireEvent.click(screen.getByTestId("pr-row-acme/loomcli#3"));
    const openBtn = await screen.findByRole("button", { name: /Open review/i });
    fireEvent.click(openBtn);
    await waitFor(() => {
      expect(onOpenReview).toHaveBeenCalled();
    });
    expect(
      onOpenReview.mock.calls.some(
        (c) =>
          c[0]?.reviewPr === "acme/loomcli#3" ||
          c[0]?.reviewPr?.includes("loomcli#3"),
      ),
    ).toBe(true);
  });

  it("shows membership incomplete banner when continuation is incomplete", () => {
    renderWorkspace({
      standaloneContinuation: {
        repos: [],
        has_more: false,
        complete: false,
      },
    });
    expect(
      screen.getByText(/membership is incomplete or unverified/i),
    ).toBeInTheDocument();
  });

  it("toggles history mode", () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole("button", { name: /^History$/i }));
    expect(screen.getByTestId("stacked-pr-history")).toBeInTheDocument();
  });
});
