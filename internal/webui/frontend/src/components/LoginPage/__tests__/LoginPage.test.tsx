/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { LoginPage } from "../LoginPage";
import { createDefaultAuth } from "@/test-utils/mockAuthContext";

// Mock useAuth from AuthContext
const mockSignIn = vi.fn();
const mockUseAuth = vi.fn();
vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => mockUseAuth(),
}));

// Mock useFocusTrap
vi.mock("@/hooks", () => ({
  useFocusTrap: vi.fn(),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
}));

describe("LoginPage", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockSignIn.mockResolvedValue(undefined);
    mockUseAuth.mockReturnValue(createDefaultAuth({ signIn: mockSignIn }));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // ========== Rendering ==========

  describe("rendering", () => {
    it("renders full-viewport overlay with role=dialog", () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      const dialog = screen.getByRole("dialog");
      expect(dialog).toBeInTheDocument();
      expect(dialog).toHaveAttribute("aria-modal", "true");
    });

    it("renders GitHub and Google buttons", () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      expect(
        screen.getByRole("button", { name: "Continue with GitHub" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Continue with Google" }),
      ).toBeInTheDocument();
    });

    it("renders title with correct aria-labelledby", () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      const dialog = screen.getByRole("dialog");
      expect(dialog).toHaveAttribute("aria-labelledby", "login-title");
      expect(screen.getByText("Sign in to Superfactory")).toHaveAttribute(
        "id",
        "login-title",
      );
    });

    it("renders the supplied Superfactory wordmark", () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);

      expect(
        screen.getByRole("img", { name: "Superfactory" }),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("superfactory-wordmark-light"),
      ).toHaveAttribute(
        "src",
        "/brand/superfactory-wordmark-light-cropped.png",
      );
      expect(
        screen.getByTestId("superfactory-wordmark-dark"),
      ).toHaveAttribute(
        "src",
        "/brand/superfactory-wordmark-dark-cropped.png",
      );
    });

    it("OAuth and toggle buttons have type=button (only form submit is type=submit)", () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      const submitButtons = screen
        .getAllByRole("button")
        .filter((b) => b.getAttribute("type") === "submit");
      expect(submitButtons).toHaveLength(1);
      const nonSubmit = screen
        .getAllByRole("button")
        .filter((b) => b.getAttribute("type") !== "submit");
      nonSubmit.forEach((button) => {
        expect(button).toHaveAttribute("type", "button");
      });
    });
  });

  // ========== OAuth button interaction ==========

  describe("OAuth button interaction", () => {
    it("calls signIn('github') when GitHub button clicked", async () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });
      expect(mockSignIn).toHaveBeenCalledWith("github");
    });

    it("calls signIn('google') when Google button clicked", async () => {
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with Google" }),
        );
      });
      expect(mockSignIn).toHaveBeenCalledWith("google");
    });

    it("disables OAuth and submit buttons after click (loading state)", async () => {
      // signIn never resolves to keep loading state
      mockSignIn.mockReturnValue(new Promise(() => {}));
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });
      expect(
        screen.getByRole("button", {
          name: /Continue with GitHub|Redirecting/,
        }),
      ).toBeDisabled();
      expect(
        screen.getByRole("button", { name: "Continue with Google" }),
      ).toBeDisabled();
      expect(
        screen.getByRole("button", { name: /Sign in|Signing in/ }),
      ).toBeDisabled();
    });

    it('shows "Redirecting..." on clicked button', async () => {
      mockSignIn.mockReturnValue(new Promise(() => {}));
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });
      expect(screen.getByText(/Redirecting/)).toBeInTheDocument();
      // Other button should still show its original text
      expect(
        screen.getByRole("button", { name: "Continue with Google" }),
      ).toBeInTheDocument();
    });
  });

  // ========== Loading timeout ==========

  describe("loading timeout", () => {
    it("shows timeout message after 10 seconds", async () => {
      mockSignIn.mockReturnValue(new Promise(() => {}));
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });
      expect(
        screen.queryByText(/Redirect seems stuck/),
      ).not.toBeInTheDocument();

      await act(async () => {
        vi.advanceTimersByTime(10_000);
      });

      expect(screen.getByText(/Redirect seems stuck/)).toBeInTheDocument();
    });

    it("re-enables buttons after timeout", async () => {
      mockSignIn.mockReturnValue(new Promise(() => {}));
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });

      await act(async () => {
        vi.advanceTimersByTime(10_000);
      });

      const buttons = screen.getAllByRole("button");
      buttons.forEach((button) => {
        expect(button).not.toBeDisabled();
      });
    });
  });

  // ========== Error display ==========

  describe("error display", () => {
    it("displays OAuth error message from props", () => {
      render(<LoginPage error="access_denied" onErrorClear={vi.fn()} />);
      expect(screen.getByText(/access_denied/)).toBeInTheDocument();
    });

    it("shows auth service down message", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ signIn: mockSignIn, authServiceDown: true }),
      );
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      expect(
        screen.getByText(/Authentication service is currently unavailable/),
      ).toBeInTheDocument();
    });

    it("hides OAuth buttons when auth service down", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ signIn: mockSignIn, authServiceDown: true }),
      );
      render(<LoginPage error={null} onErrorClear={vi.fn()} />);
      expect(
        screen.queryByRole("button", { name: /Continue with/ }),
      ).not.toBeInTheDocument();
    });

    it("does not show OAuth error when auth service is down", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ signIn: mockSignIn, authServiceDown: true }),
      );
      render(<LoginPage error="access_denied" onErrorClear={vi.fn()} />);
      expect(screen.queryByText(/access_denied/)).not.toBeInTheDocument();
    });

    it("clears error on button click", async () => {
      const onErrorClear = vi.fn();
      render(<LoginPage error="some_error" onErrorClear={onErrorClear} />);
      await act(async () => {
        fireEvent.click(
          screen.getByRole("button", { name: "Continue with GitHub" }),
        );
      });
      expect(onErrorClear).toHaveBeenCalledTimes(1);
    });
  });
});
