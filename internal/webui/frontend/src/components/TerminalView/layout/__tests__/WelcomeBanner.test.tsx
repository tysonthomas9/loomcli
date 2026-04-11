/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WelcomeBanner component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { WelcomeBanner } from "../WelcomeBanner";

// Mock CSS module
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

const defaultProps = {
  backendName: "claude",
  isActive: true,
  onDismiss: vi.fn(),
  onExampleClick: vi.fn(),
};

describe("WelcomeBanner", () => {
  describe("rendering with known backends", () => {
    it("renders the welcome banner with correct testid", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByTestId("welcome-banner")).toBeInTheDocument();
    });

    it("renders heading with capitalized backend name for 'claude'", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      expect(screen.getByText("Welcome to Claude")).toBeInTheDocument();
    });

    it("renders heading with capitalized backend name for 'codex'", () => {
      render(<WelcomeBanner {...defaultProps} backendName="codex" />);

      expect(screen.getByText("Welcome to Codex")).toBeInTheDocument();
    });

    it("renders heading with capitalized backend name for 'opencode'", () => {
      render(<WelcomeBanner {...defaultProps} backendName="opencode" />);

      expect(screen.getByText("Welcome to Opencode")).toBeInTheDocument();
    });

    it("renders Claude-specific description", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      expect(
        screen.getByText(/Claude is an AI assistant by Anthropic/),
      ).toBeInTheDocument();
    });

    it("renders Codex-specific description", () => {
      render(<WelcomeBanner {...defaultProps} backendName="codex" />);

      expect(screen.getByText(/Codex is OpenAI/)).toBeInTheDocument();
    });

    it("renders Claude-specific example prompts", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

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

    it("renders 3 example buttons", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      const examples = screen.getAllByTestId("welcome-example");
      expect(examples).toHaveLength(3);
    });
  });

  describe("rendering with unknown backend", () => {
    it("renders fallback description for unknown backend", () => {
      render(<WelcomeBanner {...defaultProps} backendName="unknown-backend" />);

      expect(screen.getByText(/An AI coding assistant/)).toBeInTheDocument();
    });

    it("renders fallback example prompts for unknown backend", () => {
      render(<WelcomeBanner {...defaultProps} backendName="custom" />);

      expect(
        screen.getByText("Explain what this file does"),
      ).toBeInTheDocument();
      expect(screen.getByText("Help me debug this error")).toBeInTheDocument();
      expect(
        screen.getByText("Write a unit test for this function"),
      ).toBeInTheDocument();
    });

    it("capitalizes the first letter of unknown backend name in heading", () => {
      render(<WelcomeBanner {...defaultProps} backendName="mybot" />);

      expect(screen.getByText("Welcome to Mybot")).toBeInTheDocument();
    });
  });

  describe("close button", () => {
    it("renders close button", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByTestId("welcome-banner-close")).toBeInTheDocument();
    });

    it("calls onDismiss when close button is clicked", () => {
      const onDismiss = vi.fn();
      render(<WelcomeBanner {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.click(screen.getByTestId("welcome-banner-close"));

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("close button has accessible label", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByTestId("welcome-banner-close")).toHaveAttribute(
        "aria-label",
        "Dismiss welcome banner",
      );
    });
  });

  describe("example click", () => {
    it("calls onExampleClick with the example text when an example is clicked", () => {
      const onExampleClick = vi.fn();
      render(
        <WelcomeBanner
          {...defaultProps}
          backendName="claude"
          onExampleClick={onExampleClick}
        />,
      );

      fireEvent.click(
        screen.getByText("Explain the architecture of this project"),
      );

      expect(onExampleClick).toHaveBeenCalledWith(
        "Explain the architecture of this project",
      );
    });

    it("calls onExampleClick for each different example", () => {
      const onExampleClick = vi.fn();
      render(
        <WelcomeBanner
          {...defaultProps}
          backendName="claude"
          onExampleClick={onExampleClick}
        />,
      );

      fireEvent.click(
        screen.getByText("Find potential bugs in the auth module"),
      );

      expect(onExampleClick).toHaveBeenCalledWith(
        "Find potential bugs in the auth module",
      );
    });
  });

  describe("keypress auto-dismiss", () => {
    it("calls onDismiss on non-modifier keypress when isActive", () => {
      const onDismiss = vi.fn();
      render(
        <WelcomeBanner
          {...defaultProps}
          isActive={true}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.keyDown(document, { key: "a" });

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("does NOT dismiss on modifier-only keys", () => {
      const onDismiss = vi.fn();
      render(
        <WelcomeBanner
          {...defaultProps}
          isActive={true}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.keyDown(document, { key: "Shift" });
      fireEvent.keyDown(document, { key: "Control" });
      fireEvent.keyDown(document, { key: "Alt" });
      fireEvent.keyDown(document, { key: "Meta" });

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("does NOT register keydown listener when isActive is false", () => {
      const onDismiss = vi.fn();
      render(
        <WelcomeBanner
          {...defaultProps}
          isActive={false}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.keyDown(document, { key: "a" });

      expect(onDismiss).not.toHaveBeenCalled();
    });
  });

  describe("hint text", () => {
    it("renders dismiss hint", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByText(/Press any key or click/)).toBeInTheDocument();
    });
  });

  describe("accessibility", () => {
    it('has role="region"', () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByRole("region")).toBeInTheDocument();
    });

    it("has appropriate aria-label", () => {
      render(<WelcomeBanner {...defaultProps} backendName="claude" />);

      expect(screen.getByRole("region")).toHaveAttribute(
        "aria-label",
        "Welcome to Claude",
      );
    });
  });

  describe("Try asking label", () => {
    it("renders the 'Try asking' label", () => {
      render(<WelcomeBanner {...defaultProps} />);

      expect(screen.getByText("Try asking")).toBeInTheDocument();
    });
  });
});
