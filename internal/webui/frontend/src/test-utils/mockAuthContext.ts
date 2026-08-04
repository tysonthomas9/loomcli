/**
 * Shared factory for creating mock AuthContextValue objects.
 * Used by consumer component tests (AuthGate, LoginPage, etc.).
 *
 * Consolidates duplicate defaultAuth() functions that previously
 * existed in AuthGate.test.tsx and LoginPage.test.tsx.
 */

import { vi } from "vitest";
import type { AuthContextValue } from "@/contexts/AuthContext";

/**
 * Create a default unauthenticated auth context value.
 * Matches the shape of AuthContextValue with sensible defaults.
 */
export function createDefaultAuth(
  overrides: Partial<AuthContextValue> = {},
): AuthContextValue {
  return {
    mode: "oidc",
    isLoading: false,
    isAuthenticated: false,
    authServiceDown: false,
    user: null,
    signIn: vi.fn(),
    signUp: vi.fn(),
    signOut: vi.fn(),
    ...overrides,
  };
}

/**
 * Create an authenticated auth context value with a populated user.
 */
export function createAuthenticatedAuth(
  overrides: Partial<AuthContextValue> = {},
): AuthContextValue {
  return createDefaultAuth({
    isAuthenticated: true,
    user: {
      id: "user-1",
      name: "Test User",
      email: "test@example.com",
    },
    ...overrides,
  });
}

/**
 * Create auth context for mode=open (no auth required).
 */
export function createNoAuth(): AuthContextValue {
  return createDefaultAuth({
    mode: "open",
    isAuthenticated: true,
    user: null,
  });
}
