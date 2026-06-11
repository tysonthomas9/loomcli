/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import type { Issue } from "@/types";

import styles from "../EpicRollup.module.css";
import { EpicRollup } from "../EpicRollup";

function createTicket(
  overrides: Partial<Issue> = {},
): Issue {
  return {
    id: "TASK-1",
    title: "Example ticket",
    status: "open",
    priority: 2,
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("EpicRollup", () => {
  it("renders empty state when there are no child tickets", () => {
    render(<EpicRollup tickets={[]} />);

    expect(screen.getByTestId("epic-rollup")).toBeInTheDocument();
    expect(screen.getByText("No child tickets yet.")).toBeInTheDocument();
  });

  it("renders child tickets across workflow statuses", () => {
    const tickets = [
      createTicket({
        id: "TASK-IP",
        title: "Local mode planner dogfood",
        status: "in_progress",
        assignee: "Planner",
      }),
      createTicket({
        id: "TASK-RV",
        title: "Review staging checklist",
        status: "review",
      }),
      createTicket({
        id: "TASK-DEF",
        title: "Backlog grooming placeholder",
        status: "deferred",
      }),
      createTicket({
        id: "TASK-DONE",
        title: "Ship epic runner",
        status: "closed",
      }),
    ];

    render(<EpicRollup tickets={tickets} />);

    expect(screen.getByText("Local mode planner dogfood")).toBeInTheDocument();
    expect(screen.getByText("Backlog grooming placeholder")).toBeInTheDocument();
    expect(screen.getByText("1 of 4 complete")).toBeInTheDocument();
    expect(screen.getByText("Tickets (4)")).toBeInTheDocument();
  });

  it("applies the same title typography class to every ticket row", () => {
    const tickets = [
      createTicket({
        id: "TASK-IP",
        title: "Local mode planner dogfood",
        status: "in_progress",
      }),
      createTicket({
        id: "TASK-DEF",
        title: "Backlog grooming placeholder",
        status: "deferred",
      }),
      createTicket({
        id: "TASK-RV",
        title: "Review staging checklist",
        status: "review",
      }),
    ];

    const { container } = render(<EpicRollup tickets={tickets} />);

    const titles = container.querySelectorAll(`.${styles.ticketTitle}`);
    expect(titles).toHaveLength(3);
    for (const title of titles) {
      expect(title).toHaveClass(styles.ticketTitle);
    }
  });

  it("calls onTicketClick when a row is activated", () => {
    const onTicketClick = vi.fn();
    const ticket = createTicket({
      id: "TASK-1",
      title: "Clickable ticket",
      status: "open",
    });

    render(
      <EpicRollup tickets={[ticket]} onTicketClick={onTicketClick} />,
    );

    fireEvent.click(screen.getByText("Clickable ticket"));
    expect(onTicketClick).toHaveBeenCalledWith(ticket);
  });
});
