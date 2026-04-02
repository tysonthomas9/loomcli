import type { ReactNode } from "react";

import { useAuth } from "@/contexts/AuthContext";
import { LoginPage } from "@/components/LoginPage";

/**
 * AuthGate — renders children only when authenticated.
 * When mode='none' (no auth configured), always renders children.
 * When mode='external', shows LoginPage until user is authenticated.
 */
export function AuthGate({ children }: { children: ReactNode }): JSX.Element {
  const { mode, isLoading, isAuthenticated } = useAuth();

  // No auth configured — pass through
  if (mode === "none") return <>{children}</>;

  // Still checking session
  if (isLoading) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: "100vh",
        }}
      >
        <div style={{ color: "var(--text-secondary, #666)", fontSize: "14px" }}>
          Checking authentication...
        </div>
      </div>
    );
  }

  // Not authenticated — show login
  if (!isAuthenticated) return <LoginPage />;

  // Authenticated — render app
  return <>{children}</>;
}
