/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalView, SearchBar, and SessionNamePrompt components.
 */

import {
  render as rtlRender,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import type { RenderOptions } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";
import { KeyboardShortcutProvider } from "@/hooks/ui";

import { TerminalView } from "../TerminalView";
import { MAX_TABS } from "@/components/TerminalView/tabs/terminalTabUtils";
import {
  BackendPickerPrompt,
  SessionNamePrompt,
} from "@/components/TerminalView/layout";

// ── Mock shared state ────────────────────────────────────────────────────────

const mockMetadataHook = vi.hoisted(() => ({
  tabs: [] as Array<{
    session_name: string;
    label: string;
    notes: string;
    sort_order: number;
    created_at: string;
    updated_at: string;
  }>,
  isLoading: false,
  error: null as Error | null,
  createTab: vi.fn().mockResolvedValue(undefined),
  updateLabel: vi.fn().mockResolvedValue(undefined),
  updateNotes: vi.fn().mockResolvedValue(undefined),
  reorderTabs: vi.fn(),
  deleteTab: vi.fn().mockResolvedValue(undefined),
  refetch: vi.fn(),
  handleMutation: vi.fn(),
}));

vi.mock("@/hooks/terminal", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/terminal")>(
      "@/hooks/terminal",
    );
  return {
    ...actual,
    useTerminalMetadata: () => mockMetadataHook,
    useSessionRestore: () => mockSessionRestoreHook,
  };
});

const mockBackendConfigHook = vi.hoisted(() => ({
  config: {
    backend: "claude",
    source: "default",
    available: ["claude", "codex", "opencode"],
    agents: [],
  },
  isLoading: false,
  error: null as string | null,
  isSaving: false,
  updateBackend: vi.fn(),
  refetch: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return { ...actual, useBackendConfig: () => mockBackendConfigHook };
});

const mockSessionRestoreHook = vi.hoisted(() => ({
  activeTabId: null as string | null,
  isRestoring: true,
}));

// useSessionRestore is mocked above via @/hooks/terminal

// ── Mock sibling components ──────────────────────────────────────────────────

vi.mock("../instances/TerminalInstance", () => ({
  TerminalInstance: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => (
      <div data-testid={`terminal-instance-${props.sessionName}`}>
        TerminalInstance:{props.sessionName}
      </div>
    ),
  ),
}));

vi.mock("../tabs/TerminalTabBar", () => ({
  TerminalTabBar: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => (
      <div data-testid="terminal-tab-bar">
        {props.tabs.map((t: { id: string; label: string }) => (
          <button
            key={t.id}
            data-testid={`tab-${t.id}`}
            onClick={() => props.onTabChange(t.id)}
          >
            {t.label}
          </button>
        ))}
        <button data-testid="new-tab-button" onClick={props.onNewTab}>
          +
        </button>
        <button
          data-testid="close-tab-button"
          onClick={() => props.onTabClose(props.activeTabId)}
        >
          Close
        </button>
        <button
          data-testid="toggle-fullheight"
          onClick={props.onToggleFullHeight}
        >
          Toggle
        </button>
        <span data-testid="active-tab-id">{props.activeTabId}</span>
        <span data-testid="is-full-height">{String(props.isFullHeight)}</span>
      </div>
    ),
  ),
}));

// ── Render wrapper (provides KeyboardShortcutProvider for escape layers) ─────

const Wrapper = ({ children }: { children: React.ReactNode }) => (
  <KeyboardShortcutProvider>{children}</KeyboardShortcutProvider>
);

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, { wrapper: Wrapper, ...options });
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function setMetadata(
  tabs: Array<{
    session_name: string;
    label: string;
    notes?: string;
    sort_order?: number;
  }>,
  isLoading = false,
) {
  const now = new Date().toISOString();
  mockMetadataHook.tabs = tabs.map((t, i) => ({
    session_name: t.session_name,
    label: t.label,
    notes: t.notes ?? "",
    sort_order: t.sort_order ?? i,
    created_at: now,
    updated_at: now,
  }));
  mockMetadataHook.isLoading = isLoading;
  mockMetadataHook.error = null;
}

const DEFAULT_METADATA = [
  { session_name: "session-1", label: "Session 1" },
  { session_name: "session-2", label: "Session 2" },
];

// ── Tests: TerminalView ──────────────────────────────────────────────────────

describe("TerminalView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockMetadataHook.tabs = [];
    mockMetadataHook.isLoading = true;
    mockMetadataHook.error = null;
    mockMetadataHook.createTab = vi.fn().mockResolvedValue(undefined);
    mockBackendConfigHook.isLoading = false;
    mockBackendConfigHook.config = {
      backend: "claude",
      source: "default",
      available: ["claude", "codex", "opencode"],
      agents: [],
    };
    mockSessionRestoreHook.activeTabId = null;
    mockSessionRestoreHook.isRestoring = true;
  });

  // ── Session initialization ───────────────────────────────────────────────

  describe("session initialization", () => {
    it("shows loading state while metadata is loading", () => {
      mockMetadataHook.isLoading = true;
      render(<TerminalView />);

      expect(
        screen.getByTestId("loading-skeleton-terminal"),
      ).toBeInTheDocument();
    });

    it("restores tabs from persisted metadata once loaded", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(
        screen.queryByTestId("loading-skeleton-terminal"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("terminal-tab-bar")).toBeInTheDocument();
      expect(screen.getByText("Session 1")).toBeInTheDocument();
      expect(screen.getByText("Session 2")).toBeInTheDocument();
    });

    it("first metadata entry becomes the active tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("tab ids match session names from metadata", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("tab-session-1")).toBeInTheDocument();
      expect(screen.getByTestId("tab-session-2")).toBeInTheDocument();
    });

    it("auto-creates only default backend tab on first open (empty metadata)", () => {
      setMetadata([]);
      render(<TerminalView />);

      // Only the default backend tab is auto-created; users add others via "+"
      expect(screen.getByTestId("tab-lead-claude-1")).toBeInTheDocument();
      expect(screen.queryByTestId("tab-lead-codex-1")).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("tab-lead-opencode-1"),
      ).not.toBeInTheDocument();
    });

    it("claude tab is active by default on first open", () => {
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "lead-claude-1",
      );
    });

    it("first tab active when claude not available", () => {
      mockBackendConfigHook.config = {
        backend: "codex",
        source: "default",
        available: ["codex", "opencode"],
        agents: [],
      };
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "lead-codex-1",
      );
    });

    it("shows empty state when backend config returns empty available", () => {
      mockBackendConfigHook.config = {
        backend: "claude",
        source: "default",
        available: [],
        agents: [],
      };
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("no-backends-empty-state")).toBeInTheDocument();
    });

    it("createTab called once for default backend on first open", () => {
      setMetadata([]);
      render(<TerminalView />);

      expect(mockMetadataHook.createTab).toHaveBeenCalledTimes(1);
      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "lead-claude-1",
        "lead-claude-1",
        0,
      );
    });

    it("createTab not called when restoring from metadata", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(mockMetadataHook.createTab).not.toHaveBeenCalled();
    });

    it("does not initialize tabs when isActive is false", () => {
      setMetadata([]);
      render(<TerminalView isActive={false} />);

      // No tabs should have been created — init is deferred until view is active
      expect(mockMetadataHook.createTab).not.toHaveBeenCalled();
      expect(screen.queryByTestId("tab-lead-claude-1")).not.toBeInTheDocument();
    });

    it("initializes tabs when isActive transitions from false to true", () => {
      setMetadata([]);
      const { rerender } = render(<TerminalView isActive={false} />);

      // Should not have initialized yet
      expect(mockMetadataHook.createTab).not.toHaveBeenCalled();

      // Transition to active
      rerender(<TerminalView isActive={true} />);

      // Now tabs should appear — only default backend tab auto-created
      expect(screen.getByTestId("tab-lead-claude-1")).toBeInTheDocument();
      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "lead-claude-1",
      );
      expect(mockMetadataHook.createTab).toHaveBeenCalledTimes(1);
    });
  });

  // ── Tab sort order ─────────────────────────────────────────────────────────

  describe("tab sort order", () => {
    it("sorts restored tabs by sort_order from metadata", () => {
      setMetadata([
        { session_name: "session-a", label: "A", sort_order: 2 },
        { session_name: "session-b", label: "B", sort_order: 0 },
        { session_name: "session-c", label: "C", sort_order: 1 },
      ]);
      render(<TerminalView />);

      const tabBar = screen.getByTestId("terminal-tab-bar");
      const buttons = tabBar.querySelectorAll("button[data-testid^='tab-']");
      expect(buttons[0]).toHaveTextContent("B");
      expect(buttons[1]).toHaveTextContent("C");
      expect(buttons[2]).toHaveTextContent("A");
    });

    it("first tab in sorted order becomes active (not first in metadata)", () => {
      setMetadata([
        { session_name: "session-a", label: "A", sort_order: 2 },
        { session_name: "session-b", label: "B", sort_order: 0 },
        { session_name: "session-c", label: "C", sort_order: 1 },
      ]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-b");
    });
  });

  // ── Active tab persistence ────────────────────────────────────────────────

  describe("active tab persistence", () => {
    it("persists activeTabId to sessionStorage on tab change", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("tab-session-2"));

      expect(sessionStorage.getItem("terminal-active-tab")).toBe("session-2");
    });

    it("restores activeTabId from sessionStorage on initialization", () => {
      sessionStorage.setItem("terminal-active-tab", "session-2");
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("falls back to first tab when sessionStorage has stale ID", () => {
      sessionStorage.setItem("terminal-active-tab", "deleted-session");
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("falls back to first sorted tab when sessionStorage has stale ID", () => {
      sessionStorage.setItem("terminal-active-tab", "deleted-session");
      setMetadata([
        { session_name: "session-a", label: "A", sort_order: 2 },
        { session_name: "session-b", label: "B", sort_order: 0 },
      ]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-b");
    });

    it("does not flicker when server restore matches sessionStorage", () => {
      sessionStorage.setItem("terminal-active-tab", "session-2");
      setMetadata(DEFAULT_METADATA);

      // Start with server still restoring
      mockSessionRestoreHook.isRestoring = true;
      mockSessionRestoreHook.activeTabId = null;
      const { rerender } = render(<TerminalView />);

      // useTabInit sets session-2 from sessionStorage
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");

      // Server restore completes with same tab ID
      mockSessionRestoreHook.isRestoring = false;
      mockSessionRestoreHook.activeTabId = "session-2";
      rerender(<TerminalView />);

      // Active tab should still be session-2 (no flicker)
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("server restore wins when it differs from sessionStorage", () => {
      sessionStorage.setItem("terminal-active-tab", "session-1");
      setMetadata(DEFAULT_METADATA);

      // Start with server still restoring
      mockSessionRestoreHook.isRestoring = true;
      mockSessionRestoreHook.activeTabId = null;
      const { rerender } = render(<TerminalView />);

      // useTabInit sets session-1 from sessionStorage
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      // Server restore completes with different tab ID
      mockSessionRestoreHook.isRestoring = false;
      mockSessionRestoreHook.activeTabId = "session-2";
      rerender(<TerminalView />);

      // Server value should win
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });
  });

  // ── New tab prompt ─────────────────────────────────────────────────────────

  describe("new tab prompt", () => {
    it("clicking + opens BackendPickerPrompt modal", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      const overlay = screen.getByTestId("backend-picker-prompt-overlay");
      expect(overlay).toHaveAttribute("aria-hidden", "true");

      fireEvent.click(screen.getByTestId("new-tab-button"));

      expect(overlay).toHaveAttribute("aria-hidden", "false");
    });

    it("clicking + when max tabs exist does not open prompt", () => {
      const onTabLimitReached = vi.fn();
      const maxTabs = Array.from({ length: MAX_TABS }, (_, i) => ({
        session_name: `s${i}`,
        label: `S${i}`,
      }));
      setMetadata(maxTabs);
      render(<TerminalView onTabLimitReached={onTabLimitReached} />);

      const overlay = screen.getByTestId("backend-picker-prompt-overlay");
      fireEvent.click(screen.getByTestId("new-tab-button"));

      expect(overlay).toHaveAttribute("aria-hidden", "true");
      expect(onTabLimitReached).toHaveBeenCalledWith(
        `Maximum terminal tabs reached (${MAX_TABS}). Close a tab before opening another.`,
      );
    });

    it("selecting a backend creates a new tab with auto-generated name", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));

      const select = screen.getByTestId("backend-picker-select");
      fireEvent.change(select, { target: { value: "claude" } });
      fireEvent.submit(select.closest("form")!);

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-lead-claude-1"),
        ).toBeInTheDocument();
      });
    });

    it("new tab becomes active after creation", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));

      const select = screen.getByTestId("backend-picker-select");
      fireEvent.change(select, { target: { value: "claude" } });
      fireEvent.submit(select.closest("form")!);

      await waitFor(() => {
        expect(screen.getByTestId("active-tab-id").textContent).toBe(
          "lead-claude-1",
        );
      });
    });

    it("cancelling the prompt does not create a tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));
      fireEvent.click(screen.getByTestId("backend-picker-cancel-button"));

      expect(
        screen.queryByTestId("terminal-instance-lead-claude-1"),
      ).not.toBeInTheDocument();
      expect(
        screen.getByTestId("backend-picker-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "true");
    });

    it("creating two tabs with same backend produces sequential names", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Create first claude tab
      fireEvent.click(screen.getByTestId("new-tab-button"));
      const select = screen.getByTestId("backend-picker-select");
      fireEvent.change(select, { target: { value: "claude" } });
      fireEvent.submit(select.closest("form")!);

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-lead-claude-1"),
        ).toBeInTheDocument();
      });

      // Create second claude tab
      fireEvent.click(screen.getByTestId("new-tab-button"));
      const select2 = screen.getByTestId("backend-picker-select");
      fireEvent.change(select2, { target: { value: "claude" } });
      fireEvent.submit(select2.closest("form")!);

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-lead-claude-2"),
        ).toBeInTheDocument();
      });
    });

    it("creating tabs with different backends produces independent counters", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Create claude tab
      fireEvent.click(screen.getByTestId("new-tab-button"));
      fireEvent.change(screen.getByTestId("backend-picker-select"), {
        target: { value: "claude" },
      });
      fireEvent.submit(
        screen.getByTestId("backend-picker-select").closest("form")!,
      );

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-lead-claude-1"),
        ).toBeInTheDocument();
      });

      // Create codex tab
      fireEvent.click(screen.getByTestId("new-tab-button"));
      fireEvent.change(screen.getByTestId("backend-picker-select"), {
        target: { value: "codex" },
      });
      fireEvent.submit(
        screen.getByTestId("backend-picker-select").closest("form")!,
      );

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-lead-codex-1"),
        ).toBeInTheDocument();
      });
    });

    it("shows loading when config is still loading", () => {
      mockBackendConfigHook.isLoading = true;
      mockBackendConfigHook.config = null;
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Initialization blocked by configLoading, so loading indicator shows
      expect(
        screen.getByTestId("loading-skeleton-terminal"),
      ).toBeInTheDocument();
    });
  });

  // ── Tab management ─────────────────────────────────────────────────────────

  describe("tab management", () => {
    it("handleTabClose removes tab", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to session-2 so closing active won't interfere
      fireEvent.click(screen.getByTestId("tab-session-2"));

      // Now the active tab is session-2; close it
      fireEvent.click(screen.getByTestId("close-tab-button"));

      await waitFor(() => {
        expect(
          screen.queryByTestId("terminal-instance-session-2"),
        ).not.toBeInTheDocument();
      });
    });

    it("handleTabClose selects previous tab when closing active tab", async () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to session-2
      fireEvent.click(screen.getByTestId("tab-session-2"));
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");

      // Close session-2 (active)
      fireEvent.click(screen.getByTestId("close-tab-button"));

      await waitFor(() => {
        expect(screen.getByTestId("active-tab-id").textContent).toBe(
          "session-1",
        );
      });
    });

    it("cannot close the last remaining tab", () => {
      setMetadata([{ session_name: "only-session", label: "Only" }]);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("close-tab-button"));

      expect(
        screen.getByTestId("terminal-instance-only-session"),
      ).toBeInTheDocument();
    });
  });

  // Search overlay removed with the wterm migration — native browser
  // find-in-page (Cmd+F) operates on the DOM-rendered cells.

  // ── Full-height ────────────────────────────────────────────────────────────

  describe("full-height", () => {
    it("container does not have fullHeight class by default", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("is-full-height").textContent).toBe("false");
      expect(screen.getByTestId("terminal-view").className).not.toContain(
        "fullHeight",
      );
    });

    it("toggle adds fullHeight class", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("toggle-fullheight"));

      expect(screen.getByTestId("is-full-height").textContent).toBe("true");
      expect(screen.getByTestId("terminal-view").className).toContain(
        "fullHeight",
      );
    });
  });

  // ── Issue context (sanitizeSessionName + pendingIssueContext) ─────────────

  describe("issue context and sanitizeSessionName", () => {
    it("creates tab with dots replaced by dashes in issue ID", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "proj.sub.123",
            title: "Test issue",
          }}
          onIssueContextConsumed={vi.fn()}
        />,
      );

      // sanitizeSessionName("proj.sub.123") => "proj-sub-123"
      // tab sessionName => "issue-proj-sub-123"
      expect(
        screen.getByTestId("terminal-instance-issue-proj-sub-123"),
      ).toBeInTheDocument();
    });

    it("creates tab with special chars stripped from issue ID", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "proj@123!",
            title: "Test issue",
          }}
          onIssueContextConsumed={vi.fn()}
        />,
      );

      // sanitizeSessionName("proj@123!") => "proj123"
      // tab sessionName => "issue-proj123"
      expect(
        screen.getByTestId("terminal-instance-issue-proj123"),
      ).toBeInTheDocument();
    });

    it("creates tab preserving hyphens and underscores in issue ID", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "proj-sub_123",
            title: "Test issue",
          }}
          onIssueContextConsumed={vi.fn()}
        />,
      );

      expect(
        screen.getByTestId("terminal-instance-issue-proj-sub_123"),
      ).toBeInTheDocument();
    });

    it("new issue tab becomes active", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "PROJ-42",
            title: "New feature",
          }}
          onIssueContextConsumed={vi.fn()}
        />,
      );

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "issue-PROJ-42",
      );
    });

    it("calls onIssueContextConsumed after creating tab", () => {
      setMetadata(DEFAULT_METADATA);
      const onConsumed = vi.fn();
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "PROJ-42",
            title: "New feature",
          }}
          onIssueContextConsumed={onConsumed}
        />,
      );

      expect(onConsumed).toHaveBeenCalled();
    });

    it("switches to existing tab if issue tab already exists", () => {
      setMetadata([
        ...DEFAULT_METADATA,
        { session_name: "issue-PROJ-42", label: "issue-PROJ-42" },
      ]);
      const onConsumed = vi.fn();
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "PROJ-42",
            title: "Existing issue",
          }}
          onIssueContextConsumed={onConsumed}
        />,
      );

      // Should switch to existing tab, not create a new one
      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "issue-PROJ-42",
      );
      expect(onConsumed).toHaveBeenCalled();
    });

    it("persists tab metadata for new issue tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingIssueContext={{
            issue_id: "PROJ-99",
            title: "Persist test",
          }}
          onIssueContextConsumed={vi.fn()}
        />,
      );

      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "issue-PROJ-99",
        "issue-PROJ-99",
        expect.any(Number),
      );
    });
  });

  // ── pendingAgentName (V7 Terminal View) ───────────────────────────────────

  describe("pendingAgentName", () => {
    it("creates agent tab with correct session name pattern", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView pendingAgentName="fox" onAgentNameConsumed={vi.fn()} />,
      );

      // sanitizeSessionName("fox") => "fox"
      // tab sessionName => "agent-fox"
      expect(
        screen.getByTestId("terminal-instance-agent-fox"),
      ).toBeInTheDocument();
    });

    it("new agent tab becomes active", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView pendingAgentName="fox" onAgentNameConsumed={vi.fn()} />,
      );

      expect(screen.getByTestId("active-tab-id").textContent).toBe("agent-fox");
    });

    it("calls onAgentNameConsumed after creating agent tab", () => {
      setMetadata(DEFAULT_METADATA);
      const onConsumed = vi.fn();
      render(
        <TerminalView
          pendingAgentName="fox"
          onAgentNameConsumed={onConsumed}
        />,
      );

      expect(onConsumed).toHaveBeenCalled();
    });

    it("switches to existing tab if agent tab already exists", () => {
      setMetadata([
        ...DEFAULT_METADATA,
        { session_name: "agent-fox", label: "agent-fox" },
      ]);
      const onConsumed = vi.fn();
      render(
        <TerminalView
          pendingAgentName="fox"
          onAgentNameConsumed={onConsumed}
        />,
      );

      // Should switch to existing tab, not create a new one
      expect(screen.getByTestId("active-tab-id").textContent).toBe("agent-fox");
      expect(onConsumed).toHaveBeenCalled();
    });

    it("persists tab metadata for new agent tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView pendingAgentName="fox" onAgentNameConsumed={vi.fn()} />,
      );

      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "agent-fox",
        "agent-fox",
        expect.any(Number),
      );
    });

    it("sanitizes agent name with dots in session name", () => {
      setMetadata(DEFAULT_METADATA);
      render(
        <TerminalView
          pendingAgentName="agent.alpha"
          onAgentNameConsumed={vi.fn()}
        />,
      );

      // sanitizeSessionName("agent.alpha") => "agent-alpha"
      expect(
        screen.getByTestId("terminal-instance-agent-agent-alpha"),
      ).toBeInTheDocument();
    });

    it("does not create agent tab when no pendingAgentName is provided", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // No agent tab should exist
      expect(
        screen.queryByTestId("terminal-instance-agent-fox"),
      ).not.toBeInTheDocument();
    });
  });

  // ── Render tests ───────────────────────────────────────────────────────────

  describe("render tests", () => {
    it("only active tab terminal pane is visible", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      const pane1 = screen
        .getByTestId("terminal-instance-session-1")
        .closest('[role="tabpanel"]')!;
      expect(pane1).toHaveStyle({
        visibility: "visible",
        position: "relative",
      });
    });

    it("inactive tab panes are hidden", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      const pane2 = screen
        .getByTestId("terminal-instance-session-2")
        .closest('[role="tabpanel"]')!;
      expect(pane2).toHaveStyle({ visibility: "hidden", position: "absolute" });
    });

    it('data-testid="terminal-view" present on container', () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    });
  });

  // ── Brand color mapping ─────────────────────────────────────────────────

  describe("brand color mapping", () => {
    it("tabs with lead-claude-1 session get claude brand color (#d4a574)", async () => {
      setMetadata([]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../tabs/TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const claudeTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-claude-1",
      );
      expect(claudeTab?.brandColor).toBe("#d4a574");
    });

    it("tabs with lead-codex-1 session get codex brand color (#10a37f)", async () => {
      // Use metadata to restore a codex tab (auto-creation only creates default backend)
      setMetadata([
        {
          session_name: "lead-codex-1",
          label: "lead-codex-1",
          notes: "",
          sort_order: 0,
          created_at: "2024-01-01T00:00:00Z",
          updated_at: "2024-01-01T00:00:00Z",
        },
      ]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../tabs/TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const codexTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-codex-1",
      );
      expect(codexTab?.brandColor).toBe("#10a37f");
    });

    it("shows empty state when no backends are available", () => {
      mockBackendConfigHook.config = {
        backend: "claude",
        source: "default",
        available: [],
        agents: [],
      };
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("no-backends-empty-state")).toBeInTheDocument();
    });

    it("gemini backend gets correct brand color (#8e24aa)", async () => {
      setMetadata([{ session_name: "lead-gemini-1", label: "lead-gemini-1" }]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../tabs/TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const geminiTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-gemini-1",
      );
      expect(geminiTab?.brandColor).toBe("#8e24aa");
    });

    it("unknown backend names get undefined brandColor (CSS fallbacks apply)", async () => {
      setMetadata([{ session_name: "lead-foobar-1", label: "lead-foobar-1" }]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../tabs/TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const foobarTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-foobar-1",
      );
      expect(foobarTab?.brandColor).toBeUndefined();
    });
  });

  // ── Escape key ──────────────────────────────────────────────────────────

  describe("escape key behavior", () => {
    it("Escape does not leave the terminal view", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    });
  });
});

// ── Tests: SessionNamePrompt ─────────────────────────────────────────────────

describe("SessionNamePrompt", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("auto-focuses input on open", async () => {
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={[]}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    expect(screen.getByTestId("session-name-input")).toHaveFocus();
  });

  it("Escape key calls onCancel", () => {
    const onCancel = vi.fn();
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={[]}
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("form submission calls onConfirm with trimmed name", () => {
    const onConfirm = vi.fn();
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={[]}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    );

    const input = screen.getByTestId("session-name-input");
    fireEvent.change(input, { target: { value: "  my-session  " } });
    fireEvent.submit(input.closest("form")!);

    expect(onConfirm).toHaveBeenCalledWith("my-session");
  });

  it("validates against regex pattern (shows error for invalid chars)", () => {
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={[]}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const input = screen.getByTestId("session-name-input");
    fireEvent.change(input, { target: { value: "invalid name!" } });

    expect(screen.getByTestId("session-name-error")).toHaveTextContent(
      "Only letters, numbers, hyphens, and underscores are allowed",
    );
  });

  it("shows duplicate error when name is in existingNames", () => {
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={["taken"]}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const input = screen.getByTestId("session-name-input");
    fireEvent.change(input, { target: { value: "taken" } });

    expect(screen.getByTestId("session-name-error")).toHaveTextContent(
      "Session already exists",
    );
  });

  it("submit button disabled for empty/invalid/duplicate names", () => {
    render(
      <SessionNamePrompt
        isOpen={true}
        existingNames={["existing"]}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const submitBtn = screen.getByTestId("session-name-confirm-button");
    const input = screen.getByTestId("session-name-input");

    // Empty
    expect(submitBtn).toBeDisabled();

    // Invalid chars
    fireEvent.change(input, { target: { value: "bad name!" } });
    expect(submitBtn).toBeDisabled();

    // Duplicate
    fireEvent.change(input, { target: { value: "existing" } });
    expect(submitBtn).toBeDisabled();

    // Valid
    fireEvent.change(input, { target: { value: "valid-name" } });
    expect(submitBtn).not.toBeDisabled();
  });
});

// ── Tests: BackendPickerPrompt ───────────────────────────────────────────────

describe("BackendPickerPrompt", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders dropdown with all available backends", () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={["claude", "codex", "opencode"]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const select = screen.getByTestId("backend-picker-select");
    const options = select.querySelectorAll("option");

    expect(options).toHaveLength(3);
    expect(options[0]).toHaveTextContent("Claude");
    expect(options[1]).toHaveTextContent("Codex");
    expect(options[2]).toHaveTextContent("OpenCode");
  });

  it("calls onSelect with correct backend when Create is clicked", async () => {
    const onSelect = vi.fn();
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={["claude", "codex", "opencode"]}
        isLoading={false}
        onSelect={onSelect}
        onCancel={vi.fn()}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    const select = screen.getByTestId("backend-picker-select");
    fireEvent.change(select, { target: { value: "codex" } });
    fireEvent.click(screen.getByTestId("backend-picker-create-button"));

    expect(onSelect).toHaveBeenCalledWith("codex");
  });

  it("calls onCancel when Cancel is clicked", () => {
    const onCancel = vi.fn();
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={["claude", "codex"]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={onCancel}
      />,
    );

    fireEvent.click(screen.getByTestId("backend-picker-cancel-button"));

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when Escape is pressed", () => {
    const onCancel = vi.fn();
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={["claude", "codex"]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={onCancel}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("shows loading state when isLoading=true", () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={[]}
        isLoading={true}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("backend-picker-loading")).toBeInTheDocument();
    expect(screen.getByTestId("backend-picker-loading")).toHaveTextContent(
      "Loading backends...",
    );
    expect(
      screen.queryByTestId("backend-picker-select"),
    ).not.toBeInTheDocument();
  });

  it("shows empty message when no backends available", () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={[]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("backend-picker-empty")).toBeInTheDocument();
    expect(screen.getByTestId("backend-picker-empty")).toHaveTextContent(
      "No backends available",
    );
    expect(
      screen.queryByTestId("backend-picker-select"),
    ).not.toBeInTheDocument();
  });

  it("Create button is disabled when loading", () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={[]}
        isLoading={true}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("backend-picker-create-button")).toBeDisabled();
  });

  it("Create button is disabled when no backends available", () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={[]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("backend-picker-create-button")).toBeDisabled();
  });

  it("dropdown defaults to first available backend", async () => {
    render(
      <BackendPickerPrompt
        isOpen={true}
        availableBackends={["codex", "claude", "opencode"]}
        isLoading={false}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await act(async () => {
      await vi.runAllTimersAsync();
    });

    const select = screen.getByTestId(
      "backend-picker-select",
    ) as HTMLSelectElement;
    expect(select.value).toBe("codex");
  });
});
