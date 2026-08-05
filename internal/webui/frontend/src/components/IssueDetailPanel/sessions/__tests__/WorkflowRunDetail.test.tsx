/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { TaskWorkflowRun } from "@/api/workflows";

import { WorkflowRunDetail } from "../WorkflowRunDetail";
import { WorkflowRunTimelineRow } from "../WorkflowRunTimelineRow";

function repositoryGuardRun(): TaskWorkflowRun {
  return {
    workspace_key: "WS",
    run_id: "automation-run-repository-guard",
    driver_id: "prompt-agent",
    driver_version_id: "v1",
    source_ref: "trigger-event-task-10",
    status: "completed",
    summary: "Repository selection is required before an agent task can start.",
    output: {
      blocker: "repository_required",
      claimed: "false",
      skipped: "true",
    },
    started_at: "2026-07-18T20:00:00Z",
    finished_at: "2026-07-18T20:00:01Z",
    created_at: "2026-07-18T20:00:00Z",
    updated_at: "2026-07-18T20:00:01Z",
  };
}

describe("sessionless workflow run UI", () => {
  it("shows the durable outcome and explains why transcript and diff are absent", () => {
    render(<WorkflowRunDetail run={repositoryGuardRun()} />);
    expect(screen.getByText("Automation run")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Repository selection is required before an agent task can start.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Repository Required")).toBeInTheDocument();
    expect(screen.getByText("trigger-event-task-10")).toBeInTheDocument();
    expect(
      screen.getByText(
        /No agent session was recorded.*no agent transcript or diff/i,
      ),
    ).toBeInTheDocument();
  });

  it("describes an active workflow as pending rather than completed", () => {
    render(
      <WorkflowRunDetail
        run={{
          ...repositoryGuardRun(),
          run_id: "automation-run-queued",
          status: "queued",
          summary: undefined,
          output: undefined,
          started_at: undefined,
          finished_at: undefined,
        }}
      />,
    );
    expect(
      screen.getByText(
        "The automation is queued and has not started an agent session yet.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/An agent session is not available yet.*will update/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/automation completed/i)).not.toBeInTheDocument();
  });

  it("renders an accessible selectable automation row", () => {
    const onClick = vi.fn();
    render(
      <WorkflowRunTimelineRow
        run={repositoryGuardRun()}
        isSelected={false}
        onClick={onClick}
      />,
    );
    const row = screen.getByRole("button", {
      name: /Automation run, Completed, Repository selection is required/i,
    });
    fireEvent.keyDown(row, { key: "Enter" });
    expect(onClick).toHaveBeenCalledOnce();
  });
});
