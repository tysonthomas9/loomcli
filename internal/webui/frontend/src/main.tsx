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
import {
  fetchAppConfig,
  AppConfigError,
  AUTH_MODE_OIDC,
  type AppConfig,
} from "@/api/appConfig";
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
    config.mode === AUTH_MODE_OIDC ? (
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

const BOOT_TIMEOUT_MS = 10_000;

async function doBootAndRender(): Promise<void> {
  const config = await fetchAppConfig();

  if (config.mode === AUTH_MODE_OIDC) {
    initExternalAuth(config.auth_url);
  }

  renderApp(config);
}

async function bootAndRender(): Promise<void> {
  let timeoutId: ReturnType<typeof setTimeout> | undefined;

  try {
    await Promise.race([
      doBootAndRender(),
      new Promise<never>((_resolve, reject) => {
        timeoutId = setTimeout(() => {
          reject(new AppConfigError("Application boot timed out"));
        }, BOOT_TIMEOUT_MS);
      }),
    ]);
  } catch (error) {
    renderBootError(error);
  } finally {
    if (timeoutId !== undefined) clearTimeout(timeoutId);
  }
}

bootAndRender().catch((error) => {
  console.error("[Boot] Fatal error:", error);
  renderBootError(error);
});
