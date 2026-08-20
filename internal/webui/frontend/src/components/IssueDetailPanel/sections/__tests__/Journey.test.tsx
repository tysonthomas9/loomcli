/**
 * @vitest-environment jsdom
 */

import { act, render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Event } from "@/types";

import { Journey } from "../Journey";

function event(overrides: Partial<Event> = {}): Event {
  return {
    id: "1787248800000-0",
    issue_id: "loom-1",
    event_type: "issue.create",
    actor: "alice",
    created_at: "2026-08-20T12:00:00.000Z",
    ...overrides,
  };
}

describe("Journey", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-20T12:00:10.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders spans and anchors the live now-line inside the live span", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event({
            id: "1787248805000-0",
            event_type: "issue.update",
            created_at: "2026-08-20T12:00:05.000Z",
            changes: [{ field: "status", before: "open", after: "blocked" }],
          }),
        ]}
      />,
    );

    expect(screen.getAllByTestId("journey-span")).toHaveLength(2);
    expect(screen.getByText("Stuck")).toBeInTheDocument();
    const liveSpan = screen.getAllByTestId("journey-span").at(-1);
    expect(liveSpan).toHaveAttribute("data-live", "true");
    expect(
      within(liveSpan as HTMLElement).getByTestId("journey-now-line"),
    ).toHaveAccessibleName("Now");
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "Stages derived from 2 events returned.",
    );
    expect(screen.getByTestId("journey-window-note")).not.toHaveTextContent(
      "Earlier history may not be included",
    );

    act(() => vi.advanceTimersByTime(5_000));
    expect(screen.getByText("10s")).toBeInTheDocument();
  });

  it("renders an honest empty-window state without a now-line", () => {
    render(<Journey events={[]} eventLimit={200} />);

    expect(
      screen.getByText("No journey stages in this window."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("journey-now-line")).not.toBeInTheDocument();
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "Stages derived from 0 events returned.",
    );
  });

  it("warns about earlier history only when the response fills its limit", () => {
    render(
      <Journey
        events={[
          event(),
          event({
            id: "1787248805000-0",
            event_type: "issue.claim",
            created_at: "2026-08-20T12:00:05.000Z",
          }),
        ]}
        eventLimit={2}
      />,
    );

    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "Stages derived from the most recent 2 events returned. Earlier history may not be included.",
    );
  });

  it("renders unequal durations as a uniform sequence with duration text", () => {
    render(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event({
            id: "1787248860000-0",
            event_type: "issue.update",
            actor: "worker-1",
            created_at: "2026-08-20T12:01:00.000Z",
            changes: [
              { field: "status", before: "open", after: "in_progress" },
            ],
          }),
          event({
            id: "1787331600000-0",
            event_type: "issue.close",
            actor: "worker-1",
            created_at: "2026-08-21T11:00:00.000Z",
          }),
        ]}
      />,
    );

    const spans = screen.getAllByTestId("journey-span");
    expect(spans).toHaveLength(3);
    expect(new Set(spans.map((span) => span.style.flexGrow))).toHaveLength(1);
    expect(spans[0].style.flexGrow).toBe("");
    expect(within(spans[0]).getByText("1m")).toBeInTheDocument();
    expect(within(spans[1]).getByText("22h 59m")).toBeInTheDocument();
    expect(within(spans[2]).getByText("0s")).toBeInTheDocument();
  });
});
