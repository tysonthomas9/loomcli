import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '@/styles/index.css';
import App from '@/App';
import { ErrorBoundary } from '@/components/ErrorBoundary';
import { ToastProvider, AgentProvider } from '@/hooks';
import { IssueDetailPanelFixture, ErrorTriggerFixture, ToastTestFixture } from '@/TestFixtures';

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error('Failed to find root element');
}

// Simple path-based routing for test fixtures (development only)
function getComponent() {
  const path = window.location.pathname;

  // Test fixture routes - only available in development
  if (import.meta.env.DEV && path === '/test/issue-detail-panel') {
    return <IssueDetailPanelFixture />;
  }

  if (import.meta.env.DEV && path === '/test/error-boundary') {
    return <ErrorTriggerFixture />;
  }

  if (import.meta.env.DEV && path === '/test/toast') {
    return <ToastTestFixture />;
  }

  // Default: render main app
  return <App />;
}

createRoot(rootElement).render(
  <StrictMode>
    <ErrorBoundary>
      <ToastProvider>
        <AgentProvider>{getComponent()}</AgentProvider>
      </ToastProvider>
    </ErrorBoundary>
  </StrictMode>
);
