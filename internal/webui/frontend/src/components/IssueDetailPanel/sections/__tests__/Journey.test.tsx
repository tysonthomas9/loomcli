/**
 * @vitest-environment jsdom
 */

import { act, render, screen } from "@testing-library/react";
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

  it("renders spans, a live now-line, and the bounded-window disclosure", () => {
    render(
      <Journey
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
    expect(screen.getByTestId("journey-now-line")).toHaveAccessibleName("Now");
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "most recent 2 events returned",
    );

    act(() => vi.advanceTimersByTime(5_000));
    expect(screen.getByText("10s")).toBeInTheDocument();
  });

  it("renders an honest empty-window state without a now-line", () => {
    render(<Journey events={[]} />);

    expect(
      screen.getByText("No journey stages in this window."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("journey-now-line")).not.toBeInTheDocument();
    expect(screen.getByTestId("journey-window-note")).toHaveTextContent(
      "most recent 0 events returned",
    );
  });
});
