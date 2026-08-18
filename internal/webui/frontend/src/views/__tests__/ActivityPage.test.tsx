/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@testing-library/jest-dom";
import type { MutationPayload } from "@/types/workspace";
import type { AuditEvent, AuditPage } from "@/types/activity";
import { ActivityPage } from "../ActivityPage";

const mocks = vi.hoisted(() => ({
  fetchAuditEvents: vi.fn(),
  liveHandler: undefined as ((mutation: MutationPayload) => void) | undefined,
}));

vi.mock("@/api/workspace", () => ({
  fetchAuditEvents: mocks.fetchAuditEvents,
}));

vi.mock("@/hooks/common/useEventProvider", () => ({
  useEventSubscription: (callback: (mutation: MutationPayload) => void) => {
    mocks.liveHandler = callback;
  },
}));

vi.mock("@/hooks/workspace/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({
    workspaceId: "default",
    agents: [{ name: "api-architect-1" }],
  }),
}));

function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    cursor: "100-0",
    timestamp: "2026-08-14T18:00:00Z",
    actor: "api-architect-1",
    action: "issue.claim",
    entity_type: "issue",
    entity_id: "TEAMBACKEND-1",
    details: {},
    ...overrides,
  };
}

function page(events: AuditEvent[], nextCursor = ""): AuditPage {
  return { events, next_cursor: nextCursor };
}

describe("ActivityPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.liveHandler = undefined;
  });

  it("shows a loading state while history is requested", () => {
    mocks.fetchAuditEvents.mockReturnValue(new Promise(() => {}));

    render(<ActivityPage />);

    expect(
      screen.getByRole("status", { name: "Loading activity" }),
    ).toBeInTheDocument();
  });

  it("renders history newest first and accents matching agent actors", async () => {
    mocks.fetchAuditEvents.mockResolvedValue(
      page([
        event({ cursor: "100-0", timestamp: "2026-08-14T17:00:00Z" }),
        event({
          cursor: "101-0",
          timestamp: "2026-08-14T18:00:00Z",
          actor: "operator",
          action: "issue.close",
        }),
      ]),
    );

    render(<ActivityPage />);

    const rows = await screen.findAllByRole("listitem");
    expect(rows[0]).toHaveAccessibleName("operator closed TEAMBACKEND-1");
    expect(rows[1]).toHaveAccessibleName(
      "api-architect-1 claimed TEAMBACKEND-1",
    );
    expect(within(rows[0]).getByText("operator")).toHaveAttribute(
      "data-actor-kind",
      "operator",
    );
    expect(within(rows[1]).getByText("api-architect-1")).toHaveAttribute(
      "data-actor-kind",
      "agent",
    );
  });

  it("maps actor and entity filters to history params and filters live events", async () => {
    const alice = event({ actor: "api-architect-1", entity_id: "TEAM-1" });
    const bob = event({
      cursor: "101-0",
      actor: "bob",
      entity_id: "TEAM-2",
    });
    mocks.fetchAuditEvents
      .mockResolvedValueOnce(page([alice, bob]))
      .mockResolvedValue(page([alice]));

    render(<ActivityPage />);
    await screen.findAllByRole("listitem");

    await act(async () => {
      fireEvent.change(screen.getByLabelText("Filter by actor"), {
        target: { value: "api-architect-1" },
      });
      await Promise.resolve();
    });
    await act(async () => {
      fireEvent.change(screen.getByLabelText("Filter by issue"), {
        target: { value: "TEAM-1" },
      });
      await Promise.resolve();
    });

    await vi.waitFor(() => {
      expect(mocks.fetchAuditEvents).toHaveBeenLastCalledWith("default", {
        limit: 50,
        actor: "api-architect-1",
        entity: "TEAM-1",
      });
    });
    await screen.findByRole("listitem", {
      name: "api-architect-1 claimed TEAM-1",
    });

    act(() => {
      mocks.liveHandler?.({
        type: "status",
        cursor: "102-0",
        action: "issue.close",
        entity_type: "issue",
        entity_id: "TEAM-2",
        actor: "bob",
        timestamp: "2026-08-14T18:01:00Z",
      } as MutationPayload);
      mocks.liveHandler?.({
        type: "status",
        cursor: "103-0",
        action: "issue.close",
        entity_type: "issue",
        entity_id: "TEAM-1",
        actor: "api-architect-1",
        timestamp: "2026-08-14T18:02:00Z",
      } as MutationPayload);
    });

    expect(
      screen.queryByRole("listitem", { name: "bob closed TEAM-2" }),
    ).not.toBeInTheDocument();
    expect(
      await screen.findByRole("listitem", {
        name: "api-architect-1 closed TEAM-1",
      }),
    ).toHaveAttribute("data-live", "true");
  });

  it("loads older history with the returned cursor", async () => {
    let resolveOlder!: (value: AuditPage) => void;
    const olderPage = new Promise<AuditPage>((resolve) => {
      resolveOlder = resolve;
    });
    mocks.fetchAuditEvents
      .mockResolvedValueOnce(
        page([event({ cursor: "101-0" })], "older-page-cursor"),
      )
      .mockReturnValueOnce(olderPage);

    render(<ActivityPage />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Load more activity" }),
    );
    await act(async () => {
      resolveOlder(
        page([
          event({
            cursor: "99-0",
            actor: "operator",
            action: "issue.create",
            entity_id: "TEAM-0",
            timestamp: "2026-08-14T16:00:00Z",
          }),
        ]),
      );
      await olderPage;
    });

    await vi.waitFor(() => {
      expect(mocks.fetchAuditEvents).toHaveBeenLastCalledWith("default", {
        limit: 50,
        since: "older-page-cursor",
      });
    });
    expect(
      await screen.findByRole("listitem", {
        name: "operator created TEAM-0",
      }),
    ).toBeInTheDocument();
    await vi.waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Load more activity" }),
      ).not.toBeInTheDocument();
    });
  });

  it("shows an empty state", async () => {
    mocks.fetchAuditEvents.mockResolvedValue(page([]));

    render(<ActivityPage />);

    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
  });

  it("shows an error state and retries history", async () => {
    mocks.fetchAuditEvents
      .mockRejectedValueOnce(new Error("audit unavailable"))
      .mockResolvedValueOnce(page([]));

    render(<ActivityPage />);
    expect(
      await screen.findByRole("heading", { name: "Activity did not load" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
    expect(mocks.fetchAuditEvents).toHaveBeenCalledTimes(2);
  });
});
