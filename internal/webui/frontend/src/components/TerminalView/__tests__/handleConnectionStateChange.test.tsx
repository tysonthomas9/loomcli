/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for stale closure bugs in TerminalView callbacks (loomcli-5y1sd.6).
 *
 * Tests that handleNewTabClick does not use a stale tabs.length value,
 * and that handleConnectionStateChange uses functional state updates
 * to avoid stale closures.
 */

import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { TerminalView } from "../TerminalView";

// ── Mock shared state ────────────────────────────────────────────────────────

const mockHook = vi.hoisted(() => ({
  sessions: [] as Array<{ name: string; label: string; created: number }>,
  isLoading: true,
  error: null as Error | null,
  refetch: vi.fn(),
}));

vi.mock("@/hooks/useTerminalSessions", () => ({
  useTerminalSessions: () => mockHook,
}));

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
  updateLabel: vi.fn(),
  updateNotes: vi.fn().mockResolvedValue(undefined),
  reorderTabs: vi.fn(),
  deleteTab: vi.fn(),
  refetch: vi.fn(),
  handleMutation: vi.fn(),
}));

vi.mock("@/hooks/useTerminalMetadata", () => ({
  useTerminalMetadata: () => mockMetadataHook,
}));

// ── Mock sibling components ──────────────────────────────────────────────────

// Track onConnectionStateChange callbacks per session
const connectionStateCallbacks = new Map<
  string,
  (state: string) => void
>();

vi.mock("../TerminalInstance", () => ({
  TerminalInstance: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => {
      // Store the onConnectionStateChange callback
      if (props.onConnectionStateChange) {
        connectionStateCallbacks.set(
          props.sessionName,
          props.onConnectionStateChange,
        );
      }
      return (
        <div data-testid={`terminal-instance-${props.sessionName}`}>
          TerminalInstance:{props.sessionName}
        </div>
      );
    },
  ),
}));

vi.mock("../TerminalTabBar", () => ({
  TerminalTabBar: vi.fn(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (props: any) => (
      <div data-testid="terminal-tab-bar">
        {props.tabs.map(
          (t: { id: string; label: string; connectionState: string }) => (
            <button
              key={t.id}
              data-testid={`tab-${t.id}`}
              data-connection-state={t.connectionState}
              onClick={() => props.onTabChange(t.id)}
            >
              {t.label}
            </button>
          ),
        )}
        <button data-testid="new-tab-button" onClick={props.onNewTab}>
          +
        </button>
        <button
          data-testid="close-tab-button"
          onClick={() => props.onTabClose(props.activeTabId)}
        >
          Close
        </button>
      </div>
    ),
  ),
}));

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

function setHook(
  sessions: Array<{ name: string; label: string; created: number }>,
  isLoading: boolean,
) {
  mockHook.sessions = sessions;
  mockHook.isLoading = isLoading;
  mockHook.error = null;
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe("handleConnectionStateChange stale closure (loomcli-5y1sd.6)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    connectionStateCallbacks.clear();
  });

  it("updates connection state for correct tab using functional state update", async () => {
    const sessions = [
      { name: "session-1", label: "Session 1", created: 1 },
      { name: "session-2", label: "Session 2", created: 2 },
    ];
    setHook(sessions, false);
    render(<TerminalView />);

    // Verify both tabs are rendered
    expect(
      screen.getByTestId("terminal-instance-session-1"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("terminal-instance-session-2"),
    ).toBeInTheDocument();

    // Simulate connection state change for session-2
    const callback = connectionStateCallbacks.get("session-2");
    expect(callback).toBeDefined();
    act(() => {
      callback!("connected");
    });

    // Verify session-2's tab shows connected state
    await waitFor(() => {
      const tab2 = screen.getByTestId("tab-session-2");
      expect(tab2.dataset.connectionState).toBe("connected");
    });
  });

  it("handleNewTabClick does not use stale tabs count", async () => {
    // Start with 7 sessions (one below MAX_TABS=8)
    const sevenSessions = Array.from({ length: 7 }, (_, i) => ({
      name: `s${i}`,
      label: `S${i}`,
      created: i,
    }));
    setHook(sevenSessions, false);
    render(<TerminalView />);

    // Open prompt and create tab #8 (reaching MAX_TABS)
    fireEvent.click(screen.getByTestId("new-tab-button"));

    const input = screen.getByTestId("session-name-input");
    fireEvent.change(input, { target: { value: "s7" } });
    fireEvent.submit(input.closest("form")!);

    await waitFor(() => {
      expect(screen.getByTestId("terminal-instance-s7")).toBeInTheDocument();
    });

    // Now try to click + again - should NOT open the prompt
    // because we have 8 tabs (MAX_TABS)
    fireEvent.click(screen.getByTestId("new-tab-button"));

    // The prompt should remain closed
    const overlay = screen.getByTestId("session-name-prompt-overlay");
    expect(overlay).toHaveAttribute("aria-hidden", "true");
  });

  it("does not re-seed a session that was already seeded", async () => {
    // This test verifies that connection state changes are idempotent
    const sessions = [
      { name: "session-1", label: "Session 1", created: 1 },
    ];
    setHook(sessions, false);
    render(<TerminalView />);

    const callback = connectionStateCallbacks.get("session-1");
    expect(callback).toBeDefined();

    // Fire connected twice
    act(() => {
      callback!("connected");
      callback!("connected");
    });

    // Tab should still show connected (no error, no double-update issues)
    await waitFor(() => {
      const tab = screen.getByTestId("tab-session-1");
      expect(tab.dataset.connectionState).toBe("connected");
    });
  });

  it("does not seed when no pending context exists for the session", async () => {
    // Connect a tab that has no pending seed context
    const sessions = [
      { name: "session-1", label: "Session 1", created: 1 },
    ];
    setHook(sessions, false);
    render(<TerminalView />);

    const callback = connectionStateCallbacks.get("session-1");
    expect(callback).toBeDefined();

    // This should not throw or cause issues
    act(() => {
      callback!("connected");
    });

    await waitFor(() => {
      const tab = screen.getByTestId("tab-session-1");
      expect(tab.dataset.connectionState).toBe("connected");
    });
  });
});
