import { useState, useEffect, useRef, useCallback } from "react";

import { PRODUCT_NAME } from "@/utils/brand";
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
  const { signIn, signUp, authServiceDown } = useAuth();
  const overlayRef = useRef<HTMLDivElement>(null);
  const [loadingProvider, setLoadingProvider] = useState<string | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [isSignUp, setIsSignUp] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useFocusTrap(overlayRef, true);

  useEffect(() => {
    if (authServiceDown) setTimedOut(false);
  }, [authServiceDown]);

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

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      onErrorClear();
      setFormError(null);
      setSubmitting(true);
      try {
        if (isSignUp) {
          await signUp(email, password, name);
        } else {
          await signIn("email", email, password);
        }
      } catch (err) {
        setFormError(
          err instanceof Error ? err.message : "Authentication failed",
        );
      } finally {
        setSubmitting(false);
      }
    },
    [isSignUp, email, password, name, signIn, signUp, onErrorClear],
  );

  const isLoading = loadingProvider !== null || submitting;

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
          Sign in to {PRODUCT_NAME}
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

        {formError && !authServiceDown && (
          <p className={styles.errorMessage}>{formError}</p>
        )}

        {timedOut && (
          <p className={styles.warningMessage}>
            Redirect seems stuck. Please try again.
          </p>
        )}

        {!authServiceDown && (
          <>
            <form onSubmit={handleSubmit} className={styles.form}>
              {isSignUp && (
                <input
                  type="text"
                  placeholder="Name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className={styles.input}
                  autoComplete="name"
                />
              )}
              <input
                type="email"
                placeholder="Email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className={styles.input}
                autoComplete="email"
                required
              />
              <input
                type="password"
                placeholder="Password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className={styles.input}
                autoComplete={isSignUp ? "new-password" : "current-password"}
                required
                minLength={8}
              />
              <button
                type="submit"
                className={styles.submitButton}
                disabled={isLoading}
              >
                {submitting
                  ? isSignUp
                    ? "Creating account\u2026"
                    : "Signing in\u2026"
                  : isSignUp
                    ? "Create account"
                    : "Sign in"}
              </button>
            </form>

            <p className={styles.toggleText}>
              {isSignUp ? "Already have an account?" : "Don't have an account?"}{" "}
              <button
                type="button"
                className={styles.toggleButton}
                onClick={() => {
                  setIsSignUp(!isSignUp);
                  setFormError(null);
                }}
              >
                {isSignUp ? "Sign in" : "Sign up"}
              </button>
            </p>

            <div className={styles.divider}>
              <span className={styles.dividerText}>or</span>
            </div>

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
