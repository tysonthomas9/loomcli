/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalView, SearchBar, and SessionNamePrompt components.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { TerminalView } from "../TerminalView";
import { BackendPickerPrompt } from "../BackendPickerPrompt";
import { SessionNamePrompt } from "../SessionNamePrompt";

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

vi.mock("@/hooks/useTerminalMetadata", () => ({
  useTerminalMetadata: () => mockMetadataHook,
}));

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

vi.mock("@/hooks/useBackendConfig", () => ({
  useBackendConfig: () => mockBackendConfigHook,
}));

// ── Mock sibling components ──────────────────────────────────────────────────

vi.mock("../TerminalInstance", () => ({
  TerminalInstance: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => (
      <div data-testid={`terminal-instance-${props.sessionName}`}>
        TerminalInstance:{props.sessionName}
      </div>
    ),
  ),
}));

vi.mock("../TerminalTabBar", () => ({
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

// Mock CSS modules
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

vi.mock("../TerminalView.module.css", () => ({
  default: {
    container: "container",
    fullHeight: "fullHeight",
    loading: "loading",
    terminalsContainer: "terminalsContainer",
    terminalPane: "terminalPane",
    searchOverlay: "searchOverlay",
    searchInput: "searchInput",
    searchButton: "searchButton",
    searchToggle: "searchToggle",
    searchToggleActive: "searchToggleActive",
    searchCounter: "searchCounter",
    noResults: "noResults",
  },
}));

vi.mock("../BackendPickerPrompt.module.css", () => ({
  default: {
    overlay: "overlay",
    open: "open",
    modal: "modal",
    header: "header",
    title: "title",
    subtitle: "subtitle",
    content: "content",
    selectGroup: "selectGroup",
    label: "label",
    select: "select",
    loadingText: "loadingText",
    emptyText: "emptyText",
    footer: "footer",
    buttonPrimary: "buttonPrimary",
    buttonSecondary: "buttonSecondary",
  },
}));

vi.mock("../SessionNamePrompt.module.css", () => ({
  default: {
    overlay: "overlay",
    open: "open",
    modal: "modal",
    header: "header",
    title: "title",
    subtitle: "subtitle",
    content: "content",
    inputGroup: "inputGroup",
    label: "label",
    input: "input",
    inputError: "inputError",
    errorText: "errorText",
    footer: "footer",
    buttonPrimary: "buttonPrimary",
    buttonSecondary: "buttonSecondary",
  },
}));

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

    it("auto-creates tabs from backend config on first open (empty metadata)", () => {
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("tab-lead-claude-1")).toBeInTheDocument();
      expect(screen.getByTestId("tab-lead-codex-1")).toBeInTheDocument();
      expect(screen.getByTestId("tab-lead-opencode-1")).toBeInTheDocument();
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

    it("fallback tab when backend config returns empty available", () => {
      mockBackendConfigHook.config = {
        backend: "claude",
        source: "default",
        available: [],
        agents: [],
      };
      setMetadata([]);
      render(<TerminalView />);

      expect(screen.getByTestId("tab-talk-to-lead")).toBeInTheDocument();
    });

    it("createTab called for each auto-created tab on first open", () => {
      setMetadata([]);
      render(<TerminalView />);

      expect(mockMetadataHook.createTab).toHaveBeenCalledTimes(3);
      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "lead-claude-1",
        "lead-claude-1",
        0,
      );
      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "lead-codex-1",
        "lead-codex-1",
        1,
      );
      expect(mockMetadataHook.createTab).toHaveBeenCalledWith(
        "lead-opencode-1",
        "lead-opencode-1",
        2,
      );
    });

    it("createTab not called when restoring from metadata", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(mockMetadataHook.createTab).not.toHaveBeenCalled();
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

    it("clicking + when 8 tabs exist does not open prompt", () => {
      const eightTabs = Array.from({ length: 8 }, (_, i) => ({
        session_name: `s${i}`,
        label: `S${i}`,
      }));
      setMetadata(eightTabs);
      render(<TerminalView />);

      const overlay = screen.getByTestId("backend-picker-prompt-overlay");
      fireEvent.click(screen.getByTestId("new-tab-button"));

      expect(overlay).toHaveAttribute("aria-hidden", "true");
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

  // ── Search overlay ─────────────────────────────────────────────────────────

  describe("search overlay", () => {
    it("search overlay hidden by default", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(
        screen.queryByTestId("terminal-search-bar"),
      ).not.toBeInTheDocument();
    });

    it("Cmd+F toggles search overlay open", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      expect(screen.getByTestId("terminal-search-bar")).toBeInTheDocument();
    });

    it("Escape closes search overlay", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });
      expect(screen.getByTestId("terminal-search-bar")).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(
        screen.queryByTestId("terminal-search-bar"),
      ).not.toBeInTheDocument();
    });

    it("search input auto-focuses on open", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const input = screen.getByTestId("terminal-search-input");
      expect(input).toHaveFocus();
    });

    it("search bar shows case-sensitive toggle button", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const toggle = screen.getByTestId("search-toggle-case");
      expect(toggle).toBeInTheDocument();
      expect(toggle).toHaveTextContent("Aa");
      expect(toggle).toHaveAttribute("aria-pressed", "false");
    });

    it("search bar shows regex toggle button", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const toggle = screen.getByTestId("search-toggle-regex");
      expect(toggle).toBeInTheDocument();
      expect(toggle).toHaveTextContent(".*");
      expect(toggle).toHaveAttribute("aria-pressed", "false");
    });

    it("toggling case-sensitive updates aria-pressed", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const toggle = screen.getByTestId("search-toggle-case");
      fireEvent.click(toggle);

      expect(toggle).toHaveAttribute("aria-pressed", "true");
    });

    it("toggling regex updates aria-pressed", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const toggle = screen.getByTestId("search-toggle-regex");
      fireEvent.click(toggle);

      expect(toggle).toHaveAttribute("aria-pressed", "true");
    });

    it("no match counter when search input is empty", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      expect(screen.queryByText(/of/)).not.toBeInTheDocument();
      expect(screen.queryByText("No results")).not.toBeInTheDocument();
    });
  });

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

  // ── Render tests ───────────────────────────────────────────────────────────

  describe("render tests", () => {
    it("only active tab terminal pane is visible (display:flex)", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      const pane1 = screen
        .getByTestId("terminal-instance-session-1")
        .closest('[role="tabpanel"]')!;
      expect(pane1).toHaveStyle({ display: "flex" });
    });

    it("inactive tab panes have display:none", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      const pane2 = screen
        .getByTestId("terminal-instance-session-2")
        .closest('[role="tabpanel"]')!;
      expect(pane2).toHaveStyle({ display: "none" });
    });

    it('data-testid="terminal-view" present on container', () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    });
  });

  // ── Brand color mapping ─────────────────────────────────────────────────

  describe("brand color mapping", () => {
    it("tabs with lead-claude-1 session get claude brand color (#D97706)", async () => {
      setMetadata([]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const claudeTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-claude-1",
      );
      expect(claudeTab?.brandColor).toBe("#D97706");
    });

    it("tabs with lead-codex-1 session get codex brand color (#22c55e)", async () => {
      setMetadata([]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const codexTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-codex-1",
      );
      expect(codexTab?.brandColor).toBe("#22c55e");
    });

    it("talk-to-lead session uses default backend's brand color (claude -> #D97706)", async () => {
      mockBackendConfigHook.config = {
        backend: "claude",
        source: "default",
        available: [],
        agents: [],
      };
      setMetadata([]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const fallbackTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "talk-to-lead",
      );
      expect(fallbackTab?.brandColor).toBe("#D97706");
    });

    it("unknown backend names get undefined brandColor (CSS fallbacks apply)", async () => {
      // A session matching lead-{backend}-{n} with an unrecognized backend
      // leaves brandColor undefined so CSS semantic fallbacks take over
      setMetadata([{ session_name: "lead-gemini-1", label: "lead-gemini-1" }]);
      render(<TerminalView />);

      const { TerminalTabBar } = await import("../TerminalTabBar");
      const mockTabBar = vi.mocked(TerminalTabBar);
      const lastCallProps =
        mockTabBar.mock.calls[mockTabBar.mock.calls.length - 1][0];
      const geminiTab = lastCallProps.tabs.find(
        (t: { id: string }) => t.id === "lead-gemini-1",
      );
      expect(geminiTab?.brandColor).toBeUndefined();
    });
  });

  // ── Keyboard shortcuts: tab cycling ──────────────────────────────────────

  describe("keyboard shortcuts: tab cycling", () => {
    it("Ctrl+Tab switches to the next tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Default active tab is session-1
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("Ctrl+Shift+Tab switches to the previous tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to session-2 first
      fireEvent.click(screen.getByTestId("tab-session-2"));
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");

      fireEvent.keyDown(document, {
        key: "Tab",
        ctrlKey: true,
        shiftKey: true,
      });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("Ctrl+Tab wraps from last tab to first", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to session-2 (last tab)
      fireEvent.click(screen.getByTestId("tab-session-2"));
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("Ctrl+Shift+Tab wraps from first tab to last", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Active tab is session-1 (first)
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      fireEvent.keyDown(document, {
        key: "Tab",
        ctrlKey: true,
        shiftKey: true,
      });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("Alt+ArrowRight switches to the next tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      fireEvent.keyDown(document, { key: "ArrowRight", altKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("Alt+ArrowLeft switches to the previous tab", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to session-2
      fireEvent.click(screen.getByTestId("tab-session-2"));

      fireEvent.keyDown(document, { key: "ArrowLeft", altKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("Alt+ArrowRight wraps from last to first", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      // Switch to last tab
      fireEvent.click(screen.getByTestId("tab-session-2"));

      fireEvent.keyDown(document, { key: "ArrowRight", altKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("Alt+ArrowLeft wraps from first to last", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      fireEvent.keyDown(document, { key: "ArrowLeft", altKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-2");
    });

    it("Ctrl+Tab is a no-op with only one tab", () => {
      setMetadata([{ session_name: "only-session", label: "Only" }]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "only-session",
      );

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "only-session",
      );
    });

    it("Alt+ArrowRight is a no-op with only one tab", () => {
      setMetadata([{ session_name: "only-session", label: "Only" }]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "only-session",
      );

      fireEvent.keyDown(document, { key: "ArrowRight", altKey: true });

      expect(screen.getByTestId("active-tab-id").textContent).toBe(
        "only-session",
      );
    });

    it("tab cycling with 3 tabs cycles correctly forward", () => {
      setMetadata([
        { session_name: "s1", label: "S1" },
        { session_name: "s2", label: "S2" },
        { session_name: "s3", label: "S3" },
      ]);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("s1");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });
      expect(screen.getByTestId("active-tab-id").textContent).toBe("s2");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });
      expect(screen.getByTestId("active-tab-id").textContent).toBe("s3");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });
      expect(screen.getByTestId("active-tab-id").textContent).toBe("s1");
    });

    it("tab cycling does not fire when isActive=false", () => {
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView isActive={false} />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      // Should still be on session-1 — handler not registered
      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });
  });

  // ── Escape key ──────────────────────────────────────────────────────────

  describe("escape key behavior", () => {
    it("Escape calls onEscape when nothing else is open", () => {
      const onEscape = vi.fn();
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView onEscape={onEscape} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).toHaveBeenCalledTimes(1);
    });

    it("Escape does NOT call onEscape when search is open", () => {
      const onEscape = vi.fn();
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView onEscape={onEscape} />);

      // Open search overlay
      fireEvent.keyDown(document, { key: "f", metaKey: true });
      expect(screen.getByTestId("terminal-search-bar")).toBeInTheDocument();

      // Press Escape — should close search, not call onEscape
      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("Escape does NOT call onEscape when isActive=false", () => {
      const onEscape = vi.fn();
      setMetadata(DEFAULT_METADATA);
      render(<TerminalView isActive={false} onEscape={onEscape} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("Escape does nothing when onEscape is not provided", () => {
      setMetadata(DEFAULT_METADATA);
      // Should not throw
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "Escape" });

      // Just verify the component is still rendered (no crash)
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
