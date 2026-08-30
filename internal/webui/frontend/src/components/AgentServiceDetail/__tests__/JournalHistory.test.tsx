// @vitest-environment jsdom

import { fireEvent, render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it } from "vitest";

import { JournalHistory, parseJournal } from "../JournalHistory";

const JOURNAL = `# Scout journal

Append-only run log used as dedupe memory.

## Run 2026-08-30T00:01:11.968Z (driver run run-old)

Repos analyzed:
- source-repo @ abc123 (branch main)

Issues created:
- WS-1 — First recommendation

Skipped candidates:
- none

Warnings:
- none

## Run 2026-08-30T01:00:59.945Z (driver run run-new)

Repos analyzed:
- source-repo @ def456 (branch main)

Issues created:
- none

Skipped candidates:
- First recommendation — Already covered by WS-1.

Warnings:
- prompt output was truncated
`;

describe("JournalHistory", () => {
  it("parses append-only runs without changing the source order", () => {
    expect(parseJournal(JOURNAL)).toEqual({
      title: "Scout journal",
      introduction: "Append-only run log used as dedupe memory.",
      runs: [
        {
          timestamp: "2026-08-30T00:01:11.968Z",
          runId: "run-old",
          sections: [
            {
              label: "Repos analyzed",
              items: ["source-repo @ abc123 (branch main)"],
            },
            {
              label: "Issues created",
              items: ["WS-1 — First recommendation"],
            },
            { label: "Skipped candidates", items: [] },
            { label: "Warnings", items: [] },
          ],
        },
        {
          timestamp: "2026-08-30T01:00:59.945Z",
          runId: "run-new",
          sections: [
            {
              label: "Repos analyzed",
              items: ["source-repo @ def456 (branch main)"],
            },
            { label: "Issues created", items: [] },
            {
              label: "Skipped candidates",
              items: ["First recommendation — Already covered by WS-1."],
            },
            {
              label: "Warnings",
              items: ["prompt output was truncated"],
            },
          ],
        },
      ],
    });
  });

  it("renders newest-first compact summaries and reveals details on demand", () => {
    const { container } = render(<JournalHistory content={JOURNAL} />);
    const summaries = Array.from(container.querySelectorAll("summary"));

    expect(summaries[0]).toHaveTextContent("0 created");
    expect(summaries[0]).toHaveTextContent("1 skipped");
    expect(summaries[0]).toHaveTextContent("1 warning");
    const newestRun = summaries[0]!.closest("details");
    expect(newestRun).not.toBeNull();
    expect(
      within(newestRun!).getByText(/Already covered by WS-1/),
    ).not.toBeVisible();

    fireEvent.click(summaries[0]!);

    expect(within(newestRun!).getByText("Driver run run-new")).toBeVisible();
    expect(
      within(newestRun!).getByText(/Already covered by WS-1/),
    ).toBeVisible();
    expect(screen.getByText("Full journal")).toBeInTheDocument();
  });

  it("falls back to the markdown presentation for an unstructured journal", () => {
    const { container } = render(
      <JournalHistory content="# Notes\n\nFree-form journal entry." />,
    );

    expect(container).toHaveTextContent("Notes");
    expect(container).toHaveTextContent("Free-form journal entry.");
    expect(container.querySelector("details")).not.toBeInTheDocument();
  });
});
