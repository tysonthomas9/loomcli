/**
 * @vitest-environment jsdom
 */

import {
  act,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
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
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
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
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Task journey stages" }),
    ).toHaveAttribute("tabindex", "0");
    const liveSpan = screen.getAllByTestId("journey-span").at(-1);
    expect(liveSpan).toHaveAttribute("data-live", "true");
    expect(liveSpan).toHaveAttribute("data-stage", "blocked");
    expect(
      within(liveSpan as HTMLElement).getByRole("img", {
        name: "Now, current stage",
      }),
    ).toBeInTheDocument();
    expect(liveSpan).toHaveAccessibleName(
      "Blocked · Human action required · alice · 5s",
    );
    expect(
      within(liveSpan as HTMLElement).getByTestId("journey-attention-icon"),
    ).toHaveAttribute("aria-hidden", "true");
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

  it("keeps a newly arrived live 0s span, now-line, and announcement visible", () => {
    const { rerender } = render(
      <Journey eventLimit={200} events={[event()]} />,
    );

    rerender(
      <Journey
        eventLimit={200}
        events={[
          event(),
          event({
            id: "1787248810000-0",
            event_type: "issue.update",
            created_at: "2026-08-20T12:00:10.000Z",
            changes: [{ field: "status", before: "open", after: "review" }],
          }),
        ]}
      />,
    );

    const spans = screen.getAllByTestId("journey-span");
    const liveSpan = spans.at(-1) as HTMLElement;
    expect(spans).toHaveLength(2);
    expect(within(liveSpan).getByText("Review")).toBeInTheDocument();
    expect(within(liveSpan).getByText("0s")).toBeInTheDocument();
    expect(liveSpan).toHaveAttribute("data-live", "true");
    expect(
      within(liveSpan).getByTestId("journey-now-line"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("journey-stage-announcement")).toHaveTextContent(
      "Journey updated: Review, owned by alice.",
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

  it("announces a new stage politely without changing the announcement on ticks", () => {
    const { rerender } = render(
      <Journey eventLimit={200} events={[event()]} />,
    );
    const liveRegion = screen.getByTestId("journey-stage-announcement");

    expect(liveRegion).toHaveAttribute("aria-live", "polite");
    expect(liveRegion).toHaveAttribute("aria-atomic", "true");
    expect(liveRegion).toBeEmptyDOMElement();

    rerender(
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

    expect(liveRegion).toHaveTextContent(
      "Journey updated: Blocked, owned by alice.",
    );

    act(() => vi.advanceTimersByTime(2_000));
    expect(liveRegion).toHaveTextContent(
      "Journey updated: Blocked, owned by alice.",
    );
  });

  it("opens a closed overflowing journey at the end and preserves user scroll", () => {
    vi.spyOn(Element.prototype, "scrollWidth", "get").mockReturnValue(1_144);
    vi.spyOn(Element.prototype, "clientWidth", "get").mockReturnValue(774);

    const events = [
      event(),
      event({
        id: "1787248805000-0",
        event_type: "issue.close",
        created_at: "2026-08-20T12:00:05.000Z",
      }),
    ];
    const { rerender } = render(<Journey eventLimit={200} events={events} />);
    const rail = screen.getByRole("region", { name: "Task journey stages" });

    expect(rail.scrollLeft).toBe(370);
    expect(screen.getByTestId("journey-overflow-left")).toHaveAttribute(
      "data-visible",
      "true",
    );
    expect(screen.getByTestId("journey-overflow-right")).not.toHaveAttribute(
      "data-visible",
    );

    rail.scrollLeft = 75;
    rerender(<Journey eventLimit={200} events={events} />);
    expect(rail.scrollLeft).toBe(75);
  });

  it("does not yank a live rail on ticks and moves only for a new live span", () => {
    vi.spyOn(Element.prototype, "scrollWidth", "get").mockReturnValue(1_144);
    vi.spyOn(Element.prototype, "clientWidth", "get").mockReturnValue(774);
    vi.spyOn(HTMLElement.prototype, "offsetLeft", "get").mockReturnValue(1_040);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(104);

    const { rerender } = render(
      <Journey eventLimit={200} events={[event()]} />,
    );
    const rail = screen.getByRole("region", { name: "Task journey stages" });
    expect(rail.scrollLeft).toBe(370);

    rail.scrollLeft = 75;
    act(() => vi.advanceTimersByTime(2_000));
    expect(rail.scrollLeft).toBe(75);

    rerender(
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
    expect(rail.scrollLeft).toBe(370);
  });

  it("clears the right overflow cue within one pixel of maximum scroll", () => {
    vi.spyOn(Element.prototype, "scrollWidth", "get").mockReturnValue(3_026);
    vi.spyOn(Element.prototype, "clientWidth", "get").mockReturnValue(774);

    render(<Journey eventLimit={200} events={[event()]} />);

    const rail = screen.getByRole("region", { name: "Task journey stages" });
    expect(screen.getByTestId("journey-overflow-right")).toHaveAttribute(
      "data-visible",
      "true",
    );

    rail.scrollLeft = 2_251.62;
    fireEvent.scroll(rail);

    expect(screen.getByTestId("journey-overflow-right")).not.toHaveAttribute(
      "data-visible",
    );
    expect(screen.getByTestId("journey-overflow-left")).toHaveAttribute(
      "data-visible",
      "true",
    );
  });
});
