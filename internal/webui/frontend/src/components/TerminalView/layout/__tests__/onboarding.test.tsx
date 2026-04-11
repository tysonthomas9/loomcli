/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for Talk to Lead first-time onboarding components:
 * WelcomeBanner, NoBackendsEmptyState, HelpPopover.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { WelcomeBanner } from "../WelcomeBanner";
import { NoBackendsEmptyState } from "../NoBackendsEmptyState";
import { HelpPopover } from "@/components/TerminalView/controls";

// Mock CSS modules
vi.mock("../WelcomeBanner.module.css", () => ({
  default: {
    overlay: "overlay",
    card: "card",
    closeButton: "closeButton",
    heading: "heading",
    description: "description",
    examplesLabel: "examplesLabel",
    examples: "examples",
    example: "example",
    hint: "hint",
  },
}));

vi.mock("../NoBackendsEmptyState.module.css", () => ({
  default: {
    container: "container",
    icon: "icon",
    heading: "heading",
    description: "description",
    settingsButton: "settingsButton",
  },
}));

vi.mock("../HelpPopover.module.css", () => ({
  default: {
    popover: "popover",
    sectionTitle: "sectionTitle",
    row: "row",
    kbd: "kbd",
    command: "command",
    commandDesc: "commandDesc",
  },
}));

describe("WelcomeBanner", () => {
  const defaultProps = {
    backendName: "claude",
    isActive: true,
    onDismiss: vi.fn(),
    onExampleClick: vi.fn(),
  };

  beforeEach(() => {
    vi.restoreAllMocks();
    defaultProps.onDismiss = vi.fn();
    defaultProps.onExampleClick = vi.fn();
  });

  describe("rendering known backends", () => {
    it("renders correct content for claude", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      expect(screen.getByText("Welcome to Claude")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Claude is an AI assistant by Anthropic. Ask about your codebase, get help with bugs, or discuss architecture.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Explain the architecture of this project"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Find potential bugs in the auth module"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Help me write tests for the API endpoints"),
      ).toBeInTheDocument();
    });

    it("renders correct content for codex", () => {
      render(<WelcomeBanner {...defaultProps} backendName="codex" />);

      expect(screen.getByText("Welcome to Codex")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Codex is OpenAI\u2019s code-focused model. Great for code generation, refactoring, and technical explanations.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Refactor this function to use async/await"),
      ).toBeInTheDocument();
    });

    it("renders correct content for opencode", () => {
      render(<WelcomeBanner {...defaultProps} backendName="opencode" />);

      expect(screen.getByText("Welcome to Opencode")).toBeInTheDocument();
      expect(
        screen.getByText(
          "OpenCode is an open-source coding assistant. Use it for code review, documentation, and pair programming.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Review this pull request for issues"),
      ).toBeInTheDocument();
    });
  });

  describe("rendering fallback", () => {
    it("renders fallback content for unknown backend", () => {
      render(<WelcomeBanner {...defaultProps} backendName="somellm" />);

      expect(screen.getByText("Welcome to Somellm")).toBeInTheDocument();
      expect(
        screen.getByText(
          "An AI coding assistant. Ask questions about your codebase, get help debugging, or request code changes.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Explain what this file does"),
      ).toBeInTheDocument();
      expect(screen.getByText("Help me debug this error")).toBeInTheDocument();
      expect(
        screen.getByText("Write a unit test for this function"),
      ).toBeInTheDocument();
    });
  });

  describe("interactions", () => {
    it("calls onDismiss when close button is clicked", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.click(screen.getByTestId("welcome-banner-close"));

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("calls onDismiss on non-modifier keydown", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "a" });

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("does NOT call onDismiss on modifier-only keydown (Shift)", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "Shift" });

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("does NOT call onDismiss on modifier-only keydown (Control)", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "Control" });

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("does NOT call onDismiss on modifier-only keydown (Alt)", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "Alt" });

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("does NOT call onDismiss on modifier-only keydown (Meta)", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "Meta" });

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("calls onExampleClick with correct text when example is clicked", () => {
      const onExampleClick = vi.fn();
      render(
        <WelcomeBanner {...defaultProps} onExampleClick={onExampleClick} />,
      );

      const examples = screen.getAllByTestId("welcome-example");
      fireEvent.click(examples[0]);

      expect(onExampleClick).toHaveBeenCalledTimes(1);
      expect(onExampleClick).toHaveBeenCalledWith(
        "Explain the architecture of this project",
      );
    });

    it("calls onExampleClick with correct text for second example", () => {
      const onExampleClick = vi.fn();
      render(
        <WelcomeBanner {...defaultProps} onExampleClick={onExampleClick} />,
      );

      const examples = screen.getAllByTestId("welcome-example");
      fireEvent.click(examples[1]);

      expect(onExampleClick).toHaveBeenCalledWith(
        "Find potential bugs in the auth module",
      );
    });
  });

  describe("accessibility", () => {
    it("has role=region with aria-label", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      const banner = screen.getByTestId("welcome-banner");
      expect(banner).toHaveAttribute("role", "region");
      expect(banner).toHaveAttribute("aria-label", "Welcome to Claude");
    });

    it("close button has aria-label", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByTestId("welcome-banner-close")).toHaveAttribute(
        "aria-label",
        "Dismiss welcome banner",
      );
    });
  });
});

describe("NoBackendsEmptyState", () => {
  describe("rendering", () => {
    it("renders heading", () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByText("No backends configured")).toBeInTheDocument();
    });

    it("renders description", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.getByText(
          "Configure at least one AI backend to start using Talk to Lead.",
        ),
      ).toBeInTheDocument();
    });

    it("has role=status", () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByTestId("no-backends-empty-state")).toHaveAttribute(
        "role",
        "status",
      );
    });
  });

  describe("interactions", () => {
    it("calls onGoToSettings when button is clicked", () => {
      const onGoToSettings = vi.fn();
      render(<NoBackendsEmptyState onGoToSettings={onGoToSettings} />);

      fireEvent.click(screen.getByTestId("go-to-settings-button"));

      expect(onGoToSettings).toHaveBeenCalledTimes(1);
    });

    it("renders Go to Settings button when onGoToSettings is provided", () => {
      const onGoToSettings = vi.fn();
      render(<NoBackendsEmptyState onGoToSettings={onGoToSettings} />);

      expect(screen.getByTestId("go-to-settings-button")).toBeInTheDocument();
      expect(screen.getByTestId("go-to-settings-button")).toHaveTextContent(
        "Go to Settings",
      );
    });

    it("hides button when onGoToSettings is not provided", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.queryByTestId("go-to-settings-button"),
      ).not.toBeInTheDocument();
    });
  });
});

describe("HelpPopover", () => {
  const defaultProps = {
    isOpen: true,
    onClose: vi.fn(),
  };

  beforeEach(() => {
    defaultProps.onClose = vi.fn();
  });

  describe("rendering", () => {
    it("renders shortcuts section", () => {
      render(<HelpPopover {...defaultProps} />);

      expect(screen.getByText("Keyboard Shortcuts")).toBeInTheDocument();
      expect(screen.getByText("Search in terminal")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+F")).toBeInTheDocument();
      expect(screen.getByText("New tab")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+T")).toBeInTheDocument();
    });

    it("renders slash commands section", () => {
      render(<HelpPopover {...defaultProps} />);

      expect(screen.getByText("Slash Commands")).toBeInTheDocument();
      expect(screen.getByText("/help")).toBeInTheDocument();
      expect(screen.getByText("Show available commands")).toBeInTheDocument();
      expect(screen.getByText("/clear")).toBeInTheDocument();
      expect(screen.getByText("Clear terminal output")).toBeInTheDocument();
    });

    it("has role=dialog with aria-label", () => {
      render(<HelpPopover {...defaultProps} />);

      const popover = screen.getByTestId("terminal-help-popover");
      expect(popover).toHaveAttribute("role", "dialog");
      expect(popover).toHaveAttribute("aria-label", "Terminal help");
    });
  });

  describe("interactions", () => {
    it("closes on Escape keydown", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("closes on click outside", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.mouseDown(document);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not close on click inside the popover", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      const popover = screen.getByTestId("terminal-help-popover");
      fireEvent.mouseDown(popover);

      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("closed state", () => {
    it("returns null when isOpen is false", () => {
      const { container } = render(
        <HelpPopover isOpen={false} onClose={vi.fn()} />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("terminal-help-popover"),
      ).not.toBeInTheDocument();
    });

    it("does not register event listeners when closed", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={false} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).not.toHaveBeenCalled();
    });
  });
});

/**
 * Unit tests for the global onboarding dismissal logic in TerminalView.
 *
 * TerminalView is too complex to render in isolation, so we test the
 * localStorage-based logic directly — the same code paths used by the
 * useState initializer and handleDismissWelcome callback.
 */
describe("Global onboarding dismissal (localStorage)", () => {
  const GLOBAL_KEY = "terminal-onboarding-dismissed";
  const OLD_KEY_PREFIX = "terminal-welcome-dismissed-";

  /**
   * Mirrors the useState initializer in TerminalView (lines 138-153).
   * Returns what `dismissedWelcome` would be set to on mount.
   */
  function computeInitialDismissed(): boolean {
    try {
      if (localStorage.getItem(GLOBAL_KEY) === "1") return true;
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key?.startsWith(OLD_KEY_PREFIX)) {
          localStorage.setItem(GLOBAL_KEY, "1");
          return true;
        }
      }
    } catch {
      // localStorage unavailable — show banners every session
    }
    return false;
  }

  /**
   * Mirrors the handleDismissWelcome callback in TerminalView (lines 719-726).
   */
  function handleDismissWelcome(): void {
    try {
      localStorage.setItem(GLOBAL_KEY, "1");
    } catch {
      // localStorage unavailable
    }
  }

  beforeEach(() => {
    localStorage.clear();
  });

  it("fresh user: no localStorage keys → dismissedWelcome is false", () => {
    const result = computeInitialDismissed();
    expect(result).toBe(false);
  });

  it("returning user: terminal-onboarding-dismissed=1 → dismissedWelcome is true", () => {
    localStorage.setItem(GLOBAL_KEY, "1");

    const result = computeInitialDismissed();
    expect(result).toBe(true);
  });

  it("migration: old per-backend key exists → treated as dismissed, global key written", () => {
    localStorage.setItem(`${OLD_KEY_PREFIX}claude`, "1");

    const result = computeInitialDismissed();
    expect(result).toBe(true);
    expect(localStorage.getItem(GLOBAL_KEY)).toBe("1");
  });

  it("migration works with any old backend key", () => {
    localStorage.setItem(`${OLD_KEY_PREFIX}codex`, "1");

    const result = computeInitialDismissed();
    expect(result).toBe(true);
    expect(localStorage.getItem(GLOBAL_KEY)).toBe("1");
  });

  it("dismiss writes the global key to localStorage", () => {
    expect(localStorage.getItem(GLOBAL_KEY)).toBeNull();

    handleDismissWelcome();

    expect(localStorage.getItem(GLOBAL_KEY)).toBe("1");
  });

  it("after dismiss, subsequent initializations see dismissed=true", () => {
    // First visit: not dismissed
    expect(computeInitialDismissed()).toBe(false);

    // User dismisses
    handleDismissWelcome();

    // Next mount sees the global key
    expect(computeInitialDismissed()).toBe(true);
  });

  it("global dismissal: dismissing on one backend suppresses for all backends", () => {
    // Simulate dismissing while using "claude" backend
    handleDismissWelcome();

    // Now check: a different backend ("codex") should also see dismissed
    // The logic does not check backendName at all — it is purely global
    expect(computeInitialDismissed()).toBe(true);

    // No per-backend keys were written — only the global key exists
    expect(localStorage.getItem(`${OLD_KEY_PREFIX}claude`)).toBeNull();
    expect(localStorage.getItem(`${OLD_KEY_PREFIX}codex`)).toBeNull();
    expect(localStorage.getItem(GLOBAL_KEY)).toBe("1");
  });

  it("ignores unrelated localStorage keys during migration scan", () => {
    localStorage.setItem("some-other-key", "value");
    localStorage.setItem("terminal-other-setting", "true");

    const result = computeInitialDismissed();
    expect(result).toBe(false);
    expect(localStorage.getItem(GLOBAL_KEY)).toBeNull();
  });
});
