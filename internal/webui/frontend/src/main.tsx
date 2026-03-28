import { StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { RouterProvider } from "react-router-dom";

import "@/styles/index.css";
import { migrateLocalStorage } from "@/utils/migrateLocalStorage";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { BootError } from "@/components/BootError";
import { initErrorReporter, reportError } from "@/api/errorReporter";
import { ToastProvider } from "@/hooks";
import { router } from "@/router";
import { fetchAppConfig, type AppConfig } from "@/api/appConfig";
import { initExternalAuth } from "@/api/authClient";
import { ExternalAuthProvider, NoAuthProvider } from "@/contexts/AuthContext";
import { AuthGate } from "@/components/AuthGate";

// Run localStorage migration before anything reads storage.
migrateLocalStorage();

// Install global error handlers early, before auth/render initialization.
initErrorReporter();

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Failed to find root element");
}

// Reuse root across retries — React errors if createRoot is called twice on the same element.
let root: Root | null = null;

function getRoot(): Root {
  if (!root) root = createRoot(rootElement!);
  return root;
}

function renderBootError(error: unknown): void {
  getRoot().render(
    <StrictMode>
      <BootError error={error} onRetry={bootAndRender} />
    </StrictMode>,
  );
}

function renderApp(config: AppConfig): void {
  const routerElement = <RouterProvider router={router} />;

  const authWrapped =
    config.mode === "external" ? (
      <ExternalAuthProvider>
        <AuthGate>{routerElement}</AuthGate>
      </ExternalAuthProvider>
    ) : (
      <NoAuthProvider>{routerElement}</NoAuthProvider>
    );

  getRoot().render(
    <StrictMode>
      <ErrorBoundary
        onError={(error, errorInfo) => {
          reportError("react-error", error, {
            componentStack: errorInfo.componentStack ?? undefined,
          });
        }}
      >
        <ToastProvider>{authWrapped}</ToastProvider>
      </ErrorBoundary>
    </StrictMode>,
  );
}

async function bootAndRender(): Promise<void> {
  let config: AppConfig;
  try {
    config = await fetchAppConfig();
  } catch (error) {
    renderBootError(error);
    return;
  }

  if (config.mode === "external") {
    initExternalAuth(config.auth_url);
  }

  renderApp(config);
}

bootAndRender().catch((error) => {
  console.error("[Boot] Fatal error:", error);
  renderBootError(error);
});
