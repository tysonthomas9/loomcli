import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  useRef,
  type ReactNode,
} from "react";

import { getAuthClient } from "@/api/common";
import {
  setAuthToken,
  setAuthState,
  getAuthState,
  onAuthStateChange,
  onAuthTokenExpired,
  type AuthState,
} from "@/api";
import { AUTH_MODE_OPEN, AUTH_MODE_OIDC, type AuthMode } from "@/api/common";

// ============= Types =============

type AuthUser = {
  id: string;
  name: string;
  email: string;
  image?: string | undefined;
};

type AuthContextValue = {
  /** Auth mode from server config */
  mode: AuthMode;
  /** Authenticated user, null if not logged in or mode='none' */
  user: AuthUser | null;
  /** True while session is being determined (initial load or refresh) */
  isLoading: boolean;
  /** True when user has a valid session (always true when mode='none') */
  isAuthenticated: boolean;
  /** True when the auth service is unreachable (network error, not 401) */
  authServiceDown: boolean;
  /** Start sign-in: OAuth provider or email/password */
  signIn: (
    provider: "github" | "google" | "email",
    email?: string,
    password?: string,
  ) => Promise<void>;
  /** Sign up with email/password */
  signUp: (email: string, password: string, name: string) => Promise<void>;
  /** Sign out: clears session, token, dispatches event */
  signOut: () => Promise<void>;
};

// ============= Lazy Hook Extraction =============

// Lazy getter — only calls getAuthClient() on first invocation.
// Safe because ExternalAuthProvider only renders when mode='external',
// after initExternalAuth() has been called (task .11).
let _useSession: ReturnType<typeof getAuthClient>["useSession"] | null = null;
function getUseSession() {
  if (!_useSession) _useSession = getAuthClient().useSession;
  return _useSession;
}

// ============= Token Refresh Deduplication =============

let pendingRefreshPromise: Promise<void> | null = null;
let lastRefreshTimestamp = 0;
const REFRESH_DEBOUNCE_MS = 1000;

async function refreshJwt(): Promise<void> {
  const now = Date.now();
  if (now - lastRefreshTimestamp < REFRESH_DEBOUNCE_MS) return;
  if (pendingRefreshPromise) {
    await pendingRefreshPromise;
    return;
  }

  pendingRefreshPromise = (async () => {
    try {
      const result = await getAuthClient().token();
      if (result.data?.token) {
        setAuthToken(result.data.token);
      } else {
        setAuthToken(null);
        setAuthState("failed");
      }
    } catch {
      setAuthToken(null);
      setAuthState("failed");
    } finally {
      lastRefreshTimestamp = Date.now();
      pendingRefreshPromise = null;
    }
  })();

  await pendingRefreshPromise;
}

// ============= Context =============

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

// ============= ExternalAuthProvider =============

export function ExternalAuthProvider({
  children,
}: {
  children: ReactNode;
}): JSX.Element {
  const { data: session, isPending, error } = getUseSession()();
  const [authServiceDown, setAuthServiceDown] = useState(false);
  const [tokenReady, setTokenReady] = useState(
    () => getAuthState() === "authenticated",
  );
  const tokenFetchedForSession = useRef<string | null>(null);

  // Track authState changes so isAuthenticated waits for JWT
  useEffect(() => {
    return onAuthStateChange((state: AuthState) => {
      setTokenReady(state === "authenticated");
    });
  }, []);

  // When session changes: acquire JWT via authClient.token()
  useEffect(() => {
    if (isPending) return;

    if (error) {
      setAuthServiceDown(true);
      setAuthToken(null);
      setAuthState("failed");
      return;
    }

    setAuthServiceDown(false);

    if (session?.user) {
      // Session exists — acquire RS256 JWT via authClient.token()
      const sessionId = session.session?.id ?? "active";
      if (tokenFetchedForSession.current === sessionId) return;
      tokenFetchedForSession.current = sessionId;

      getAuthClient()
        .token()
        .then((result) => {
          if (result.data?.token) {
            setAuthToken(result.data.token);
          } else {
            setAuthToken(null);
            setAuthState("failed");
          }
        })
        .catch(() => {
          tokenFetchedForSession.current = null;
          setAuthToken(null);
          setAuthState("failed");
        });
    } else {
      tokenFetchedForSession.current = null;
      setAuthToken(null);
    }
  }, [session, isPending, error]);

  // Listen for auth-token-expired notifications from fetchApi's 401 handler
  useEffect(() => {
    function handleTokenExpired() {
      tokenFetchedForSession.current = null;
      refreshJwt();
    }
    return onAuthTokenExpired(handleTokenExpired);
  }, []);

  const user: AuthUser | null = session?.user
    ? {
        id: session.user.id,
        name: session.user.name ?? "",
        email: session.user.email,
        image: session.user.image ?? undefined,
      }
    : null;

  const signIn = useCallback(
    async (
      provider: "github" | "google" | "email",
      email?: string,
      password?: string,
    ) => {
      if (provider === "email") {
        if (!email || !password) throw new Error("Email and password required");
        const result = await getAuthClient().signIn.email({
          email,
          password,
        });
        if (result.error) {
          throw new Error(result.error.message ?? "Sign in failed");
        }
        return;
      }
      try {
        sessionStorage.setItem(
          "loom-auth-return-to",
          window.location.pathname + window.location.search,
        );
      } catch {
        // sessionStorage quota exceeded — acceptable to lose returnTo
      }
      await getAuthClient().signIn.social({
        provider,
        callbackURL: window.location.origin,
      });
    },
    [],
  );

  const signUp = useCallback(
    async (email: string, password: string, name: string) => {
      const result = await getAuthClient().signUp.email({
        email,
        password,
        name,
      });
      if (result.error) {
        throw new Error(result.error.message ?? "Sign up failed");
      }
    },
    [],
  );

  const signOut = useCallback(async () => {
    await getAuthClient().signOut();
    setAuthToken(null);
    window.dispatchEvent(new CustomEvent("auth-sign-out"));
  }, []);

  // isAuthenticated requires both a valid session AND an acquired JWT.
  // Without this, AuthGate unblocks before the Bearer token is ready,
  // causing 401s on the first API calls after a hard refresh.
  const hasSession = !!session?.user;
  const isAuthenticated = hasSession && tokenReady;

  const value: AuthContextValue = {
    mode: AUTH_MODE_OIDC,
    user,
    isLoading: isPending || (hasSession && !tokenReady),
    isAuthenticated,
    authServiceDown,
    signIn,
    signUp,
    signOut,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

// ============= NoAuthProvider =============

const NO_AUTH_VALUE: AuthContextValue = {
  mode: AUTH_MODE_OPEN,
  user: null,
  isLoading: false,
  isAuthenticated: true,
  authServiceDown: false,
  signIn: async () => {},
  signUp: async () => {},
  signOut: async () => {},
};

export function NoAuthProvider({
  children,
}: {
  children: ReactNode;
}): JSX.Element {
  return (
    <AuthContext.Provider value={NO_AUTH_VALUE}>
      {children}
    </AuthContext.Provider>
  );
}

// ============= Hook =============

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (ctx === undefined) {
    throw new Error(
      "useAuth must be used within an AuthProvider (ExternalAuthProvider or NoAuthProvider)",
    );
  }
  return ctx;
}

// ============= Type Exports =============

export type { AuthContextValue, AuthUser };
