/**
 * @vitest-environment jsdom
 */

import { act, render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Event } from "@/types";

import { Journey } from "../Journey";

const BASE_MS = Date.parse("2026-08-20T12:00:00.000Z");

function event(seconds = 0, overrides: Partial<Event> = {}): Event {
  return {
    id: `${BASE_MS + seconds * 1_000}-0`,
    issue_id: "loom-1",
    event_type: "issue.create",
    actor: "alice",
    created_at: new Date(BASE_MS + seconds * 1_000).toISOString(),
    ...overrides,
  };
}

describe("Journey", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(BASE_MS + 10_000));
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("renders an ordered vertical trace with nested audit rows", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event(5, {
            event_type: "issue.claim",
            actor: "agent-dev-1",
            summary: "agent-dev-1 claimed loom-1",
          }),
        ]}
      />,
    );

    const region = screen.getByRole("region", { name: "Task journey" });
    const trace = within(region).getByTestId("journey-trace");
    expect(trace.tagName).toBe("OL");
    expect(screen.getAllByTestId("journey-span")).toHaveLength(2);
    expect(screen.getByTestId("journey-tail")).toHaveTextContent("Now");
    expect(screen.getByText("agent-dev-1 claimed loom-1")).toBeInTheDocument();
    expect(
      screen.getByText("agent-dev-1 claimed loom-1").closest("li"),
    ).toHaveAttribute("data-actor-kind", "agent");
    expect(screen.queryByTestId("journey-now-line")).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("journey-overflow-left"),
    ).not.toBeInTheDocument();
  });

  it("renders a halt band and positions it in the spine gradient", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event(0, {
            id: `${BASE_MS}-1`,
            event_type: "issue.claim",
            actor: "agent-dev-1",
          }),
          event(1.997, {
            event_type: "issue.update",
            actor: "agent-dev-1",
            summary: "Updated notes and updated_at",
            changes: [
              { field: "notes", after: "BLOCKED: toolchain unavailable" },
              { field: "updated_at", after: "2026-08-20T12:00:01.997Z" },
            ],
          }),
          event(2, {
            event_type: "issue.update",
            actor: "agent-dev-1",
            changes: [
              { field: "status", before: "in_progress", after: "blocked" },
              { field: "updated_at", after: "2026-08-20T12:00:02.000Z" },
            ],
          }),
          event(5.997, {
            event_type: "issue.update",
            actor: "operator",
            summary: "Updated notes and updated_at",
            changes: [
              { field: "notes", after: "added go 1.24 to the agent image" },
              { field: "updated_at", after: "2026-08-20T12:00:05.997Z" },
            ],
          }),
          event(6, {
            event_type: "issue.status_changed",
            actor: "operator",
            old_value: "blocked",
            new_value: "in_progress",
          }),
          event(10, {
            event_type: "issue.close",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );

    const halt = screen.getByTestId("journey-halt");
    expect(halt).toHaveAccessibleName(
      "Halted — blocker declared by the agent, 4s",
    );
    expect(halt).toHaveTextContent("“toolchain unavailable”");
    expect(halt).toHaveTextContent(
      "Cleared — added go 1.24 to the agent image",
    );
    expect(screen.queryByText("Updated notes")).not.toBeInTheDocument();
    expect(within(halt).getAllByTestId("journey-audit-event")).toHaveLength(2);

    const railStyle = screen
      .getByTestId("journey-spine-rail")
      .getAttribute("style");
    expect(railStyle).toContain("linear-gradient");
    expect(railStyle).toContain("20%");
    expect(railStyle).toContain("60%");
    expect(railStyle).toContain("--color-journey-halt");
  });

  it("renders a quote-less halt band without a preceding BLOCKED note", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(0, { event_type: "issue.claim", actor: "agent-dev-1" }),
          event(5, {
            event_type: "issue.blocked",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );

    expect(screen.getByTestId("journey-halt")).not.toHaveTextContent("“");
  });

  it("renders the terminal Done tail with lead time, stages, and halts", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event(10, {
            event_type: "issue.claim",
            actor: "agent-dev-1",
          }),
          event(20, {
            event_type: "issue.blocked",
            actor: "agent-dev-1",
          }),
          event(30, {
            event_type: "issue.unblocked",
            actor: "operator",
          }),
          event(7_621, {
            event_type: "issue.close",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );

    expect(screen.getByTestId("journey-tail")).toHaveTextContent(
      "Doneclosed after 2h 07m 01s · 2 stages · 1 halt",
    );
  });

  it("renders the live Now tail and ticks its running duration", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event(5, {
            event_type: "issue.claim",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );

    const tail = screen.getByTestId("journey-tail");
    expect(tail).toHaveTextContent("Nowin this stage for 5s");

    act(() => vi.advanceTimersByTime(5_000));
    expect(tail).toHaveTextContent("Nowin this stage for 10s");
  });

  it("shows an open halt as still blocked and ticks its duration", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(0, {
            event_type: "issue.claim",
            actor: "agent-dev-1",
          }),
          event(5, {
            event_type: "issue.blocked",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );

    const halt = screen.getByTestId("journey-halt");
    expect(halt).toHaveAccessibleName("Halted — still blocked, 5s");
    expect(halt).toHaveAttribute("data-trailing", "true");
    expect(screen.getByTestId("journey-tail")).toHaveTextContent(
      "Nowhalted for 5s",
    );

    act(() => vi.advanceTimersByTime(2_000));
    expect(halt).toHaveAccessibleName("Halted — still blocked, 7s");
    expect(screen.getByTestId("journey-tail")).toHaveTextContent(
      "Nowhalted for 7s",
    );
  });

  it("keeps the live ticker aligned to the span's clock boundary", () => {
    const { rerender } = render(
      <Journey eventLimit={200} events={[event()]} />,
    );

    act(() => vi.advanceTimersByTime(800));
    rerender(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event(10, {
            event_type: "issue.status_changed",
            new_value: "review",
          }),
        ]}
      />,
    );

    const tail = screen.getByTestId("journey-tail");
    expect(tail).toHaveTextContent("in this stage for 0s");

    act(() => vi.advanceTimersByTime(199));
    expect(tail).toHaveTextContent("in this stage for 0s");

    act(() => vi.advanceTimersByTime(1));
    expect(tail).toHaveTextContent("in this stage for 1s");

    act(() => vi.advanceTimersByTime(1));
    expect(tail).toHaveTextContent("in this stage for 1s");

    act(() => vi.advanceTimersByTime(4_999));
    expect(tail).toHaveTextContent("in this stage for 6s");
  });

  it("announces only a real stage change, not a blocked halt", () => {
    const { rerender } = render(
      <Journey eventLimit={200} events={[event()]} />,
    );
    const liveRegion = screen.getByTestId("journey-stage-announcement");
    expect(liveRegion).toHaveAttribute("aria-live", "polite");
    expect(liveRegion).toBeEmptyDOMElement();

    const claimed = [
      event(),
      event(5, {
        event_type: "issue.claim",
        actor: "agent-dev-1",
      }),
    ];
    rerender(<Journey eventLimit={200} events={claimed} />);
    expect(liveRegion).toHaveTextContent(
      "Journey updated: In progress, owned by agent-dev-1.",
    );

    rerender(
      <Journey
        eventLimit={200}
        events={[
          ...claimed,
          event(7, {
            event_type: "issue.blocked",
            actor: "agent-dev-1",
          }),
        ]}
      />,
    );
    expect(liveRegion).not.toHaveTextContent("Blocked");
    expect(liveRegion).toHaveTextContent(
      "Journey updated: In progress, owned by agent-dev-1.",
    );
  });

  it("renders the empty state and reports a truncated event window", () => {
    const { rerender } = render(<Journey events={[]} eventLimit={2} />);
    expect(
      screen.getByText("No journey stages in this window."),
    ).toBeInTheDocument();
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "Stages derived from 0 events returned.",
    );

    rerender(
      <Journey
        eventLimit={2}
        events={[
          event(),
          event(5, { event_type: "issue.claim", actor: "agent-dev-1" }),
        ]}
      />,
    );
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "Stages derived from the most recent 2 events returned. Earlier history may not be included.",
    );
  });

  it("renders a created, unclaimed Open span as a quiet dashed panel", () => {
    render(
      <Journey
        eventLimit={200}
        events={[event(0, { event_type: "issue.create", actor: "operator" })]}
      />,
    );

    const span = screen.getByTestId("journey-span");
    expect(
      screen.getByTestId("journey-spine-rail").getAttribute("style"),
    ).toContain("repeating-linear-gradient");
    expect(screen.getByText("unclaimed")).toBeInTheDocument();
    expect(span.querySelector("article")).toHaveAttribute("data-quiet", "true");
  });
});
