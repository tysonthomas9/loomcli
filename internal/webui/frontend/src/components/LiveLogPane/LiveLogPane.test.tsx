// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { LiveLogPane } from "./LiveLogPane";

const useLogStream = vi.fn();

vi.mock("@/hooks/terminal/useLogStream", () => ({
  useLogStream: (...args: unknown[]) => useLogStream(...args),
}));

describe("LiveLogPane", () => {
  beforeEach(() => {
    useLogStream.mockReset();
    useLogStream.mockReturnValue({
      content: "",
      state: "disconnected",
      error: null,
    });
  });

  it("renders streamed content and forwards its connection coordinates", () => {
    useLogStream.mockReturnValue({
      content: "planning line\n",
      state: "connected",
      error: null,
    });

    render(
      <LiveLogPane
        workspaceId="DESKTOP-QA"
        streamPath="/tasks/LOOM-1/logs/planning/stream"
        enabled
      />,
    );

    expect(useLogStream).toHaveBeenLastCalledWith({
      workspaceId: "DESKTOP-QA",
      streamPath: "/tasks/LOOM-1/logs/planning/stream",
      enabled: true,
    });
    expect(screen.getByTestId("agent-log-state")).toHaveAttribute(
      "data-state",
      "connected",
    );
    expect(screen.getByTestId("agent-log-content")).toHaveTextContent(
      "planning line",
    );
  });

  it("labels a connected empty stream as no logs", () => {
    useLogStream.mockReturnValue({
      content: "",
      state: "connected",
      error: null,
    });

    render(
      <LiveLogPane
        workspaceId="DESKTOP-QA"
        streamPath="/agents/ember/logs/stream"
        enabled
      />,
    );

    expect(screen.getByTestId("agent-log-state")).toHaveAttribute(
      "data-state",
      "empty",
    );
    expect(screen.getByTestId("agent-log-state")).toHaveTextContent("no logs");
  });

  it("sticks to new tail content until the reader scrolls up", () => {
    let content = "first\n";
    useLogStream.mockImplementation(() => ({
      content,
      state: "connected",
      error: null,
    }));

    const { rerender } = render(
      <LiveLogPane
        workspaceId="DESKTOP-QA"
        streamPath="/agents/ember/logs/stream"
        enabled
      />,
    );
    const output = screen.getByTestId("agent-log-content");
    Object.defineProperties(output, {
      scrollHeight: { configurable: true, value: 100 },
      clientHeight: { configurable: true, value: 20 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });

    content = "first\nsecond\n";
    rerender(
      <LiveLogPane
        workspaceId="DESKTOP-QA"
        streamPath="/agents/ember/logs/stream"
        enabled
      />,
    );
    expect(output.scrollTop).toBe(100);

    output.scrollTop = 0;
    fireEvent.scroll(output);
    content = "first\nsecond\nthird\n";
    rerender(
      <LiveLogPane
        workspaceId="DESKTOP-QA"
        streamPath="/agents/ember/logs/stream"
        enabled
      />,
    );
    expect(output.scrollTop).toBe(0);
  });
});
