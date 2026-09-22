/**
 * @vitest-environment jsdom
 */

/**
 * Standalone unit tests for the TerminalHeader component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TerminalHeader } from "../TerminalHeader";

// Mock CSS module
vi.mock("../EmbeddedTerminal.module.css", () => ({
  default: {
    header: "header",
    backendLabel: "backendLabel",
    brandDot: "brandDot",
    connectionDot: "connectionDot",
    breadcrumb: "breadcrumb",
    actionBtn: "actionBtn",
    actions: "actions",
  },
}));

describe("TerminalHeader", () => {
  describe("backend display", () => {
    it("shows 'Claude' label for claude backend", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent("Claude");
    });

    it("shows 'Codex' label for codex backend", () => {
      render(
        <TerminalHeader
          backend="codex"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent("Codex");
    });

    it("shows 'OpenCode' label for opencode backend", () => {
      render(
        <TerminalHeader
          backend="opencode"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent(
        "OpenCode",
      );
    });

    it("shows raw backend string for unknown backends", () => {
      render(
        <TerminalHeader
          backend="custom-llm"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent(
        "custom-llm",
      );
    });

    it("shows brand-colored dot with correct color for claude", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const dot = screen.getByTestId("brand-dot");
      expect(dot.style.backgroundColor).toBe("rgb(217, 119, 6)");
    });

    it("uses gray color for unknown backend brand dot", () => {
      render(
        <TerminalHeader
          backend="unknown"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const dot = screen.getByTestId("brand-dot");
      expect(dot.style.backgroundColor).toBe("rgb(156, 163, 175)");
    });
  });

  describe("connection state dot", () => {
    it("reflects disconnected state", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="disconnected"
        />,
      );

      expect(screen.getByTestId("connection-dot")).toHaveAttribute(
        "data-state",
        "disconnected",
      );
    });

    it("reflects connecting state", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connecting"
        />,
      );

      expect(screen.getByTestId("connection-dot")).toHaveAttribute(
        "data-state",
        "connecting",
      );
    });

    it("reflects connected state", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("connection-dot")).toHaveAttribute(
        "data-state",
        "connected",
      );
    });
  });

  describe("worktree breadcrumb", () => {
    it("renders breadcrumb when worktreePath is provided", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/home/user/project"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("worktree-breadcrumb")).toBeInTheDocument();
    });

    it("hides breadcrumb when worktreePath is omitted", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(
        screen.queryByTestId("worktree-breadcrumb"),
      ).not.toBeInTheDocument();
    });

    it("hides breadcrumb when worktreePath is undefined", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath={undefined}
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(
        screen.queryByTestId("worktree-breadcrumb"),
      ).not.toBeInTheDocument();
    });
  });

  describe("path truncation", () => {
    it("truncates long paths to last 3 segments with ellipsis prefix", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/home/admin/projects/myorg/myrepo"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const breadcrumb = screen.getByTestId("worktree-breadcrumb");
      expect(breadcrumb).toHaveTextContent("\u2026/projects/myorg/myrepo");
    });

    it("does not truncate paths with 3 or fewer segments", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/foo/bar/baz"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const breadcrumb = screen.getByTestId("worktree-breadcrumb");
      expect(breadcrumb).toHaveTextContent("/foo/bar/baz");
    });

    it("handles trailing slashes correctly", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/a/b/c/d/e/"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const breadcrumb = screen.getByTestId("worktree-breadcrumb");
      expect(breadcrumb).toHaveTextContent("\u2026/c/d/e");
    });

    it("handles single segment path", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/root"
          agentName="a1"
          connectionState="connected"
        />,
      );

      const breadcrumb = screen.getByTestId("worktree-breadcrumb");
      expect(breadcrumb).toHaveTextContent("/root");
    });
  });

  describe("maximize button", () => {
    it("renders maximize button when onMaximize is provided", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
          onMaximize={vi.fn()}
          isMaximized={false}
        />,
      );

      expect(screen.getByTestId("maximize-btn")).toBeInTheDocument();
    });

    it("does not render maximize button when onMaximize is undefined", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.queryByTestId("maximize-btn")).not.toBeInTheDocument();
    });

    it("shows 'Maximize terminal' aria-label when not maximized", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
          onMaximize={vi.fn()}
          isMaximized={false}
        />,
      );

      expect(screen.getByTestId("maximize-btn")).toHaveAttribute(
        "aria-label",
        "Maximize terminal",
      );
    });

    it("shows 'Restore terminal' aria-label when maximized", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
          onMaximize={vi.fn()}
          isMaximized={true}
        />,
      );

      expect(screen.getByTestId("maximize-btn")).toHaveAttribute(
        "aria-label",
        "Restore terminal",
      );
    });

    it("calls onMaximize when clicked", () => {
      const onMaximize = vi.fn();
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
          onMaximize={onMaximize}
          isMaximized={false}
        />,
      );

      fireEvent.click(screen.getByTestId("maximize-btn"));
      expect(onMaximize).toHaveBeenCalledTimes(1);
    });
  });
});
