import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router-dom";

import "@/styles/index.css";
import { migrateLocalStorage } from "@/utils/migrateLocalStorage";
import { initAuth, getAuthState } from "@/api";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { initErrorReporter, reportError } from "@/api/errorReporter";
import { ToastProvider } from "@/hooks";
import { router } from "@/router";

// Run localStorage migration before anything reads storage.
migrateLocalStorage();

// Install global error handlers early, before auth/render initialization.
initErrorReporter();

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Failed to find root element");
}

// Initialize auth before rendering to ensure token is available for API calls.
// App renders even if auth fails (server may have auth disabled).
initAuth()
  .then(() => {
    const state = getAuthState();
    if (state === "failed") {
      console.error(
        "[Auth] Failed to initialize authentication — API calls will fail",
      );
    } else if (state === "disabled") {
      console.info("[Auth] Authentication disabled by server");
    }
  })
  .catch((error) => {
    console.error("[Auth] Unexpected error during initialization:", error);
  })
  .finally(() => {
    createRoot(rootElement).render(
      <StrictMode>
        <ErrorBoundary
          onError={(error, errorInfo) => {
            reportError("react-error", error, {
              componentStack: errorInfo.componentStack ?? undefined,
            });
          }}
        >
          <ToastProvider>
            <RouterProvider router={router} />
          </ToastProvider>
        </ErrorBoundary>
      </StrictMode>,
    );
  });
