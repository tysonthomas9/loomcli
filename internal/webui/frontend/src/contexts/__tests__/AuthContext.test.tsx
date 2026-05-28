/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AuthContext — ExternalAuthProvider, NoAuthProvider, useAuth.
 *
 * Uses vi.resetModules() + dynamic import in beforeEach to get a fresh module
 * with cleared module-scoped state (_useSession, pendingRefreshPromise,
 * lastRefreshTimestamp) for each test.
 */

import React from "react";
import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

// ---------------------------------------------------------------------------
// Mock: @/api
// ---------------------------------------------------------------------------
const mockSetAuthToken = vi.fn();
const mockSetAuthState = vi.fn();
let authStateListeners: Array<(state: string) => void> = [];
let currentAuthState = "none";

function notifyAuthListeners(state: string) {
  currentAuthState = state;
  for (const cb of authStateListeners) cb(state);
}

vi.mock("@/api", () => ({
  setAuthToken: (...args: unknown[]) => {
    mockSetAuthToken(...args);
    // Simulate real setAuthToken behavior: update state + notify
    notifyAuthListeners(args[0] !== null ? "authenticated" : "none");
  },
  setAuthState: (...args: unknown[]) => {
    mockSetAuthState(...args);
    notifyAuthListeners(args[0] as string);
  },
  getAuthState: () => currentAuthState,
  onAuthStateChange: (cb: (state: string) => void) => {
    authStateListeners.push(cb);
    return () => {
      authStateListeners = authStateListeners.filter((l) => l !== cb);
    };
  },
  onAuthTokenExpired: () => () => {},
}));

// ---------------------------------------------------------------------------
// Mock: @/api/authClient
// ---------------------------------------------------------------------------
let mockSessionData: {
  data: Record<string, unknown> | null;
  isPending: boolean;
  error: Error | null;
};

const mockToken = vi.fn();
const mockSignInSocial = vi.fn();
const mockSignInEmail = vi.fn();
const mockSignUpEmail = vi.fn();
const mockSignOut = vi.fn();

const mockGetAuthClient = vi.fn();

vi.mock(import("@/api/common"), async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    getAuthClient: (...args: unknown[]) => mockGetAuthClient(...args),
  };
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function buildMockClient() {
  return {
    useSession: () => mockSessionData,
    token: mockToken,
    signIn: { social: mockSignInSocial, email: mockSignInEmail },
    signUp: { email: mockSignUpEmail },
    signOut: mockSignOut,
  };
}

function resetSessionData() {
  mockSessionData = { data: null, isPending: true, error: null };
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("AuthContext", () => {
  let mod: typeof import("../AuthContext");

  beforeEach(async () => {
    vi.resetModules();

    // Reset all mock state
    authStateListeners = [];
    currentAuthState = "none";
    resetSessionData();
    mockToken.mockReset();
    mockSignInSocial.mockReset();
    mockSignInEmail.mockReset();
    mockSignUpEmail.mockReset();
    mockSignOut.mockReset();
    mockSetAuthToken.mockReset();
    mockSetAuthState.mockReset();
    mockGetAuthClient.mockReset();

    // Default mock implementations
    mockToken.mockResolvedValue({ data: { token: "jwt-123" }, error: null });
    mockSignInSocial.mockResolvedValue(undefined);
    mockSignInEmail.mockResolvedValue({ data: {}, error: null });
    mockSignUpEmail.mockResolvedValue({ data: {}, error: null });
    mockSignOut.mockResolvedValue(undefined);
    mockGetAuthClient.mockReturnValue(buildMockClient());

    // Dynamic import to get fresh module-level state
    mod = await import("../AuthContext");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // =========================================================================
  // ExternalAuthProvider
  // =========================================================================
  describe("ExternalAuthProvider", () => {
    it("calls authClient.token() when session becomes active", async () => {
      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s1" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockToken).toHaveBeenCalled();
      });
    });

    it("calls setAuthToken with JWT from authClient.token()", async () => {
      mockToken.mockResolvedValue({
        data: { token: "jwt-456" },
        error: null,
      });

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Bob", email: "bob@test.com" },
          session: { id: "s2" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockSetAuthToken).toHaveBeenCalledWith("jwt-456");
      });
    });

    it("does not call token() while isPending", () => {
      mockSessionData = { data: null, isPending: true, error: null };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      expect(mockToken).not.toHaveBeenCalled();
    });

    it("calls setAuthToken(null) when no session", async () => {
      mockSessionData = { data: null, isPending: false, error: null };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockSetAuthToken).toHaveBeenCalledWith(null);
      });
    });

    it("sets authServiceDown=true on useSession error", async () => {
      mockSessionData = {
        data: null,
        isPending: false,
        error: new Error("network failure"),
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(result.current.authServiceDown).toBe(true);
      });
      expect(mockSetAuthToken).toHaveBeenCalledWith(null);
      expect(mockSetAuthState).toHaveBeenCalledWith("failed");
    });

    it("handles token() failure gracefully", async () => {
      mockToken.mockRejectedValue(new Error("token endpoint down"));

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s3" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockSetAuthToken).toHaveBeenCalledWith(null);
      });
      expect(mockSetAuthState).toHaveBeenCalledWith("failed");
    });

    it("handles token() returning null data", async () => {
      mockToken.mockResolvedValue({
        data: null,
        error: { message: "no token" },
      });

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Carol", email: "carol@test.com" },
          session: { id: "s4" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockSetAuthToken).toHaveBeenCalledWith(null);
      });
      expect(mockSetAuthState).toHaveBeenCalledWith("failed");
    });

    it("sets isLoading=false and authServiceDown=true when token() returns empty data", async () => {
      mockToken.mockResolvedValue({
        data: null,
        error: { message: "no token" },
      });

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Dan", email: "dan@test.com" },
          session: { id: "s5" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(result.current.authServiceDown).toBe(true);
      });
      expect(result.current.isLoading).toBe(false);
      expect(result.current.isAuthenticated).toBe(false);
    });

    it("sets isLoading=false and authServiceDown=true when token() rejects", async () => {
      mockToken.mockRejectedValue(new Error("token endpoint down"));

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Eve", email: "eve@test.com" },
          session: { id: "s6" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(result.current.authServiceDown).toBe(true);
      });
      expect(result.current.isLoading).toBe(false);
      expect(result.current.isAuthenticated).toBe(false);
    });

    it("resets tokenFetchedForSession on empty token allowing retry", async () => {
      mockToken
        .mockResolvedValueOnce({ data: null, error: { message: "no token" } })
        .mockResolvedValueOnce({ data: { token: "jwt-retry" }, error: null });

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Faye", email: "faye@test.com" },
          session: { id: "s7" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { rerender } = renderHook(() => mod.useAuth(), { wrapper });

      await waitFor(() => {
        expect(mockToken).toHaveBeenCalledTimes(1);
      });
      await waitFor(() => {
        expect(mockSetAuthState).toHaveBeenCalledWith("failed");
      });

      mockSessionData = {
        data: {
          user: { id: "u1", name: "Faye", email: "faye@test.com" },
          session: { id: "s7" },
        },
        isPending: false,
        error: null,
      };

      rerender();

      await waitFor(() => {
        expect(mockToken).toHaveBeenCalledTimes(2);
      });
    });
  });

  // =========================================================================
  // Sign flows
  // =========================================================================
  describe("sign flows", () => {
    it("signIn stores returnTo in sessionStorage", async () => {
      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s1" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      await act(async () => {
        await result.current.signIn("github");
      });

      expect(sessionStorage.getItem("loom-auth-return-to")).toBe(
        window.location.pathname + window.location.search,
      );
    });

    it("signIn calls signIn.social with provider", async () => {
      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s1" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      await act(async () => {
        await result.current.signIn("google");
      });

      expect(mockSignInSocial).toHaveBeenCalledWith({
        provider: "google",
        callbackURL: window.location.origin,
      });
    });

    it("signOut dispatches auth-sign-out event", async () => {
      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s1" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      const eventListener = vi.fn();
      window.addEventListener("auth-sign-out", eventListener);

      await act(async () => {
        await result.current.signOut();
      });

      expect(eventListener).toHaveBeenCalledTimes(1);
      window.removeEventListener("auth-sign-out", eventListener);
    });

    it("signOut clears auth token and state", async () => {
      mockSessionData = {
        data: {
          user: { id: "u1", name: "Alice", email: "alice@test.com" },
          session: { id: "s1" },
        },
        isPending: false,
        error: null,
      };

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.ExternalAuthProvider>{children}</mod.ExternalAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      // Clear any calls from initial render
      mockSetAuthToken.mockClear();
      mockSetAuthState.mockClear();

      await act(async () => {
        await result.current.signOut();
      });

      expect(mockSignOut).toHaveBeenCalled();
      expect(mockSetAuthToken).toHaveBeenCalledWith(null);
    });
  });

  // =========================================================================
  // NoAuthProvider
  // =========================================================================
  describe("NoAuthProvider", () => {
    it("provides static no-auth context", () => {
      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.NoAuthProvider>{children}</mod.NoAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      expect(result.current.mode).toBe("open");
      expect(result.current.user).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(result.current.isAuthenticated).toBe(true);
      expect(result.current.authServiceDown).toBe(false);
    });

    it("signIn and signOut are no-ops", async () => {
      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.NoAuthProvider>{children}</mod.NoAuthProvider>
      );

      const { result } = renderHook(() => mod.useAuth(), { wrapper });

      // Should not throw
      await act(async () => {
        await result.current.signIn("github");
        await result.current.signOut();
      });
    });

    it("does not call getAuthClient()", () => {
      mockGetAuthClient.mockClear();

      const wrapper = ({ children }: { children: React.ReactNode }) => (
        <mod.NoAuthProvider>{children}</mod.NoAuthProvider>
      );

      renderHook(() => mod.useAuth(), { wrapper });

      expect(mockGetAuthClient).not.toHaveBeenCalled();
    });
  });

  // =========================================================================
  // useAuth hook
  // =========================================================================
  describe("useAuth", () => {
    it("throws when used outside provider", () => {
      // Suppress React error boundary console.error noise
      const spy = vi.spyOn(console, "error").mockImplementation(() => {});

      expect(() => {
        renderHook(() => mod.useAuth());
      }).toThrow(
        "useAuth must be used within an AuthProvider (ExternalAuthProvider or NoAuthProvider)",
      );

      spy.mockRestore();
    });
  });
});
