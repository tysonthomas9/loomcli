/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { AuthGate } from "../AuthGate";
import { createDefaultAuth } from "@/test-utils/mockAuthContext";

// Mock useAuth from AuthContext
const mockUseAuth = vi.fn();
vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => mockUseAuth(),
}));

// Mock LoginPage to inspect props
vi.mock("@/components/LoginPage", () => ({
  LoginPage: ({
    error,
    onErrorClear,
  }: {
    error: string | null;
    onErrorClear: () => void;
  }) => (
    <div data-testid="login-page" data-error={error ?? ""}>
      <button onClick={onErrorClear}>clear-error</button>
    </div>
  ),
}));

describe("AuthGate", () => {
  let replaceStateSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    replaceStateSpy = vi.spyOn(window.history, "replaceState");
    // Reset URL
    window.history.replaceState({}, "", "/");
  });

  afterEach(() => {
    replaceStateSpy.mockRestore();
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  // ========== Rendering decisions ==========

  describe("rendering decisions", () => {
    it("returns null when isLoading is true", () => {
      mockUseAuth.mockReturnValue(createDefaultAuth({ isLoading: true }));
      const { container } = render(
        <AuthGate>
          <div>app content</div>
        </AuthGate>,
      );
      expect(container.innerHTML).toBe("");
    });

    it("renders LoginPage when mode=oidc and not authenticated", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: false }),
      );
      render(
        <AuthGate>
          <div>app content</div>
        </AuthGate>,
      );
      expect(screen.getByTestId("login-page")).toBeInTheDocument();
      expect(screen.queryByText("app content")).not.toBeInTheDocument();
    });

    it("renders children when authenticated", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      render(
        <AuthGate>
          <div>app content</div>
        </AuthGate>,
      );
      expect(screen.getByText("app content")).toBeInTheDocument();
      expect(screen.queryByTestId("login-page")).not.toBeInTheDocument();
    });

    it("renders children when mode=open regardless of auth state", () => {
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "open", isAuthenticated: false }),
      );
      render(
        <AuthGate>
          <div>app content</div>
        </AuthGate>,
      );
      expect(screen.getByText("app content")).toBeInTheDocument();
    });
  });

  // ========== OAuth error handling ==========

  describe("OAuth error handling", () => {
    it("extracts error from URL query params and passes to LoginPage", () => {
      window.history.replaceState({}, "", "/?error=access_denied");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: false }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      const loginPage = screen.getByTestId("login-page");
      expect(loginPage).toHaveAttribute("data-error", "access_denied");
    });

    it("cleans error from URL after extraction", () => {
      window.history.replaceState({}, "", "/app?error=denied");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: false }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      expect(replaceStateSpy).toHaveBeenCalledWith({}, "", "/app");
    });

    it("handles missing error param gracefully", () => {
      window.history.replaceState({}, "", "/");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: false }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      const loginPage = screen.getByTestId("login-page");
      expect(loginPage).toHaveAttribute("data-error", "");
    });
  });

  // ========== Deep link restoration ==========

  describe("deep link restoration", () => {
    it("restores valid returnTo path after login", () => {
      sessionStorage.setItem("loom-auth-return-to", "/issues/123");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      expect(replaceStateSpy).toHaveBeenCalledWith({}, "", "/issues/123");
    });

    it("rejects returnTo containing ://", () => {
      sessionStorage.setItem(
        "loom-auth-return-to",
        "https://evil.com/redirect",
      );
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      // replaceState should not be called with the malicious URL
      const calls = replaceStateSpy.mock.calls;
      const hasEvilUrl = calls.some((c) => String(c[2]).includes("evil.com"));
      expect(hasEvilUrl).toBe(false);
    });

    it("rejects returnTo not starting with /", () => {
      sessionStorage.setItem("loom-auth-return-to", "evil.com/path");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      const calls = replaceStateSpy.mock.calls;
      const hasEvilUrl = calls.some((c) => String(c[2]).includes("evil.com"));
      expect(hasEvilUrl).toBe(false);
    });

    it("clears sessionStorage entry after reading", () => {
      sessionStorage.setItem("loom-auth-return-to", "/dashboard");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );
      expect(sessionStorage.getItem("loom-auth-return-to")).toBeNull();
    });

    it("handles sessionStorage errors gracefully", () => {
      const getItemSpy = vi
        .spyOn(Storage.prototype, "getItem")
        .mockImplementation(() => {
          throw new Error("SecurityError");
        });
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );

      // Should not throw
      expect(() =>
        render(
          <AuthGate>
            <div>app</div>
          </AuthGate>,
        ),
      ).not.toThrow();

      getItemSpy.mockRestore();
    });

    it("processes returnTo only once on re-render", () => {
      sessionStorage.setItem("loom-auth-return-to", "/settings");
      mockUseAuth.mockReturnValue(
        createDefaultAuth({ mode: "oidc", isAuthenticated: true }),
      );
      const { rerender } = render(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );

      const callCountAfterFirst = replaceStateSpy.mock.calls.filter(
        (c) => String(c[2]) === "/settings",
      ).length;

      // Re-render with same auth state
      rerender(
        <AuthGate>
          <div>app</div>
        </AuthGate>,
      );

      const callCountAfterSecond = replaceStateSpy.mock.calls.filter(
        (c) => String(c[2]) === "/settings",
      ).length;

      expect(callCountAfterSecond).toBe(callCountAfterFirst);
    });
  });
});
