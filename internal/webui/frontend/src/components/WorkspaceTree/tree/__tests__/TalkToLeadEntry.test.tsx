/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TalkToLeadEntry component.
 * Covers label rendering, backend name display, and click callback.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TalkToLeadEntry } from "../TalkToLeadEntry";

describe("TalkToLeadEntry", () => {
  it("renders 'Talk to Lead' label", () => {
    render(<TalkToLeadEntry workspaceName="ws-1" />);
    expect(screen.getByText("Talk to Lead")).toBeInTheDocument();
  });

  it("shows backend name defaulting to 'claude'", () => {
    render(<TalkToLeadEntry workspaceName="ws-1" />);
    expect(screen.getByText("claude")).toBeInTheDocument();
  });

  it("shows custom backend name when provided", () => {
    render(<TalkToLeadEntry workspaceName="ws-1" backend="gemini" />);
    expect(screen.getByText("gemini")).toBeInTheDocument();
  });

  it("sets title attribute with backend name", () => {
    render(<TalkToLeadEntry workspaceName="ws-1" backend="cursor" />);
    expect(screen.getByTitle("Talk to Lead (cursor)")).toBeInTheDocument();
  });

  it("calls onTalkToLead with workspace name on click", () => {
    const onTalkToLead = vi.fn();
    render(
      <TalkToLeadEntry
        workspaceName="my-workspace"
        onTalkToLead={onTalkToLead}
      />,
    );
    fireEvent.click(screen.getByText("Talk to Lead"));
    expect(onTalkToLead).toHaveBeenCalledWith("my-workspace");
  });

  it("does not throw when onTalkToLead is not provided and button is clicked", () => {
    render(<TalkToLeadEntry workspaceName="ws-1" />);
    expect(() => {
      fireEvent.click(screen.getByText("Talk to Lead"));
    }).not.toThrow();
  });
});
