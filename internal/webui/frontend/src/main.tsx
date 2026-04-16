import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router-dom";

import "@/styles/index.css";
import "@wterm/react/css";
import { migrateLocalStorage } from "@/utils/migrateLocalStorage";
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
