/**
 * @vitest-environment jsdom
 */

/**
 * Tests for handleConnectionStateChange — specifically the stale closure bug
 * where tabs.find() used a captured tabs array instead of tabsRef.current.
 */

import { render as rtlRender, act } from "@testing-library/react";
import type { RenderOptions } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { KeyboardShortcutProvider } from "@/hooks/useKeyboardShortcuts";

import { TerminalView } from "../TerminalView";

// ── Mock @/api/terminal ─────────────────────────────────────────────────────

const mockSeedTerminalSession = vi.fn().mockResolvedValue(undefined);

vi.mock("@/api/terminal", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/terminal")>();
  return {
    ...actual,
    seedTerminalSession: (...args: unknown[]) =>
      mockSeedTerminalSession(...args),
    spawnTerminalSession: vi.fn().mockResolvedValue(undefined),
    patchTerminalState: vi.fn().mockResolvedValue(undefined),
    restartTerminalSession: vi.fn().mockResolvedValue(undefined),
    fetchTerminalToken: vi.fn().mockResolvedValue({ token: "fake" }),
    getExportUrl: vi.fn().mockReturnValue("/export"),
    closeAllSessions: vi.fn().mockResolvedValue(undefined),
    fetchScrollback: vi.fn().mockResolvedValue(""),
    getTerminalState: vi.fn().mockResolvedValue({}),
    listTerminalSessions: vi.fn().mockResolvedValue([]),
    killTerminalSession: vi.fn().mockResolvedValue(undefined),
    getSessionStatus: vi.fn().mockResolvedValue({ status: "running" }),
    listTabMetadata: vi.fn().mockResolvedValue([]),
    getTabMetadata: vi.fn().mockResolvedValue(null),
    patchTabMetadata: vi.fn().mockResolvedValue(undefined),
    putTabMetadata: vi.fn().mockResolvedValue(undefined),
    deleteTabMetadata: vi.fn().mockResolvedValue(undefined),
    scheduleSessionKill: vi.fn().mockResolvedValue(undefined),
    listSessionsByIssue: vi.fn().mockResolvedValue([]),
    getScrollbackInfo: vi.fn().mockResolvedValue(null),
    buildTerminalWsUrl: vi.fn().mockReturnValue("ws://localhost/ws"),
  };
});

// ── Mock useTerminalMetadata ────────────────────────────────────────────────

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
  updatePinned: vi.fn().mockResolvedValue(undefined),
  reorderTabs: vi.fn(),
  deleteTab: vi.fn().mockResolvedValue(undefined),
  linkToIssue: vi.fn().mockResolvedValue(undefined),
  refetch: vi.fn(),
  handleMutation: vi.fn(),
}));

vi.mock("@/hooks/useTerminalMetadata", () => ({
  useTerminalMetadata: () => mockMetadataHook,
}));

// ── Mock useBackendConfig ───────────────────────────────────────────────────

const mockBackendConfigHook = vi.hoisted(() => ({
  config: {
    backend: "claude",
    source: "default",
    available: ["claude"],
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

// ── Mock TerminalInstance to capture onConnectionStateChange ─────────────────

// eslint-disable-next-line @typescript-eslint/no-explicit-any
let capturedInstanceProps: Map<string, any> = new Map();

vi.mock("../TerminalInstance", () => ({
  TerminalInstance: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => {
      capturedInstanceProps.set(props.sessionName, props);
      return (
        <div data-testid={`terminal-instance-${props.sessionName}`}>
          TerminalInstance:{props.sessionName}
        </div>
      );
    },
  ),
}));

// ── Mock TerminalTabBar ─────────────────────────────────────────────────────

vi.mock("../TerminalTabBar", () => ({
  TerminalTabBar: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => (
      <div data-testid="terminal-tab-bar">
        {props.tabs.map((t: { id: string; label: string }) => (
          <button key={t.id} data-testid={`tab-${t.id}`}>
            {t.label}
          </button>
        ))}
      </div>
    ),
  ),
}));

// ── Mock CSS modules ────────────────────────────────────────────────────────

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

// ── Render wrapper ─────────────────────────────────────────────────────────

const Wrapper = ({ children }: { children: React.ReactNode }) => (
  <KeyboardShortcutProvider>{children}</KeyboardShortcutProvider>
);

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, { wrapper: Wrapper, ...options });
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function setMetadata(
  tabs: Array<{
    session_name: string;
    label: string;
    notes?: string;
    sort_order?: number;
  }>,
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
  mockMetadataHook.isLoading = false;
  mockMetadataHook.error = null;
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe("handleConnectionStateChange", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    capturedInstanceProps = new Map();
    mockMetadataHook.tabs = [];
    mockMetadataHook.isLoading = true;
    mockMetadataHook.error = null;
    mockMetadataHook.createTab = vi.fn().mockResolvedValue(undefined);
    mockBackendConfigHook.isLoading = false;
    mockBackendConfigHook.config = {
      backend: "claude",
      source: "default",
      available: ["claude"],
      agents: [],
    };
  });

  it("consumes pendingSeedRef when new issue tab connects (stale closure fix)", async () => {
    // Start with one existing tab
    setMetadata([{ session_name: "lead-claude-1", label: "Session 1" }]);

    const issueContext = {
      issue_id: "TEST-123",
      title: "Test issue",
      description: "Test description",
    };

    const onConsumed = vi.fn();

    // Render with initial tab
    const { rerender } = render(<TerminalView />);

    // Clear captured props from initial render
    capturedInstanceProps.clear();

    // Re-render with pendingIssueContext to create a new issue tab
    await act(async () => {
      rerender(
        <TerminalView
          pendingIssueContext={issueContext}
          onIssueContextConsumed={onConsumed}
        />,
      );
    });

    // The new issue tab should now exist and have a TerminalInstance
    const issueSessionName = "issue-TEST-123";
    expect(capturedInstanceProps.has(issueSessionName)).toBe(true);

    // Simulate the WebSocket connect event on the new tab.
    // This is the critical test: the callback must find the new tab via tabsRef.current,
    // not the stale `tabs` closure from when the callback was memoized.
    const issueProps = capturedInstanceProps.get(issueSessionName);
    await act(async () => {
      issueProps.onConnectionStateChange("connected", true);
    });

    // seedTerminalSession should have been called with the correct context
    expect(mockSeedTerminalSession).toHaveBeenCalledTimes(1);
    expect(mockSeedTerminalSession).toHaveBeenCalledWith(
      expect.any(String), // workspaceId
      issueSessionName,
      issueContext,
    );

    // onIssueContextConsumed should have been called
    expect(onConsumed).toHaveBeenCalled();
  });

  it("does not re-seed a session that was already seeded", async () => {
    setMetadata([{ session_name: "lead-claude-1", label: "Session 1" }]);

    const issueContext = {
      issue_id: "TEST-456",
      title: "Test issue 2",
      description: "Another test",
    };

    const { rerender } = render(<TerminalView />);
    capturedInstanceProps.clear();

    await act(async () => {
      rerender(
        <TerminalView
          pendingIssueContext={issueContext}
          onIssueContextConsumed={vi.fn()}
        />,
      );
    });

    const issueSessionName = "issue-TEST-456";
    const issueProps = capturedInstanceProps.get(issueSessionName);

    // First connect — should seed
    await act(async () => {
      issueProps.onConnectionStateChange("connected", true);
    });
    expect(mockSeedTerminalSession).toHaveBeenCalledTimes(1);

    // Refresh props after re-render
    const updatedProps = capturedInstanceProps.get(issueSessionName);

    // Second connect (reconnect) — should NOT seed again
    await act(async () => {
      updatedProps.onConnectionStateChange("connected", true);
    });
    expect(mockSeedTerminalSession).toHaveBeenCalledTimes(1);
  });

  it("does not seed when no pendingSeedRef exists for the session", async () => {
    // Render with a normal tab (no issue context)
    setMetadata([{ session_name: "lead-claude-1", label: "Session 1" }]);

    render(<TerminalView />);

    const props = capturedInstanceProps.get("lead-claude-1");
    expect(props).toBeDefined();

    // Simulate connect on a tab that has no pending seed context
    await act(async () => {
      props.onConnectionStateChange("connected", true);
    });

    expect(mockSeedTerminalSession).not.toHaveBeenCalled();
  });
});
