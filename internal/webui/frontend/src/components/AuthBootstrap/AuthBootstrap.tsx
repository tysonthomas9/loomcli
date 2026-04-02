import { useState, useEffect } from "react";
import { Outlet } from "react-router-dom";

import { fetchAppConfig, type AppConfig, AppConfigError } from "@/api/appConfig";
import { initExternalAuth } from "@/api/authClient";
import {
  ExternalAuthProvider,
  NoAuthProvider,
} from "@/contexts/AuthContext";
import { AuthGate } from "@/components/AuthGate";

/**
 * AuthBootstrap — root layout that discovers auth mode and provides context.
 *
 * 1. Fetches GET /api/config to determine auth mode
 * 2. If mode='external': initializes BetterAuth client, wraps in ExternalAuthProvider
 * 3. If mode='none': wraps in NoAuthProvider (pass-through, no auth required)
 * 4. AuthGate inside the provider blocks rendering until authenticated
 * 5. Renders <Outlet /> for child routes
 */
export function AuthBootstrap(): JSX.Element {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchAppConfig()
      .then(setConfig)
      .catch((err) => {
        if (err instanceof AppConfigError) {
          setError(err.message);
        } else {
          setError("Failed to load configuration");
        }
      });
  }, []);

  // Error state — config endpoint unreachable or invalid
  if (error) {
    return (
      <div style={{ display: "flex", alignItems: "center", justifyContent: "center", minHeight: "100vh" }}>
        <div style={{ textAlign: "center", maxWidth: 400 }}>
          <h2 style={{ color: "var(--text-primary, #1a1a1a)", margin: "0 0 8px" }}>Configuration Error</h2>
          <p style={{ color: "var(--text-secondary, #666)", fontSize: "14px", margin: "0 0 16px" }}>{error}</p>
          <button
            onClick={() => { setError(null); setConfig(null); }}
            style={{
              padding: "8px 20px",
              border: "1px solid var(--border-primary, #ccc)",
              borderRadius: "6px",
              background: "transparent",
              cursor: "pointer",
              fontSize: "13px",
            }}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  // Loading state
  if (!config) {
    return (
      <div style={{ display: "flex", alignItems: "center", justifyContent: "center", minHeight: "100vh" }}>
        <div style={{ color: "var(--text-secondary, #666)", fontSize: "14px" }}>
          Loading...
        </div>
      </div>
    );
  }

  // External auth — initialize BetterAuth client and wrap with provider
  if (config.mode === "external") {
    initExternalAuth(config.auth_url);
    return (
      <ExternalAuthProvider>
        <AuthGate>
          <Outlet />
        </AuthGate>
      </ExternalAuthProvider>
    );
  }

  // No auth — pass through
  return (
    <NoAuthProvider>
      <Outlet />
    </NoAuthProvider>
  );
}
