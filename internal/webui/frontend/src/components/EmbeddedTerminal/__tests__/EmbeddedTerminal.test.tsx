/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EmbeddedTerminal and TerminalHeader components.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { ConnectionState } from "@/components/TerminalView/instances";
import type { UseGitActionsReturn } from "@/hooks/workspace";

import { TerminalHeader } from "../TerminalHeader";

// ── Hoisted mock state ────────────────────────────────────────────────────────

const hoisted = vi.hoisted(() => {
  let _capturedOnConnectionStateChange:
    | ((state: "disconnected" | "connecting" | "connected") => void)
    | undefined;

  const mockGitActions: UseGitActionsReturn = {
    push: vi.fn(),
    pull: vi.fn(),
    sync: vi.fn(),
    createPR: vi.fn(),
    reset: vi.fn(),
    updateTarget: vi.fn(),
    pushState: { isLoading: false, error: null },
    pullState: { isLoading: false, error: null },
    syncState: { isLoading: false, error: null },
    prState: { isLoading: false, error: null },
    resetState: { isLoading: false, error: null },
    targetState: { isLoading: false, error: null },
    anyLoading: false,
  };

  const mockUseGitActions = vi.fn(() => mockGitActions);
  const mockShowToast = vi.fn();
  const mockUseToast = vi.fn(() => ({ showToast: mockShowToast }));

  return {
    get capturedOnConnectionStateChange() {
      return _capturedOnConnectionStateChange;
    },
    set capturedOnConnectionStateChange(v) {
      _capturedOnConnectionStateChange = v;
    },
    mockGitActions,
    mockUseGitActions,
    mockShowToast,
    mockUseToast,
  };
});

// ── Mocks ─────────────────────────────────────────────────────────────────────

// Mock TerminalInstance since it uses the terminal renderer / WebSocket
vi.mock("@/components/TerminalView/instances/TerminalInstance", () => ({
  TerminalInstance: vi.fn(
    ({
      sessionName,
      isActive,
      backendName,
      onConnectionStateChange,
    }: {
      sessionName: string;
      isActive: boolean;
      backendName?: string;
      onConnectionStateChange?: (state: ConnectionState) => void;
    }) => {
      hoisted.capturedOnConnectionStateChange = onConnectionStateChange;
      return (
        <div
          data-testid="terminal-instance"
          data-session-name={sessionName}
          data-is-active={String(isActive)}
          data-backend-name={backendName}
        />
      );
    },
  ),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return { ...actual, useGitActions: hoisted.mockUseGitActions };
});

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return { ...actual, useToast: hoisted.mockUseToast };
});

// ── Lazy import after mocks are set up ────────────────────────────────────────

// EmbeddedTerminal must be imported after vi.mock declarations so the mocks
// are applied when the module is first evaluated.
const { EmbeddedTerminal } = await import("../EmbeddedTerminal");

// ── Helpers ───────────────────────────────────────────────────────────────────

function defaultGitActions(
  overrides: Partial<UseGitActionsReturn> = {},
): UseGitActionsReturn {
  return { ...hoisted.mockGitActions, ...overrides };
}

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  hoisted.capturedOnConnectionStateChange = undefined;
  hoisted.mockUseGitActions.mockReturnValue(defaultGitActions());
});

describe("EmbeddedTerminal", () => {
  it("renders TerminalInstance with correct sessionName and isActive props", () => {
    render(
      <EmbeddedTerminal
        sessionName="sess-abc"
        backend="claude"
        agentName="agent-1"
        isActive={true}
      />,
    );

    const instance = screen.getByTestId("terminal-instance");
    expect(instance).toBeInTheDocument();
    expect(instance).toHaveAttribute("data-session-name", "sess-abc");
    expect(instance).toHaveAttribute("data-is-active", "true");
    expect(instance).toHaveAttribute("data-backend-name", "claude");
  });

  it("passes isActive=false through to TerminalInstance", () => {
    render(
      <EmbeddedTerminal
        sessionName="sess-xyz"
        backend="codex"
        agentName={null}
        isActive={false}
      />,
    );

    const instance = screen.getByTestId("terminal-instance");
    expect(instance).toHaveAttribute("data-is-active", "false");
    expect(instance).toHaveAttribute("data-backend-name", "codex");
  });

  it("updates connection state dot when onConnectionStateChange fires", () => {
    render(
      <EmbeddedTerminal
        sessionName="sess-1"
        backend="claude"
        agentName="agent-1"
        isActive={true}
      />,
    );

    // Initial state is "disconnected"
    const dot = screen.getByTestId("connection-dot");
    expect(dot).toHaveAttribute("data-state", "disconnected");

    // Simulate TerminalInstance calling back with "connecting"
    expect(hoisted.capturedOnConnectionStateChange).toBeDefined();
    act(() => {
      hoisted.capturedOnConnectionStateChange!("connecting");
    });

    expect(dot).toHaveAttribute("data-state", "connecting");

    // Then "connected"
    act(() => {
      hoisted.capturedOnConnectionStateChange!("connected");
    });
    expect(dot).toHaveAttribute("data-state", "connected");
  });
});

describe("TerminalHeader", () => {
  describe("backend display", () => {
    it("shows display name and brand-colored dot for 'claude'", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent("Claude");
      const brandDot = screen.getByTestId("brand-dot");
      expect(brandDot.style.backgroundColor).toBe("rgb(217, 119, 6)");
    });

    it("shows display name and brand-colored dot for 'codex'", () => {
      render(
        <TerminalHeader
          backend="codex"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent("Codex");
      const brandDot = screen.getByTestId("brand-dot");
      expect(brandDot.style.backgroundColor).toBe("rgb(16, 185, 129)");
    });

    it("shows display name and brand-colored dot for 'opencode'", () => {
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
      const brandDot = screen.getByTestId("brand-dot");
      expect(brandDot.style.backgroundColor).toBe("rgb(99, 102, 241)");
    });

    it("shows raw backend string with gray dot for unknown backends", () => {
      render(
        <TerminalHeader
          backend="my-custom-backend"
          agentName="a1"
          connectionState="connected"
        />,
      );

      expect(screen.getByTestId("terminal-header")).toHaveTextContent(
        "my-custom-backend",
      );
      const brandDot = screen.getByTestId("brand-dot");
      expect(brandDot.style.backgroundColor).toBe("rgb(156, 163, 175)");
    });
  });

  describe("worktree breadcrumb", () => {
    it("renders breadcrumb when worktreePath is provided", () => {
      render(
        <TerminalHeader
          backend="claude"
          worktreePath="/home/user/projects/myapp"
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

  describe("git action buttons", () => {
    it("renders git action buttons when agentName is non-null and gitActions provided", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={defaultGitActions()}
        />,
      );

      expect(screen.getByTestId("git-actions")).toBeInTheDocument();
      expect(screen.getByTestId("action-review-changes")).toBeInTheDocument();
      expect(screen.getByTestId("action-merge")).toBeInTheDocument();
    });

    it("does NOT render git action buttons when agentName is null", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName={null}
          connectionState="connected"
        />,
      );

      expect(screen.queryByTestId("git-actions")).not.toBeInTheDocument();
    });

    it("does NOT render git action buttons when gitActions is undefined", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={undefined}
        />,
      );

      expect(screen.queryByTestId("git-actions")).not.toBeInTheDocument();
    });

    it("disables buttons when gitActions.anyLoading is true", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={defaultGitActions({ anyLoading: true })}
        />,
      );

      expect(screen.getByTestId("action-review-changes")).toBeDisabled();
      expect(screen.getByTestId("action-merge")).toBeDisabled();
    });

    it("enables buttons when gitActions.anyLoading is false", () => {
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={defaultGitActions({ anyLoading: false })}
        />,
      );

      expect(screen.getByTestId("action-review-changes")).toBeEnabled();
      expect(screen.getByTestId("action-merge")).toBeEnabled();
    });

    it("calls gitActions.sync when Review Changes is clicked", () => {
      const syncFn = vi.fn();
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={defaultGitActions({ sync: syncFn })}
        />,
      );

      fireEvent.click(screen.getByTestId("action-review-changes"));
      expect(syncFn).toHaveBeenCalledTimes(1);
    });

    it("calls gitActions.push when Merge is clicked", () => {
      const pushFn = vi.fn();
      render(
        <TerminalHeader
          backend="claude"
          agentName="agent-1"
          connectionState="connected"
          gitActions={defaultGitActions({ push: pushFn })}
        />,
      );

      fireEvent.click(screen.getByTestId("action-merge"));
      expect(pushFn).toHaveBeenCalledTimes(1);
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
      // 5 segments: home, admin, projects, myorg, myrepo -> last 3 with ".../"
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

    it("handles paths with trailing slashes correctly", () => {
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

    it("shows 'Maximize terminal' label when isMaximized is false", () => {
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

    it("shows 'Restore terminal' label when isMaximized is true", () => {
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

    it("calls onMaximize callback when clicked", () => {
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

  describe("connection state dot", () => {
    it("shows data-state=disconnected", () => {
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

    it("shows data-state=connecting", () => {
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

    it("shows data-state=connected", () => {
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
});
