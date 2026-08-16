// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it } from "vitest";

import type { TranscriptEntry } from "@/types/agent";

import { TranscriptRows } from "../TranscriptRows";

describe("TranscriptRows", () => {
  it("renders assistant text, collapsed command output, and usage", () => {
    const entries: TranscriptEntry[] = [
      {
        seq: 1,
        role: "assistant",
        type: "tool_use",
        tool_name: "shell",
        tool_input: { command: "make gate" },
        output: "[exit 1]\ngate failed\n",
      },
      {
        seq: 2,
        role: "assistant",
        type: "text",
        text: '{"recommendations":[]}',
      },
      {
        seq: 3,
        role: "system",
        type: "result",
        text: "completed | in=349798 out=3342 cache_read=305920",
        output: JSON.stringify({
          input_tokens: 349_798,
          output_tokens: 3_342,
          cache_read_tokens: 305_920,
        }),
      },
    ];

    render(<TranscriptRows entries={entries} />);

    expect(screen.getByTestId("transcript-command")).toHaveTextContent(
      "$ make gate",
    );
    expect(screen.getByTestId("transcript-command")).toHaveTextContent(
      "exit 1",
    );
    expect(screen.queryByText("gate failed")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("transcript-output-toggle"));
    const output = screen.getByText(/gate failed/);
    expect(output).toBeInTheDocument();
    expect(output.closest('[data-testid="transcript-view"]')).toBe(
      screen.getByTestId("transcript-view"),
    );
    expect(screen.getByTestId("transcript-message")).toHaveTextContent(
      '"recommendations": []',
    );
    expect(screen.getByTestId("transcript-turn-completed")).toHaveTextContent(
      "349,798 input tokens (305,920 cached) · 3,342 output",
    );
  });

  it("renders reasoning, system text, unknown entries, and failed results explicitly", () => {
    const entries = [
      {
        seq: 1,
        role: "system",
        type: "session_meta",
        text: "local-cli-codex session",
      },
      { seq: 2, role: "system", type: "text", text: "plain system notice" },
      {
        seq: 3,
        role: "assistant",
        type: "reasoning",
        text: "inspect the repo",
      },
      { seq: 4, role: "system", type: "future_event", text: "new payload" },
      { seq: 5, role: "system", type: "result", text: "failed" },
    ] as unknown as TranscriptEntry[];

    render(<TranscriptRows entries={entries} />);

    expect(screen.getByTestId("transcript-system-text")).toHaveTextContent(
      "plain system notice",
    );
    expect(screen.getByTestId("transcript-reasoning")).toHaveTextContent(
      "inspect the repo",
    );
    expect(screen.getByTestId("transcript-unknown")).toHaveTextContent(
      "future event",
    );
    expect(screen.getByTestId("transcript-failed")).toHaveTextContent("Failed");
  });
});
