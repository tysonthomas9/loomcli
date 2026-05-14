/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TalkToLeadButton } from "../TalkToLeadButton";

describe("TalkToLeadButton", () => {
  it("renders without badge when no sessionCount prop", () => {
    render(<TalkToLeadButton />);
    expect(screen.getByTestId("talk-to-lead-button")).toBeInTheDocument();
    expect(screen.queryByLabelText(/active sessions/)).not.toBeInTheDocument();
  });

  it("renders without badge when sessionCount is 0", () => {
    render(<TalkToLeadButton sessionCount={0} />);
    expect(screen.queryByLabelText(/active sessions/)).not.toBeInTheDocument();
  });

  it("renders badge when sessionCount > 0", () => {
    render(<TalkToLeadButton sessionCount={3} />);
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByLabelText("3 active sessions")).toBeInTheDocument();
  });

  it("aria-label includes count when sessionCount > 0", () => {
    render(<TalkToLeadButton sessionCount={2} />);
    expect(
      screen.getByLabelText("Talk to Lead (2 active sessions)"),
    ).toBeInTheDocument();
  });

  it("aria-label is plain when sessionCount is 0", () => {
    render(<TalkToLeadButton sessionCount={0} />);
    expect(screen.getByLabelText("Talk to Lead")).toBeInTheDocument();
  });

  it("calls onClick when clicked", () => {
    const onClick = vi.fn();
    render(<TalkToLeadButton onClick={onClick} sessionCount={1} />);
    fireEvent.click(screen.getByTestId("talk-to-lead-button"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("sets data-active when isActive is true", () => {
    render(<TalkToLeadButton isActive={true} />);
    expect(screen.getByTestId("talk-to-lead-button")).toHaveAttribute(
      "data-active",
      "true",
    );
  });

  it("marks the button when it should avoid a side panel", () => {
    render(<TalkToLeadButton avoidSidePanel={true} />);
    expect(screen.getByTestId("talk-to-lead-button")).toHaveAttribute(
      "data-avoid-side-panel",
      "true",
    );
  });
});
