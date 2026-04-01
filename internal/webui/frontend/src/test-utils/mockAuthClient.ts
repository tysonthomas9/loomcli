/**
 * Shared factory for creating mock Better Auth client objects.
 * Used by test files that mock @/api/authClient.
 *
 * This is a factory, NOT a vi.mock() setup. Each test file still owns
 * its own vi.mock("@/api/authClient") call (Vitest hoisting requires it
 * at the call site).
 */

import { vi } from "vitest";

// Session state shape matching Better Auth's useSession() return
interface SessionState {
  data: {
    user: { id: string; name: string; email: string; image?: string };
    session: { id: string };
  } | null;
  isPending: boolean;
  error: Error | null;
}

export interface MockAuthClient {
  /** The mock client object matching getAuthClient() shape */
  client: {
    useSession: () => SessionState;
    token: ReturnType<typeof vi.fn>;
    signIn: { social: ReturnType<typeof vi.fn> };
    signOut: ReturnType<typeof vi.fn>;
  };
  /** Mutable session state — helper methods mutate this */
  sessionState: SessionState;
  /** Mock for authClient.token() */
  mockToken: ReturnType<typeof vi.fn>;
  /** Mock for authClient.signIn.social() */
  mockSignInSocial: ReturnType<typeof vi.fn>;
  /** Mock for authClient.signOut() */
  mockSignOut: ReturnType<typeof vi.fn>;
  /** Reset all mocks and restore sessionState to defaults */
  reset: () => void;
  /** Set session to authenticated state */
  setAuthenticated: (user?: {
    id: string;
    name: string;
    email: string;
    image?: string;
  }) => void;
  /** Set session to unauthenticated state */
  setUnauthenticated: () => void;
  /** Set session to loading/pending state */
  setLoading: () => void;
  /** Set session to error state */
  setError: (message?: string) => void;
}

const DEFAULT_USER = {
  id: "user-1",
  name: "Test User",
  email: "test@example.com",
};

export function createMockAuthClient(): MockAuthClient {
  const sessionState: SessionState = {
    data: null,
    isPending: false,
    error: null,
  };

  const mockToken = vi.fn().mockResolvedValue({ data: { token: "mock-jwt" } });
  const mockSignInSocial = vi.fn().mockResolvedValue(undefined);
  const mockSignOut = vi.fn().mockResolvedValue(undefined);

  const client = {
    useSession: () => sessionState,
    token: mockToken,
    signIn: { social: mockSignInSocial },
    signOut: mockSignOut,
  };

  return {
    client,
    sessionState,
    mockToken,
    mockSignInSocial,
    mockSignOut,

    reset() {
      mockToken.mockReset().mockResolvedValue({ data: { token: "mock-jwt" } });
      mockSignInSocial.mockReset().mockResolvedValue(undefined);
      mockSignOut.mockReset().mockResolvedValue(undefined);
      sessionState.data = null;
      sessionState.isPending = false;
      sessionState.error = null;
    },

    setAuthenticated(user = DEFAULT_USER) {
      sessionState.data = { user, session: { id: "session-1" } };
      sessionState.isPending = false;
      sessionState.error = null;
    },

    setUnauthenticated() {
      sessionState.data = null;
      sessionState.isPending = false;
      sessionState.error = null;
    },

    setLoading() {
      sessionState.isPending = true;
      sessionState.error = null;
    },

    setError(message = "Auth service unavailable") {
      sessionState.data = null;
      sessionState.isPending = false;
      sessionState.error = new Error(message);
    },
  };
}
