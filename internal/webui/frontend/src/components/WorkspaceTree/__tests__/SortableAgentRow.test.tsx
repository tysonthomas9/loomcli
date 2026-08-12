/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";

import { SortableAgentRow } from "../SortableAgentRow";

vi.mock("@dnd-kit/sortable", () => ({
  useSortable: () => ({
    attributes: {},
    listeners: {},
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}));

vi.mock("@/components/AgentCard", () => ({
  AgentCard: ({
    agent,
    onClick,
  }: {
    agent: LoomAgentStatus;
    onClick?: () => void;
  }) => (
    <button type="button" data-testid="agent-card-stub" onClick={onClick}>
      {agent.name}
    </button>
  ),
}));

vi.mock("../AgentSection.module.css", () => ({
  default: {
    agentRow: "agentRow",
    agentCardInRow: "agentCardInRow",
    archiveButton: "archiveButton",
    dragHandle: "dragHandle",
  },
}));

const agent: LoomAgentStatus = {
  name: "codex-coder",
  branch: "main",
  status: "changes",
  ahead: 0,
  behind: 0,
  workspace: "LOCALMODE",
};

describe("SortableAgentRow", () => {
  let onArchive: ReturnType<typeof vi.fn>;
  let onContextMenu: ReturnType<typeof vi.fn>;
  let onAgentClick: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onArchive = vi.fn();
    onContextMenu = vi.fn();
    onAgentClick = vi.fn();
  });

  it("fires onArchive from the hover archive button", () => {
    render(
      <SortableAgentRow
        agent={agent}
        onArchive={onArchive}
        onContextMenu={onContextMenu}
        onAgentClick={onAgentClick}
      />,
    );

    fireEvent.click(screen.getByTestId("agent-row-archive"));
    expect(onArchive).toHaveBeenCalledWith("codex-coder");
    expect(onAgentClick).not.toHaveBeenCalled();
  });

  it("fires onContextMenu with the agent name on right-click", () => {
    render(
      <SortableAgentRow
        agent={agent}
        onArchive={onArchive}
        onContextMenu={onContextMenu}
      />,
    );

    fireEvent.contextMenu(screen.getByTestId("sortable-agent-row"));
    expect(onContextMenu).toHaveBeenCalledTimes(1);
    expect(onContextMenu.mock.calls[0]?.[1]).toBe("codex-coder");
  });

  it("hides the archive button when onArchive is omitted", () => {
    render(<SortableAgentRow agent={agent} />);
    expect(screen.queryByTestId("agent-row-archive")).not.toBeInTheDocument();
  });
});
