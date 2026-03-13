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
import { SessionNamePrompt } from "../SessionNamePrompt";

// ── Mock shared state ────────────────────────────────────────────────────────

const mockHook = vi.hoisted(() => ({
  sessions: [] as Array<{ name: string; label: string; created: number }>,
  isLoading: true,
  error: null as Error | null,
  refetch: vi.fn(),
}));

// ── Mock hooks ───────────────────────────────────────────────────────────────

vi.mock("@/hooks/useTerminalSessions", () => ({
  useTerminalSessions: () => mockHook,
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

const DEFAULT_SESSIONS = [
  { name: "session-1", label: "Session 1", created: 1 },
  { name: "session-2", label: "Session 2", created: 2 },
];

// ── Tests: TerminalView ──────────────────────────────────────────────────────

describe("TerminalView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setHook([], true);
  });

  // ── Session initialization ───────────────────────────────────────────────

  describe("session initialization", () => {
    it("shows loading state while useTerminalSessions is loading", () => {
      setHook([], true);
      render(<TerminalView />);

      expect(screen.getByText("Loading sessions...")).toBeInTheDocument();
    });

    it("renders tabs from hook sessions once loaded", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      expect(screen.queryByText("Loading sessions...")).not.toBeInTheDocument();
      expect(screen.getByTestId("terminal-tab-bar")).toBeInTheDocument();
      expect(screen.getByText("Session 1")).toBeInTheDocument();
      expect(screen.getByText("Session 2")).toBeInTheDocument();
    });

    it("first session becomes the active tab", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      expect(screen.getByTestId("active-tab-id").textContent).toBe("session-1");
    });

    it("tab ids match session names", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      expect(screen.getByTestId("tab-session-1")).toBeInTheDocument();
      expect(screen.getByTestId("tab-session-2")).toBeInTheDocument();
    });
  });

  // ── New tab prompt ─────────────────────────────────────────────────────────

  describe("new tab prompt", () => {
    it("clicking + opens SessionNamePrompt modal", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      const overlay = screen.getByTestId("session-name-prompt-overlay");
      expect(overlay).toHaveAttribute("aria-hidden", "true");

      fireEvent.click(screen.getByTestId("new-tab-button"));

      expect(overlay).toHaveAttribute("aria-hidden", "false");
    });

    it("clicking + when 8 tabs exist does not open prompt", () => {
      const eightSessions = Array.from({ length: 8 }, (_, i) => ({
        name: `s${i}`,
        label: `S${i}`,
        created: i,
      }));
      setHook(eightSessions, false);
      render(<TerminalView />);

      const overlay = screen.getByTestId("session-name-prompt-overlay");
      fireEvent.click(screen.getByTestId("new-tab-button"));

      expect(overlay).toHaveAttribute("aria-hidden", "true");
    });

    it("confirming with a valid name creates a new tab", async () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));

      const input = screen.getByTestId("session-name-input");
      fireEvent.change(input, { target: { value: "new-session" } });
      fireEvent.submit(input.closest("form")!);

      await waitFor(() => {
        expect(
          screen.getByTestId("terminal-instance-new-session"),
        ).toBeInTheDocument();
      });
    });

    it("new tab becomes active after creation", async () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));

      const input = screen.getByTestId("session-name-input");
      fireEvent.change(input, { target: { value: "new-session" } });
      fireEvent.submit(input.closest("form")!);

      await waitFor(() => {
        expect(screen.getByTestId("active-tab-id").textContent).toBe(
          "new-session",
        );
      });
    });

    it("cancelling the prompt does not create a tab", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("new-tab-button"));
      fireEvent.click(screen.getByTestId("session-name-cancel-button"));

      expect(
        screen.queryByTestId("terminal-instance-new-session"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("session-name-prompt-overlay")).toHaveAttribute(
        "aria-hidden",
        "true",
      );
    });
  });

  // ── Tab management ─────────────────────────────────────────────────────────

  describe("tab management", () => {
    it("handleTabClose removes tab", async () => {
      setHook(DEFAULT_SESSIONS, false);
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
      setHook(DEFAULT_SESSIONS, false);
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
      setHook([{ name: "only-session", label: "Only", created: 0 }], false);
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
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      expect(
        screen.queryByTestId("terminal-search-bar"),
      ).not.toBeInTheDocument();
    });

    it("Cmd+F toggles search overlay open", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      expect(screen.getByTestId("terminal-search-bar")).toBeInTheDocument();
    });

    it("Escape closes search overlay", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });
      expect(screen.getByTestId("terminal-search-bar")).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(
        screen.queryByTestId("terminal-search-bar"),
      ).not.toBeInTheDocument();
    });

    it("search input auto-focuses on open", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      const input = screen.getByTestId("terminal-search-input");
      expect(input).toHaveFocus();
    });
  });

  // ── Full-height ────────────────────────────────────────────────────────────

  describe("full-height", () => {
    it("container does not have fullHeight class by default", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      expect(screen.getByTestId("is-full-height").textContent).toBe("false");
      expect(screen.getByTestId("terminal-view").className).not.toContain(
        "fullHeight",
      );
    });

    it("toggle adds fullHeight class", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      fireEvent.click(screen.getByTestId("toggle-fullheight"));

      expect(screen.getByTestId("is-full-height").textContent).toBe("true");
      expect(screen.getByTestId("terminal-view").className).toContain(
        "fullHeight",
      );
    });
  });

  // ── Render tests ───────────────────────────────────────────────────────────

  describe("render tests", () => {
    it("only active tab terminal pane has display:block", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      const pane1 = screen
        .getByTestId("terminal-instance-session-1")
        .closest('[role="tabpanel"]')!;
      expect(pane1).toHaveStyle({ display: "block" });
    });

    it("inactive tab panes have display:none", () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

      const pane2 = screen
        .getByTestId("terminal-instance-session-2")
        .closest('[role="tabpanel"]')!;
      expect(pane2).toHaveStyle({ display: "none" });
    });

    it('data-testid="terminal-view" present on container', () => {
      setHook(DEFAULT_SESSIONS, false);
      render(<TerminalView />);

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
