/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { RunningWithoutYou } from "../RunningWithoutYou";
import type { Issue, LoomAgentStatus } from "@/types";

function agent(overrides: Partial<LoomAgentStatus>): LoomAgentStatus {
  return {
    name: "agent-dev-1",
    branch: "agent-dev-1",
    status: "idle (4m)",
    ahead: 0,
    behind: 0,
    workspace: "workspace-1",
    ...overrides,
  };
}

const task = {
  id: "TASK-1",
  title: "Wire the registry",
  priority: 2,
  issue_type: "task",
  status: "in_progress",
  created_at: "2026-08-21T15:00:00.000Z",
  updated_at: "2026-08-21T15:00:00.000Z",
} as Issue;

describe("RunningWithoutYou", () => {
  it("shows live work, idle agents, and opens the selected agent", () => {
    const onWatch = vi.fn();
    render(
      <RunningWithoutYou
        agents={[
          agent({
            name: "agent-dev-1",
            status: "working: TASK-1 (12m)",
            active_task_id: "TASK-1",
            backend: "codex",
            changes: [{ path: "src/app.ts", status: "modified" }],
          }),
          agent({ name: "agent-architect-1", role: "plan" }),
        ]}
        issues={[task]}
        onWatch={onWatch}
      />,
    );

    expect(screen.getByTestId("running-without-you")).toHaveTextContent(
      "1 session live · 1 agent idle",
    );
    expect(screen.getByTestId("live-agent")).toHaveAttribute(
      "data-agent",
      "agent-dev-1",
    );
    expect(screen.getByText("TASK-1 · Wire the registry")).toBeInTheDocument();
    expect(screen.getByTestId("idle-agent-pill")).toHaveTextContent(
      "agent-architect-1",
    );

    fireEvent.click(screen.getByRole("button", { name: "Watch" }));
    expect(onWatch).toHaveBeenCalledWith("agent-dev-1");
  });
});
