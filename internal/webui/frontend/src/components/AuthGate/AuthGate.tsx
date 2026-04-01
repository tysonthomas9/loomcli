import { useState, useEffect, useRef } from "react";

import { useAuth } from "@/contexts/AuthContext";
import { AUTH_MODE_OIDC } from "@/api/appConfig";
import { LoginPage } from "@/components/LoginPage";

export function AuthGate({
  children,
}: {
  children: React.ReactNode;
}): JSX.Element | null {
  const { mode, isLoading, isAuthenticated } = useAuth();
  const [oauthError, setOauthError] = useState<string | null>(null);
  const returnToHandled = useRef(false);

  // Extract ?error= from URL on mount (OAuth failure redirect)
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const error = params.get("error");
    if (error) {
      setOauthError(error.slice(0, 200));
      window.history.replaceState({}, "", window.location.pathname);
    }
  }, []);

  // Restore deep link from sessionStorage after successful auth
  useEffect(() => {
    if (!isAuthenticated || returnToHandled.current) return;
    returnToHandled.current = true;

    try {
      const returnTo = sessionStorage.getItem("loom-auth-return-to");
      sessionStorage.removeItem("loom-auth-return-to");

      if (returnTo && returnTo.startsWith("/") && !returnTo.includes("://")) {
        window.history.replaceState({}, "", returnTo);
      }
    } catch {
      // sessionStorage unavailable (private browsing) — acceptable degradation
    }
  }, [isAuthenticated]);

  if (isLoading) return null;

  if (mode === AUTH_MODE_OIDC && !isAuthenticated) {
    return (
      <LoginPage error={oauthError} onErrorClear={() => setOauthError(null)} />
    );
  }

  return <>{children}</>;
}
