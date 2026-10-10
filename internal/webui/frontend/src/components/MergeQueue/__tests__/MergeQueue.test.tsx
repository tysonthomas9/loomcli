/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchMergeQueue } from "@/api/workspace/git";

import { MergeQueue } from "../MergeQueue";

vi.mock("@/api/workspace/git", () => ({ fetchMergeQueue: vi.fn() }));

describe("MergeQueue", () => {
  beforeEach(() => vi.mocked(fetchMergeQueue).mockReset());

  it("shows the lead's queued merge with its PR", async () => {
    vi.mocked(fetchMergeQueue).mockResolvedValue([
      {
        stack_id: "feature",
        target: "task-3",
        pr_number: 12,
        pr_url: "https://github.com/o/r/pull/12",
        backend: "native",
        phase: "ready",
        queued_by: "lead",
      },
    ]);
    render(<MergeQueue workspaceId="W" />);
    const entry = await screen.findByTestId("merge-queue-entry");
    expect(entry).toHaveTextContent(
      "Merge up to task-3 (#12) — queued, waiting for checks and reviews. Queued by the lead.",
    );
    expect(fetchMergeQueue).toHaveBeenCalledWith("W");
  });

  it("names the human and a blocked reason", async () => {
    vi.mocked(fetchMergeQueue).mockResolvedValue([
      {
        stack_id: "feature",
        target: "task-2",
        backend: "loom",
        phase: "blocked",
        reason: "PR 3 checks or review failed",
        queued_by: "tyson",
      },
    ]);
    render(<MergeQueue workspaceId="W" />);
    expect(await screen.findByTestId("merge-queue-entry")).toHaveTextContent(
      "Merge up to task-2 — blocked: PR 3 checks or review failed. Queued by tyson.",
    );
  });

  it("renders nothing with an empty queue", async () => {
    vi.mocked(fetchMergeQueue).mockResolvedValue([]);
    const { container } = render(<MergeQueue workspaceId="W" />);
    await vi.waitFor(() => expect(fetchMergeQueue).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
