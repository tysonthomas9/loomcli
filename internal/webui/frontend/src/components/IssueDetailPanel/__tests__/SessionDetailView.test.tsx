/**
 * @vitest-environment jsdom
 */
import React from "react";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import {
  SessionDetailView,
  type TranscriptEntry,
} from "../SessionDetailView";

// Mock the CSS module
vi.mock("../SessionDetailView.module.css", () => ({
  default: new Proxy(
    {},
    {
      get: (_target, prop) => String(prop),
    },
  ),
}));

function createEntry(overrides: Partial<TranscriptEntry> = {}): TranscriptEntry {
  return {
    seq: 1,
    role: "tool",
    ...overrides,
  };
}

describe("SessionDetailView", () => {
  describe("tool_input truncation", () => {
    it("truncates tool_input longer than 2000 chars", () => {
      const longInput = "x".repeat(10000);
      const entries = [createEntry({ seq: 1, tool_input: longInput })];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      // The rendered text should be truncated (not the full 10000 chars)
      const pre = screen.getByTestId("transcript-entry-1").querySelector("pre");
      expect(pre).not.toBeNull();
      // Should be around 2000 chars + "..."
      expect(pre!.textContent!.length).toBeLessThan(2100);
      expect(pre!.textContent).toContain("...");

      // Show full input button should be present
      expect(screen.getByTestId("show-full-input")).toBeTruthy();
    });

    it("does not truncate tool_input under 2000 chars", () => {
      const shortInput = "y".repeat(500);
      const entries = [createEntry({ seq: 1, tool_input: shortInput })];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      const pre = screen.getByTestId("transcript-entry-1").querySelector("pre");
      expect(pre).not.toBeNull();
      expect(pre!.textContent).toBe(shortInput);

      // No toggle button
      expect(screen.queryByTestId("show-full-input")).toBeNull();
      expect(screen.queryByTestId("show-less-input")).toBeNull();
    });

    it("does not truncate tool_input of exactly 2000 chars", () => {
      const exactInput = "z".repeat(2000);
      const entries = [createEntry({ seq: 1, tool_input: exactInput })];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      const pre = screen.getByTestId("transcript-entry-1").querySelector("pre");
      expect(pre!.textContent).toBe(exactInput);

      // No toggle button
      expect(screen.queryByTestId("show-full-input")).toBeNull();
    });

    it("expands tool_input on 'Show full input' click", () => {
      const longInput = "a".repeat(10000);
      const entries = [createEntry({ seq: 1, tool_input: longInput })];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      // Click show full input
      fireEvent.click(screen.getByTestId("show-full-input"));

      // Full text should now be rendered
      const pre = screen.getByTestId("transcript-entry-1").querySelector("pre");
      expect(pre!.textContent).toBe(longInput);

      // Button should change to "Show less"
      expect(screen.getByTestId("show-less-input")).toBeTruthy();
      expect(screen.queryByTestId("show-full-input")).toBeNull();
    });

    it("collapses tool_input on 'Show less' click", () => {
      const longInput = "b".repeat(10000);
      const entries = [createEntry({ seq: 1, tool_input: longInput })];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      // Expand
      fireEvent.click(screen.getByTestId("show-full-input"));

      // Collapse
      fireEvent.click(screen.getByTestId("show-less-input"));

      // Should be truncated again
      const pre = screen.getByTestId("transcript-entry-1").querySelector("pre");
      expect(pre!.textContent!.length).toBeLessThan(2100);
      expect(screen.getByTestId("show-full-input")).toBeTruthy();
    });

    it("does not truncate entry.content (only tool_input)", () => {
      const longContent = "c".repeat(10000);
      const entries = [
        createEntry({ seq: 1, role: "assistant", content: longContent }),
      ];

      render(
        <SessionDetailView sessionId="test" entries={entries} />,
      );

      // Content should be fully rendered
      const contentDiv = screen.getByTestId("transcript-entry-1");
      expect(contentDiv.textContent).toContain(longContent);

      // No truncation button
      expect(screen.queryByTestId("show-full-input")).toBeNull();
    });
  });

  describe("basic rendering", () => {
    it("renders session ID and entries", () => {
      const entries = [
        createEntry({ seq: 1, role: "user", content: "Hello" }),
        createEntry({
          seq: 2,
          role: "tool",
          tool_name: "Read",
          tool_input: "file.ts",
        }),
      ];

      render(
        <SessionDetailView sessionId="session-1" entries={entries} />,
      );

      expect(screen.getByText("session-1")).toBeTruthy();
      expect(screen.getByText("Hello")).toBeTruthy();
      expect(screen.getByText("Read")).toBeTruthy();
    });

    it("shows active badge when isActive", () => {
      render(
        <SessionDetailView
          sessionId="session-1"
          entries={[]}
          isActive={true}
        />,
      );

      expect(screen.getByText("Active")).toBeTruthy();
    });
  });
});
