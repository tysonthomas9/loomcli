/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the NotesBar component.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { NotesBar } from "../NotesBar";

// Mock CSS module
vi.mock("../NotesBar.module.css", () => ({
  default: {
    notesBar: "notesBar",
    collapsed: "collapsed",
    noteIcon: "noteIcon",
    summaryText: "summaryText",
    placeholder: "placeholder",
    expanded: "expanded",
    textarea: "textarea",
    hint: "hint",
    savingIndicator: "savingIndicator",
  },
}));

describe("NotesBar", () => {
  let onSave: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    onSave = vi.fn().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // ── Collapsed state ───────────────────────────────────────────────────────

  describe("collapsed state", () => {
    it("renders collapsed with notes text", () => {
      render(<NotesBar notes="Planning auth rewrite" onSave={onSave} />);

      expect(screen.getByTestId("notes-bar-summary")).toHaveTextContent(
        "Planning auth rewrite",
      );
    });

    it('renders "Add notes..." placeholder when notes is empty', () => {
      render(<NotesBar notes="" onSave={onSave} />);

      const summary = screen.getByTestId("notes-bar-summary");
      expect(summary).toHaveTextContent("Add notes...");
      expect(summary).toHaveClass("placeholder");
    });

    it("data-testid attributes present", () => {
      render(<NotesBar notes="" onSave={onSave} />);

      expect(screen.getByTestId("notes-bar")).toBeInTheDocument();
      expect(screen.getByTestId("notes-bar-summary")).toBeInTheDocument();
    });
  });

  // ── Expanding ─────────────────────────────────────────────────────────────

  describe("expanding", () => {
    it("clicking collapsed bar expands to show textarea", () => {
      render(<NotesBar notes="Some notes" onSave={onSave} />);

      expect(
        screen.queryByTestId("notes-bar-textarea"),
      ).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);

      expect(screen.getByTestId("notes-bar-textarea")).toBeInTheDocument();
    });

    it("textarea auto-focuses on expand", () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);

      expect(screen.getByTestId("notes-bar-textarea")).toHaveFocus();
    });

    it("textarea contains current notes text", () => {
      render(<NotesBar notes="Existing note" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);

      expect(screen.getByTestId("notes-bar-textarea")).toHaveValue(
        "Existing note",
      );
    });
  });

  // ── Escape ────────────────────────────────────────────────────────────────

  describe("escape", () => {
    it("reverts draft and collapses without saving", () => {
      render(<NotesBar notes="Original" onSave={onSave} />);

      // Expand
      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      // Modify
      fireEvent.change(textarea, { target: { value: "Modified" } });

      // Escape
      fireEvent.keyDown(textarea, { key: "Escape" });

      // Should be collapsed again with original text
      expect(
        screen.queryByTestId("notes-bar-textarea"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("notes-bar-summary")).toHaveTextContent(
        "Original",
      );
      expect(onSave).not.toHaveBeenCalled();
    });
  });

  // ── Cmd/Ctrl+Enter ────────────────────────────────────────────────────────

  describe("Cmd/Ctrl+Enter", () => {
    it("saves and collapses on Cmd+Enter", async () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "New note" } });

      await act(async () => {
        fireEvent.keyDown(textarea, { key: "Enter", metaKey: true });
      });

      expect(onSave).toHaveBeenCalledWith("New note");
      expect(
        screen.queryByTestId("notes-bar-textarea"),
      ).not.toBeInTheDocument();
    });

    it("saves and collapses on Ctrl+Enter", async () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "New note" } });

      await act(async () => {
        fireEvent.keyDown(textarea, { key: "Enter", ctrlKey: true });
      });

      expect(onSave).toHaveBeenCalledWith("New note");
    });
  });

  // ── Blur save ─────────────────────────────────────────────────────────────

  describe("blur save", () => {
    it("triggers save when draft differs from notes", async () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "Changed" } });

      await act(async () => {
        fireEvent.blur(textarea);
      });

      expect(onSave).toHaveBeenCalledWith("Changed");
    });

    it("does NOT trigger save when draft matches notes", async () => {
      render(<NotesBar notes="Same text" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);

      await act(async () => {
        fireEvent.blur(screen.getByTestId("notes-bar-textarea"));
      });

      expect(onSave).not.toHaveBeenCalled();
    });
  });

  // ── Debounce ──────────────────────────────────────────────────────────────

  describe("debounce", () => {
    it("typing triggers save after 1 second of inactivity", async () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "Debounced text" } });

      // Not saved yet
      expect(onSave).not.toHaveBeenCalled();

      // Advance past debounce
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });

      expect(onSave).toHaveBeenCalledWith("Debounced text");
    });

    it("rapid typing resets the timer (only one save)", async () => {
      render(<NotesBar notes="" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "A" } });
      await act(async () => {
        vi.advanceTimersByTime(500);
      });

      fireEvent.change(textarea, { target: { value: "AB" } });
      await act(async () => {
        vi.advanceTimersByTime(500);
      });

      fireEvent.change(textarea, { target: { value: "ABC" } });
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });

      // Should only have been called once with the final value
      expect(onSave).toHaveBeenCalledTimes(1);
      expect(onSave).toHaveBeenCalledWith("ABC");
    });
  });

  // ── Prop sync ─────────────────────────────────────────────────────────────

  describe("prop sync", () => {
    it("updates draft when collapsed and notes prop changes", () => {
      const { rerender } = render(<NotesBar notes="Initial" onSave={onSave} />);

      expect(screen.getByTestId("notes-bar-summary")).toHaveTextContent(
        "Initial",
      );

      rerender(<NotesBar notes="Updated from SSE" onSave={onSave} />);

      expect(screen.getByTestId("notes-bar-summary")).toHaveTextContent(
        "Updated from SSE",
      );
    });

    it("does NOT update draft when expanded (editing in progress)", () => {
      const { rerender } = render(<NotesBar notes="Initial" onSave={onSave} />);

      // Expand
      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");
      fireEvent.change(textarea, { target: { value: "My edit" } });

      // SSE sync arrives
      rerender(<NotesBar notes="SSE update" onSave={onSave} />);

      // Draft should still be "My edit", not "SSE update"
      expect(screen.getByTestId("notes-bar-textarea")).toHaveValue("My edit");
    });
  });

  // ── Empty notes ───────────────────────────────────────────────────────────

  describe("empty notes", () => {
    it("saving empty string clears notes", async () => {
      render(<NotesBar notes="Something" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("notes-bar-summary").parentElement!);
      const textarea = screen.getByTestId("notes-bar-textarea");

      fireEvent.change(textarea, { target: { value: "" } });

      await act(async () => {
        fireEvent.keyDown(textarea, { key: "Enter", metaKey: true });
      });

      expect(onSave).toHaveBeenCalledWith("");
    });
  });

  // ── Loading state ─────────────────────────────────────────────────────────

  describe("loading state", () => {
    it("shows loading indicator when isLoading is true", () => {
      render(<NotesBar notes="" onSave={onSave} isLoading={true} />);

      expect(screen.getByText("Loading...")).toBeInTheDocument();
    });
  });
});
