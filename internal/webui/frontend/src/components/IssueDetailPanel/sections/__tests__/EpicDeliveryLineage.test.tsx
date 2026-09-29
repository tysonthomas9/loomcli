/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { MemoryRouter } from "react-router-dom";

import { ApiError } from "@/api/common";
import type {
  DeliveryGroupMemberView,
  DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";

import {
  EpicDeliveryLineage,
  EpicDeliveryLineageView,
} from "../EpicDeliveryLineage";

const listDeliveryGroups = vi.fn();
const fetchPullRequestReadiness = vi.fn();

vi.mock("@/hooks/workspace/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "STACKED-PRS" }),
}));

vi.mock("@/api/workspace/deliveryGroups", async () => {
  const actual = await vi.importActual<
    typeof import("@/api/workspace/deliveryGroups")
  >("@/api/workspace/deliveryGroups");
  return {
    ...actual,
    listDeliveryGroups: (...args: unknown[]) => listDeliveryGroups(...args),
  };
});

vi.mock("@/api/workspace/pullRequests", async () => {
  const actual = await vi.importActual<
    typeof import("@/api/workspace/pullRequests")
  >("@/api/workspace/pullRequests");
  return {
    ...actual,
    fetchPullRequestReadiness: (...args: unknown[]) =>
      fetchPullRequestReadiness(...args),
  };
});

function readiness(
  prKey: string,
  overrides: Partial<PullRequestReadinessView> = {},
  refs?: { head: string; base: string },
): PullRequestReadinessView {
  return {
    pr_key: prKey,
    freshness: "fresh",
    age_seconds: 5,
    current_verdict: "ready",
    current_reasons: [],
    ...(refs
      ? {
          snapshot: {
            pr_key: prKey,
            head_sha: "h",
            head_ref: refs.head,
            base_ref: refs.base,
            base_sha: "b",
            observed_at: "2026-09-25T00:00:00Z",
            facts: {} as never,
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        }
      : {}),
    ...overrides,
  };
}

function member(
  repo: string,
  n: number,
  overrides: Partial<DeliveryGroupMemberView> = {},
): DeliveryGroupMemberView {
  return {
    pr_key: `github:${repo}#${n}`,
    repo_name: repo,
    pr_number: n,
    source: "loom_task",
    task_id: `STACKED-PRS-${n}`,
    added_at: "2026-09-24T00:00:00Z",
    readiness: readiness(`github:${repo}#${n}`),
    ...overrides,
  };
}

function group(
  id: string,
  members: DeliveryGroupMemberView[],
  overrides: Partial<DeliveryGroupView> = {},
): DeliveryGroupView {
  return {
    workspace_key: "STACKED-PRS",
    id,
    title: `Group ${id}`,
    epic_id: "STACKED-PRS-2",
    state: "active",
    revision: 1,
    members,
    last_op_id: "op",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T00:00:00Z",
    ...overrides,
  };
}

function renderView(
  props: Partial<ComponentProps<typeof EpicDeliveryLineageView>> = {},
) {
  return render(
    <MemoryRouter>
      <EpicDeliveryLineageView
        workspaceId="STACKED-PRS"
        epicId="STACKED-PRS-2"
        groups={[]}
        {...props}
      />
    </MemoryRouter>,
  );
}

function hrefParams(el: HTMLElement): URLSearchParams {
  return new URL(el.getAttribute("href") ?? "", "http://x").searchParams;
}

describe("EpicDeliveryLineageView", () => {
  it("renders an honest empty state with an epic link into /prs", () => {
    renderView();
    const section = screen.getByRole("region", { name: "Delivery & lineage" });
    expect(
      within(section).getByText("No delivery groups linked to this epic."),
    ).toBeInTheDocument();
    const link = within(section).getByRole("link", {
      name: "Open this epic in Pull Requests",
    });
    expect(link.getAttribute("href")).toMatch(/^\/ws\/STACKED-PRS\/prs\?/);
    expect(hrefParams(link).get("epic")).toBe("STACKED-PRS-2");
  });

  it("renders a skeleton while loading", () => {
    renderView({ loading: true });
    expect(screen.getByTestId("epic-delivery-loading")).toHaveAttribute(
      "aria-busy",
      "true",
    );
  });

  it("renders Delivery groups unavailable for a 503", () => {
    renderView({ error: { kind: "unavailable", message: "fleetdb down" } });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Delivery groups unavailable.",
    );
  });

  it("renders a single group as a numbered delivery path", () => {
    renderView({
      groups: [
        group("dg_1", [member("acme/loomcli", 1), member("acme/loomcli", 2)]),
      ],
    });
    const path = screen.getByRole("list", {
      name: "Delivery order for Group dg_1",
    });
    const steps = within(path).getAllByRole("listitem");
    expect(steps).toHaveLength(2);
    expect(steps[0]).toHaveTextContent("Delivery step 1 of 2");
    expect(steps[1]).toHaveTextContent("Delivery step 2 of 2");
    expect(screen.queryByTestId("epic-delivery-ambiguous")).toBeNull();
  });

  it("lists multiple groups separately and never fuses their order", () => {
    renderView({
      groups: [
        group("dg_a", [member("acme/loomcli", 1)]),
        group("dg_b", [member("acme/loomcli", 2)]),
      ],
    });
    expect(screen.getByTestId("epic-delivery-ambiguous")).toHaveTextContent(
      "2 delivery groups",
    );
    const a = screen.getByTestId("epic-delivery-group-dg_a");
    const b = screen.getByTestId("epic-delivery-group-dg_b");
    expect(within(a).getAllByRole("listitem")).toHaveLength(1);
    expect(within(b).getAllByRole("listitem")).toHaveLength(1);
    expect(within(b).getByText(/Delivery step 1 of 1/)).toBeInTheDocument();
  });

  it("labels members without a Loom task as External", () => {
    const external = member("acme/loomcli", 7, { source: "manual" });
    delete (external as { task_id?: string }).task_id;
    renderView({
      groups: [group("dg_1", [member("acme/loomcli", 1), external])],
    });
    expect(
      within(screen.getByTestId("epic-delivery-step-acme/loomcli#7")).getByText(
        "External",
      ),
    ).toBeInTheDocument();
    expect(
      within(
        screen.getByTestId("epic-delivery-step-acme/loomcli#1"),
      ).queryByText("External"),
    ).toBeNull();
  });

  it("marks a cross-repository delivery dependency between repo neighbors", () => {
    renderView({
      groups: [
        group("dg_1", [
          member("acme/fleet-db", 1),
          member("acme/loomcli", 2),
          member("acme/loomcli", 3),
        ]),
      ],
    });
    const markers = screen.getAllByTestId("epic-delivery-cross-repo");
    expect(markers).toHaveLength(1);
    expect(markers[0]).toHaveTextContent(
      "Cross-repository delivery dependency",
    );
    expect(markers[0]).toHaveTextContent("acme/fleet-db → acme/loomcli");
    expect(
      screen.getByTestId("epic-delivery-step-acme/loomcli#2"),
    ).toContainElement(markers[0]!);
  });

  it("adds a same-repo branch-base footnote without renumbering delivery", () => {
    const child = member("acme/loomcli", 20, {
      readiness: readiness(
        "github:acme/loomcli#20",
        {},
        { head: "feat/b", base: "feat/a" },
      ),
    });
    const base = member("acme/loomcli", 10, {
      readiness: readiness(
        "github:acme/loomcli#10",
        {},
        { head: "feat/a", base: "main" },
      ),
    });
    // Same branch name in another repo must not link.
    const other = member("acme/fleet-db", 30, {
      readiness: readiness(
        "github:acme/fleet-db#30",
        {},
        { head: "feat/c", base: "feat/a" },
      ),
    });
    renderView({ groups: [group("dg_1", [child, base, other])] });

    const footnotes = screen.getAllByTestId("epic-delivery-branch-base");
    expect(footnotes).toHaveLength(1);
    expect(footnotes[0]).toHaveTextContent("Same-repository branch base");
    expect(footnotes[0]).toHaveTextContent(
      "Step 1 (#20) is based on step 2 (#10) branch feat/a in acme/loomcli",
    );
    // Delivery order stays as stored: #20 is still step 1.
    expect(
      screen.getByTestId("epic-delivery-step-acme/loomcli#20"),
    ).toHaveTextContent("Delivery step 1 of 3");
  });

  it("never shows stale readiness as current Ready", () => {
    renderView({
      groups: [
        group("dg_1", [
          member("acme/loomcli", 1, {
            readiness: readiness(
              "github:acme/loomcli#1",
              { freshness: "stale", current_verdict: "unknown" },
              { head: "feat/a", base: "main" },
            ),
          }),
        ]),
      ],
    });
    const step = screen.getByTestId("epic-delivery-step-acme/loomcli#1");
    const badge = within(step).getByTestId("readiness-badge");
    expect(badge).toHaveAttribute("data-key", "stale");
    expect(badge).not.toHaveTextContent(/^Ready$/);
    // Group header summarizes worst readiness, also not Ready.
    const header = screen
      .getByTestId("epic-delivery-group-dg_1")
      .querySelector("header")!;
    expect(within(header).getByTestId("readiness-badge")).toHaveAttribute(
      "data-key",
      "stale",
    );
    expect(step).toHaveTextContent("feat/a → main (last observed)");
  });

  it("deep-links each step and the group into /prs with group and pr", () => {
    renderView({
      groups: [
        group("dg_1", [member("acme/loomcli", 1), member("acme/loomcli", 2)]),
      ],
    });
    const stepLink = screen.getByTestId("epic-delivery-link-acme/loomcli#2");
    expect(stepLink.getAttribute("href")).toMatch(/^\/ws\/STACKED-PRS\/prs\?/);
    expect(hrefParams(stepLink).get("group")).toBe("dg_1");
    expect(hrefParams(stepLink).get("pr")).toBe("github:acme/loomcli#2");

    const open = screen.getByRole("link", { name: "Open in Pull Requests" });
    expect(hrefParams(open).get("group")).toBe("dg_1");
    expect(hrefParams(open).get("pr")).toBe("github:acme/loomcli#1");
  });

  it("offers Load more when the server reports more pages", () => {
    const onLoadMore = vi.fn();
    renderView({
      groups: [group("dg_1", [member("acme/loomcli", 1)])],
      hasMore: true,
      onLoadMore,
    });
    expect(screen.getByText(/Showing 1 group · more available/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it("shows an inconsistent banner but still renders committed members", () => {
    renderView({
      groups: [
        group("dg_1", [member("acme/loomcli", 1)], {
          inconsistent: true,
          integrity: "unverified",
        }),
      ],
    });
    const card = screen.getByTestId("epic-delivery-group-dg_1");
    expect(within(card).getByRole("status")).toHaveTextContent("inconsistent");
    expect(within(card).getByText(/Integrity unverified/)).toBeInTheDocument();
    expect(within(card).getAllByRole("listitem")).toHaveLength(1);
  });

  it("uses plain anchors when rendered outside the router", () => {
    render(
      <EpicDeliveryLineageView
        workspaceId="WS"
        epicId="E-1"
        groups={[group("dg_1", [member("acme/loomcli", 1)])]}
      />,
    );
    const link = screen.getByTestId("epic-delivery-link-acme/loomcli#1");
    expect(link.tagName).toBe("A");
    expect(hrefParams(link).get("group")).toBe("dg_1");
  });
});

describe("EpicDeliveryLineage (data-backed)", () => {
  beforeEach(() => {
    listDeliveryGroups.mockReset();
    fetchPullRequestReadiness.mockReset();
  });

  it("queries active groups for the epic and pages with the cursor", async () => {
    listDeliveryGroups
      .mockResolvedValueOnce({
        delivery_groups: [group("dg_1", [member("acme/loomcli", 1)])],
        count: 1,
        has_more: true,
        next_cursor: "c1",
      })
      .mockResolvedValueOnce({
        delivery_groups: [group("dg_2", [member("acme/loomcli", 2)])],
        count: 1,
        has_more: false,
      });

    render(
      <MemoryRouter>
        <EpicDeliveryLineage epicId="STACKED-PRS-2" />
      </MemoryRouter>,
    );

    expect(
      await screen.findByTestId("epic-delivery-group-dg_1"),
    ).toBeInTheDocument();
    expect(listDeliveryGroups).toHaveBeenCalledWith("STACKED-PRS", {
      epicId: "STACKED-PRS-2",
      state: "active",
      limit: 20,
    });

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(
      await screen.findByTestId("epic-delivery-group-dg_2"),
    ).toBeInTheDocument();
    expect(listDeliveryGroups).toHaveBeenLastCalledWith("STACKED-PRS", {
      epicId: "STACKED-PRS-2",
      state: "active",
      limit: 20,
      cursor: "c1",
    });
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("reads readiness only for undecorated members", async () => {
    const bare = member("acme/loomcli", 2);
    delete (bare as { readiness?: unknown }).readiness;
    listDeliveryGroups.mockResolvedValueOnce({
      delivery_groups: [group("dg_1", [member("acme/loomcli", 1), bare])],
      count: 1,
      has_more: false,
    });
    fetchPullRequestReadiness.mockResolvedValueOnce({
      server_now: "2026-09-25T00:00:00Z",
      fresh_for_s: 60,
      stale_after_s: 300,
      pull_requests: [
        readiness("github:acme/loomcli#2", {
          freshness: "stale",
          current_verdict: "unknown",
        }),
      ],
      repo_errors: [],
    });

    render(
      <MemoryRouter>
        <EpicDeliveryLineage epicId="STACKED-PRS-2" />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(fetchPullRequestReadiness).toHaveBeenCalledWith("STACKED-PRS", [
        "github:acme/loomcli#2",
      ]),
    );
    const step = screen.getByTestId("epic-delivery-step-acme/loomcli#2");
    await waitFor(() =>
      expect(within(step).getByTestId("readiness-badge")).toHaveAttribute(
        "data-key",
        "stale",
      ),
    );
  });

  it("shows Delivery groups unavailable when the facade returns 503", async () => {
    listDeliveryGroups.mockRejectedValueOnce(
      new ApiError(503, "Service Unavailable", {
        code: "delivery_groups_unavailable",
        message: "delivery groups unavailable",
      }),
    );

    render(
      <MemoryRouter>
        <EpicDeliveryLineage epicId="STACKED-PRS-2" />
      </MemoryRouter>,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Delivery groups unavailable.",
    );
  });
});
