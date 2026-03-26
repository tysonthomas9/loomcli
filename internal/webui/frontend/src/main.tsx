import { StrictMode } from "react";
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
  useWorkspaceContext,
} from "@/hooks";
import { IssueSessionProvider } from "@/contexts/IssueSessionContext";
import {
  IssueDetailPanelFixture,
  ErrorTriggerFixture,
  ToastTestFixture,
} from "@/TestFixtures";

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
  const { workspace } = useWorkspaceContext();
  const issueSessionMap = useIssueSessionMap(workspace?.id ?? "");
  return (
    <IssueSessionProvider value={issueSessionMap}>
      {children}
    </IssueSessionProvider>
  );
}

// Simple path-based routing for test fixtures (development only)
function getComponent() {
  const path = window.location.pathname;

  // Test fixture routes - only available in development
  if (import.meta.env.DEV && path === "/test/issue-detail-panel") {
    return <IssueDetailPanelFixture />;
  }

  if (import.meta.env.DEV && path === "/test/error-boundary") {
    return <ErrorTriggerFixture />;
  }

  if (import.meta.env.DEV && path === "/test/toast") {
    return <ToastTestFixture />;
  }

  // Default: render main app
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
                <IssueSessionWrapper>{getComponent()}</IssueSessionWrapper>
              </AgentProvider>
            </WorkspaceProvider>
          </ToastProvider>
        </ErrorBoundary>
      </StrictMode>,
    );
  });
