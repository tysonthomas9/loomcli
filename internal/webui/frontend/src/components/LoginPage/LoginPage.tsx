import { useState, useEffect, useRef, useCallback } from "react";

import { useAuth } from "@/contexts/AuthContext";
import { useFocusTrap } from "@/hooks";

import styles from "./LoginPage.module.css";

interface LoginPageProps {
  error: string | null;
  onErrorClear: () => void;
}

export function LoginPage({
  error,
  onErrorClear,
}: LoginPageProps): JSX.Element {
  const { signIn, authServiceDown } = useAuth();
  const overlayRef = useRef<HTMLDivElement>(null);
  const [loadingProvider, setLoadingProvider] = useState<string | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useFocusTrap(overlayRef, true);

  // Clear stale timeout warning when auth service state changes
  useEffect(() => {
    if (authServiceDown) setTimedOut(false);
  }, [authServiceDown]);

  // 10-second timeout for OAuth redirect
  useEffect(() => {
    if (!loadingProvider) return;
    timeoutRef.current = setTimeout(() => {
      setTimedOut(true);
      setLoadingProvider(null);
    }, 10_000);
    return () => {
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
    };
  }, [loadingProvider]);

  const handleSignIn = useCallback(
    async (provider: "github" | "google") => {
      onErrorClear();
      setTimedOut(false);
      setLoadingProvider(provider);
      try {
        await signIn(provider);
      } catch {
        setLoadingProvider(null);
      }
    },
    [signIn, onErrorClear],
  );

  const isLoading = loadingProvider !== null;

  return (
    <div
      className={styles.overlay}
      role="dialog"
      aria-modal="true"
      aria-labelledby="login-title"
      ref={overlayRef}
      tabIndex={-1}
    >
      <div className={styles.content}>
        <h2 id="login-title" className={styles.title}>
          Sign in to Loom
        </h2>

        {authServiceDown && (
          <p className={styles.errorMessage}>
            Authentication service is currently unavailable. Please try again
            later.
          </p>
        )}

        {error && !authServiceDown && (
          <p className={styles.errorMessage}>Sign-in failed: {error}</p>
        )}

        {timedOut && (
          <p className={styles.warningMessage}>
            Redirect seems stuck. Please try again.
          </p>
        )}

        {!authServiceDown && (
          <>
            <button
              className={styles.githubButton}
              onClick={() => handleSignIn("github")}
              disabled={isLoading}
              type="button"
            >
              {loadingProvider === "github"
                ? "Redirecting\u2026"
                : "Continue with GitHub"}
            </button>
            <button
              className={styles.googleButton}
              onClick={() => handleSignIn("google")}
              disabled={isLoading}
              type="button"
            >
              {loadingProvider === "google"
                ? "Redirecting\u2026"
                : "Continue with Google"}
            </button>
          </>
        )}
      </div>
    </div>
  );
}
