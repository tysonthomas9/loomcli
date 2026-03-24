import { StrictMode, type ReactElement } from "react";
import { createRoot } from "react-dom/client";

import "@/styles/index.css";
import { migrateLocalStorage } from "@/utils/migrateLocalStorage";
import { initAuth, getAuthState } from "@/api";
import App from "@/App";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { initErrorReporter, reportError } from "@/api/errorReporter";
import {
  ToastProvider,
  AgentProvider,
  WorkspaceProvider,
  useIssueSessionMap,
} from "@/hooks";
import { IssueSessionProvider } from "@/contexts/IssueSessionContext";

// Run localStorage migration before anything reads storage.
// ES imports are hoisted, so this executes after all imports but before any React rendering.
migrateLocalStorage();

// Install global error handlers early, before auth/render initialization.
initErrorReporter();

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Failed to find root element");
}

function IssueSessionWrapper({ children }: { children: React.ReactNode }) {
  const issueSessionMap = useIssueSessionMap();
  return (
    <IssueSessionProvider value={issueSessionMap}>
      {children}
    </IssueSessionProvider>
  );
}

/**
 * Resolve which component to render.
 * Test fixture routes are only available in DEV mode and are dynamically
 * imported so the test code is tree-shaken from production bundles.
 */
async function resolveComponent(): Promise<ReactElement> {
  const path = window.location.pathname;

  if (import.meta.env.DEV && path.startsWith("/test/")) {
    const {
      IssueDetailPanelFixture,
      ErrorTriggerFixture,
      ToastTestFixture,
      SessionNamePromptFixture,
      PasteConfirmDialogFixture,
    } = await import("@/TestFixtures");

    const fixtureMap: Record<string, ReactElement> = {
      "/test/issue-detail-panel": <IssueDetailPanelFixture />,
      "/test/error-boundary": <ErrorTriggerFixture />,
      "/test/toast": <ToastTestFixture />,
      "/test/session-name-prompt": <SessionNamePromptFixture />,
      "/test/paste-confirm": <PasteConfirmDialogFixture />,
    };

    const fixture = fixtureMap[path];
    if (fixture) {
      return fixture;
    }
  }

  return <App />;
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
    resolveComponent().then((component) => {
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
              <WorkspaceProvider>
                <AgentProvider>
                  <IssueSessionWrapper>{component}</IssueSessionWrapper>
                </AgentProvider>
              </WorkspaceProvider>
            </ToastProvider>
          </ErrorBoundary>
        </StrictMode>,
      );
    });
  });
