/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SidebarStatusBar component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { SidebarStatusBar } from "../SidebarStatusBar";
import type { LoomAgentStatus } from "@/types";

function makeAgent(name: string, status: string): LoomAgentStatus {
  return { name, branch: "main", status, ahead: 0, behind: 0 };
}

describe("SidebarStatusBar", () => {
  it("renders nothing when agents array is empty", () => {
    const { container } = render(<SidebarStatusBar agents={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders correct counts for mixed agent statuses", () => {
    const agents = [
      makeAgent("a1", "working: bd-123 (5m)"),
      makeAgent("a2", "working: bd-456 (2m)"),
      makeAgent("a3", "review: bd-789 (1m)"),
      makeAgent("a4", "ready"),
      makeAgent("a5", "idle"),
      makeAgent("a6", "done: bd-111 (10m)"),
    ];

    render(<SidebarStatusBar agents={agents} />);

    expect(screen.getByText(/2 working/)).toBeInTheDocument();
    expect(screen.getByText(/1 reviewing/)).toBeInTheDocument();
    expect(screen.getByText(/3 idle/)).toBeInTheDocument();
  });

  it("categorizes planning as working", () => {
    const agents = [
      makeAgent("a1", "planning: bd-123 (3m)"),
      makeAgent("a2", "ready"),
    ];

    render(<SidebarStatusBar agents={agents} />);

    expect(screen.getByText(/1 working/)).toBeInTheDocument();
    expect(screen.getByText(/0 reviewing/)).toBeInTheDocument();
    expect(screen.getByText(/1 idle/)).toBeInTheDocument();
  });

  it("categorizes error, dirty, and changes as idle", () => {
    const agents = [
      makeAgent("a1", "error: bd-123 (1m)"),
      makeAgent("a2", "dirty"),
      makeAgent("a3", "3 changes"),
    ];

    render(<SidebarStatusBar agents={agents} />);

    expect(screen.getByText(/0 working/)).toBeInTheDocument();
    expect(screen.getByText(/0 reviewing/)).toBeInTheDocument();
    expect(screen.getByText(/3 idle/)).toBeInTheDocument();
  });

  it("updates counts when agents prop changes", () => {
    const agents1 = [makeAgent("a1", "working: bd-123 (5m)")];
    const { rerender } = render(<SidebarStatusBar agents={agents1} />);
    expect(screen.getByText(/1 working/)).toBeInTheDocument();

    const agents2 = [
      makeAgent("a1", "working: bd-123 (5m)"),
      makeAgent("a2", "review: bd-456 (2m)"),
    ];
    rerender(<SidebarStatusBar agents={agents2} />);
    expect(screen.getByText(/1 working/)).toBeInTheDocument();
    expect(screen.getByText(/1 reviewing/)).toBeInTheDocument();
  });
});
