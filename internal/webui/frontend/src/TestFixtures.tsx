/**
 * Test fixtures for e2e testing.
 * Provides routes to render components that aren't yet integrated into the main app flow.
 * Only included in development builds.
 */

import { IssueDetailPanel, ToastContainer } from "@/components";
import { WorkspaceTree } from "@/components/WorkspaceTree";
import { SplitDetailSummary } from "@/components/IssueDetailPanel";
import { SessionNamePrompt } from "@/components/TerminalView/layout";
import { HelpPopover } from "@/components/TerminalView/controls";
import {
  useToast,
  ToastProvider,
  StoreContext,
  NO_STORE_CONTEXT,
  WorkspaceProvider,
} from "@/hooks";
import {
  createAgentStore,
  INITIAL_STATE as AGENT_INITIAL,
} from "@/stores/agentStore";
import { createIssueStore } from "@/stores/issueStore";
import type { IssueDetails, Priority, Issue } from "@/types";
import type { Status } from "@/types/issue";
import { useEffect, useRef, useState } from "react";

/**
 * Valid priority values.
 */
const VALID_PRIORITIES = [0, 1, 2, 3, 4] as const;

/**
 * Check if a number is a valid Priority.
 */
function isValidPriority(value: number): value is Priority {
  return VALID_PRIORITIES.includes(value as Priority);
}

/**
 * Parse issue params from URL search string.
 */
function parseIssueParams(search: string): IssueDetails | null {
  const params = new URLSearchParams(search);
  const id = params.get("id");
  const title = params.get("title");
  const status = params.get("status") as Status | null;
  const priorityStr = params.get("priority");

  if (!id || !title || !status || !priorityStr) {
    return null;
  }

  const parsedPriority = parseInt(priorityStr, 10);
  if (isNaN(parsedPriority) || !isValidPriority(parsedPriority)) {
    return null;
  }

  // Build issue details with proper types
  const issue: IssueDetails = {
    id,
    title,
    status,
    priority: parsedPriority,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    dependencies: [],
    dependents: [],
  };

  // Add optional fields only if they exist
  const issueType = params.get("issue_type");
  if (issueType) {
    issue.issue_type = issueType;
  }

  const description = params.get("description");
  if (description) {
    issue.description = description;
  }

  return issue;
}

/**
 * Test fixture for ErrorBoundary e2e tests.
 * Throws an error during render when URL has throw=true parameter.
 *
 * URL: /test/error-boundary?throw=true&errorMessage=...
 */
export function ErrorTriggerFixture(): JSX.Element {
  const params = new URLSearchParams(window.location.search);
  const shouldThrow = params.get("throw") === "true";
  const errorMessage =
    params.get("errorMessage") || "Test error from ErrorTriggerFixture";

  if (shouldThrow) {
    throw new Error(errorMessage);
  }

  return (
    <div
      data-testid="error-boundary-content"
      style={{
        padding: "2rem",
        background: "var(--bg-primary, #1a1a1a)",
        color: "var(--text-primary, #fff)",
        minHeight: "100vh",
      }}
    >
      <h1>Error Boundary Test Fixture</h1>
      <p>This content renders when no error is thrown.</p>
    </div>
  );
}

/**
 * Test fixture for IssueDetailPanel with StatusDropdown.
 * Renders an open panel with the issue specified in URL params.
 *
 * URL: /test/issue-detail-panel?id=xxx&title=xxx&status=xxx&priority=xxx
 */
export function IssueDetailPanelFixture(): JSX.Element {
  const issue = parseIssueParams(window.location.search);

  if (!issue) {
    return (
      <div style={{ padding: "2rem" }}>
        <h1>IssueDetailPanel Test Fixture</h1>
        <p>Missing or invalid URL parameters.</p>
        <p>
          Required: <code>id</code>, <code>title</code>, <code>status</code>,{" "}
          <code>priority</code> (0-4)
        </p>
        <p>
          Example:{" "}
          <code>
            /test/issue-detail-panel?id=test-1&title=Test%20Issue&status=open&priority=2
          </code>
        </p>
      </div>
    );
  }

  return (
    <WorkspaceProvider workspaceId="fixture-workspace">
      <div
        style={{ minHeight: "100vh", background: "var(--bg-primary, #1a1a1a)" }}
      >
        <IssueDetailPanel
          isOpen={true}
          issue={issue}
          onClose={() => window.history.back()}
        />
      </div>
    </WorkspaceProvider>
  );
}

/**
 * Test fixture for Toast e2e tests.
 * Provides buttons to trigger each toast type for testing.
 *
 * URL: /test/toast
 */
export function ToastTestFixture(): JSX.Element {
  const { showToast, dismissAll, dismissToast, toasts } = useToast();

  return (
    <div
      data-testid="toast-test-fixture"
      style={{
        padding: "2rem",
        background: "var(--bg-primary, #1a1a1a)",
        color: "var(--text-primary, #fff)",
        minHeight: "100vh",
      }}
    >
      <ToastContainer toasts={toasts} onDismiss={dismissToast} />
      <h1>Toast Test Fixture</h1>
      <p style={{ marginBottom: "1rem" }}>
        Active toasts: <span data-testid="toast-count">{toasts.length}</span>
      </p>

      <div
        style={{
          display: "flex",
          gap: "8px",
          flexWrap: "wrap",
          marginBottom: "1rem",
        }}
      >
        <button
          data-testid="trigger-success-toast"
          onClick={() => showToast("Success message", { type: "success" })}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Success Toast
        </button>

        <button
          data-testid="trigger-error-toast"
          onClick={() => showToast("Error message", { type: "error" })}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Error Toast
        </button>

        <button
          data-testid="trigger-warning-toast"
          onClick={() => showToast("Warning message", { type: "warning" })}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Warning Toast
        </button>

        <button
          data-testid="trigger-info-toast"
          onClick={() => showToast("Info message", { type: "info" })}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Info Toast
        </button>
      </div>

      <div
        style={{
          display: "flex",
          gap: "8px",
          flexWrap: "wrap",
          marginBottom: "1rem",
        }}
      >
        <button
          data-testid="trigger-short-toast"
          onClick={() =>
            showToast("Short duration toast", { type: "info", duration: 2000 })
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Short Toast (2s)
        </button>

        <button
          data-testid="trigger-persistent-toast"
          onClick={() =>
            showToast("Persistent toast (no auto-dismiss)", {
              type: "info",
              duration: 0,
            })
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Persistent Toast
        </button>
      </div>

      <div
        style={{
          display: "flex",
          gap: "8px",
          flexWrap: "wrap",
          marginBottom: "1rem",
        }}
      >
        <button
          data-testid="trigger-undo-toast"
          onClick={() =>
            showToast("Item deleted", {
              type: "warning",
              onUndo: () => {
                showToast("Undo successful", { type: "success" });
              },
            })
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Toast with Undo
        </button>

        <button
          data-testid="trigger-rapid-errors"
          onClick={() => {
            showToast("Connection failed", { type: "error" });
            showToast("Connection failed", { type: "error" });
            showToast("Connection failed", { type: "error" });
          }}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Rapid Errors (x3)
        </button>
      </div>

      <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" }}>
        <button
          data-testid="dismiss-all"
          onClick={dismissAll}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Dismiss All
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Fixture helper: read window.__fixtureData
// ---------------------------------------------------------------------------
declare global {
  interface Window {
    __fixtureData?: Record<string, unknown>;
  }
}

function readFixtureData<T>(key: string, fallback: T): T {
  const data = window.__fixtureData;
  if (!data || !(key in data)) return fallback;
  return data[key] as T;
}

const FIXTURE_ROOT_STYLE: React.CSSProperties = {
  minHeight: "100vh",
  background: "var(--bg-primary, #1a1a1a)",
  color: "var(--text-primary, #fff)",
};

// ---------------------------------------------------------------------------
// SessionNamePromptFixture
// URL: /test/session-name-prompt?state=open|closed&existingNames=foo,bar
// ---------------------------------------------------------------------------
export function SessionNamePromptFixture(): JSX.Element {
  const params = new URLSearchParams(window.location.search);
  const isOpen = params.get("state") !== "closed";
  const existingNames = (params.get("existingNames") ?? "")
    .split(",")
    .filter(Boolean);

  const [confirmedNames, setConfirmedNames] = useState<string[]>([]);
  const [cancelCount, setCancelCount] = useState(0);

  return (
    <div data-testid="fixture-root" style={FIXTURE_ROOT_STYLE}>
      <SessionNamePrompt
        isOpen={isOpen}
        existingNames={existingNames}
        onConfirm={(name) => setConfirmedNames((prev) => [...prev, name])}
        onCancel={() => setCancelCount((c) => c + 1)}
      />
      <span data-testid="confirmed-names">{confirmedNames.join(",")}</span>
      <span data-testid="cancel-count">{cancelCount}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// HelpPopoverFixture
// URL: /test/help-popover (always open)
// ---------------------------------------------------------------------------
export function HelpPopoverFixture(): JSX.Element {
  return (
    <div
      data-testid="fixture-root"
      style={{ ...FIXTURE_ROOT_STYLE, position: "relative" }}
    >
      <HelpPopover isOpen={true} onClose={() => {}} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// PasteConfirmFixture
// URL: /test/paste-confirm
// ---------------------------------------------------------------------------
export function PasteConfirmFixture(): JSX.Element {
  const [text, setText] = useState<string | null>(null);
  const [confirmCount, setConfirmCount] = useState(0);
  const [cancelCount, setCancelCount] = useState(0);
  const pasteButtonRef = useRef<HTMLButtonElement>(null);

  const lines = text?.split("\n") ?? [];
  const visibleLines = lines.slice(0, 10);
  const hiddenCount = Math.max(0, lines.length - visibleLines.length);

  useEffect(() => {
    if (text) pasteButtonRef.current?.focus();
  }, [text]);

  const open = (count: number) => {
    setText(
      Array.from({ length: count }, (_value, index) => `line${index + 1}`).join(
        "\n",
      ),
    );
  };

  const confirm = () => {
    setConfirmCount((count) => count + 1);
    setText(null);
  };

  const cancel = () => {
    setCancelCount((count) => count + 1);
    setText(null);
  };

  return (
    <div data-testid="fixture-root" style={FIXTURE_ROOT_STYLE}>
      <div style={{ display: "flex", gap: 8, padding: 24 }}>
        <button
          type="button"
          data-testid="open-2-lines"
          onClick={() => open(2)}
        >
          Open 2 lines
        </button>
        <button
          type="button"
          data-testid="open-10-lines"
          onClick={() => open(10)}
        >
          Open 10 lines
        </button>
        <button
          type="button"
          data-testid="open-11-lines"
          onClick={() => open(11)}
        >
          Open 11 lines
        </button>
        <button
          type="button"
          data-testid="open-15-lines"
          onClick={() => open(15)}
        >
          Open 15 lines
        </button>
        <button
          type="button"
          data-testid="open-25-lines"
          onClick={() => open(25)}
        >
          Open 25 lines
        </button>
      </div>
      <span data-testid="confirm-count">{confirmCount}</span>
      <span data-testid="cancel-count">{cancelCount}</span>
      {text && (
        <div
          data-testid="paste-dialog-overlay"
          onClick={cancel}
          style={{
            position: "fixed",
            inset: 0,
            display: "grid",
            placeItems: "center",
            background: "rgb(0 0 0 / 45%)",
            zIndex: 1000,
          }}
        >
          <div
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="paste-dialog-title"
            aria-describedby="paste-dialog-desc"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => {
              if (event.key === "Enter") confirm();
              if (event.key === "Escape") cancel();
            }}
            style={{
              width: "min(520px, calc(100vw - 32px))",
              padding: 24,
              borderRadius: 8,
              background: "var(--color-bg-card, #ffffff)",
              color: "var(--color-text-primary, #111827)",
              boxShadow: "0 20px 50px rgb(0 0 0 / 30%)",
            }}
          >
            <h2 id="paste-dialog-title">Paste {lines.length} lines?</h2>
            <p id="paste-dialog-desc">
              You are about to paste multi-line text into the terminal.
            </p>
            <pre
              style={{
                maxHeight: 240,
                overflow: "auto",
                padding: 12,
                borderRadius: 6,
                background: "var(--color-bg-secondary, #f3f4f6)",
              }}
            >
              {visibleLines.join("\n")}
            </pre>
            {hiddenCount > 0 && (
              <p>
                ... and {hiddenCount} more line{hiddenCount === 1 ? "" : "s"}
              </p>
            )}
            <div
              style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}
            >
              <button type="button" onClick={cancel}>
                Cancel
              </button>
              <button ref={pasteButtonRef} type="button" onClick={confirm}>
                Paste
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// WorkspaceTreeFixture
// URL: /test/workspace-tree (uses window.__fixtureData for context)
// ---------------------------------------------------------------------------
export function WorkspaceTreeFixture(): JSX.Element {
  const agents = readFixtureData("agents", []);
  const tasks = readFixtureData("tasks", AGENT_INITIAL.tasks);
  const agentTasks = readFixtureData("agentTasks", {});

  const agentStore = createAgentStore();
  agentStore.setState({
    agents,
    tasks,
    agentTasks,
    isConnected: true,
    connectionState: "connected",
    wasEverConnected: true,
  });

  const storeContextValue = {
    ...NO_STORE_CONTEXT,
    agentStore,
    issueStore: createIssueStore(),
  };

  return (
    <ToastProvider>
      <WorkspaceProvider workspaceId="fixture-workspace">
        <StoreContext.Provider value={storeContextValue}>
          <div data-testid="fixture-root" style={FIXTURE_ROOT_STYLE}>
            <WorkspaceTree />
          </div>
        </StoreContext.Provider>
      </WorkspaceProvider>
    </ToastProvider>
  );
}

// ---------------------------------------------------------------------------
// SplitDetailSummaryFixture
// URL: /test/split-detail-summary?id=&title=&priority=&hasDesign=true&...
// ---------------------------------------------------------------------------
export function SplitDetailSummaryFixture(): JSX.Element {
  const params = new URLSearchParams(window.location.search);
  const id = params.get("id") ?? "fixture-issue-1";
  const title = params.get("title") ?? "Fixture Issue";
  const priorityStr = params.get("priority") ?? "2";
  const hasDesign = params.get("hasDesign") !== "false";
  const description =
    params.get("description") ?? "A test issue for visual regression.";
  const issueType = params.get("issueType") ?? "task";
  const assignee = params.get("assignee") ?? "";

  const parsedPriority = parseInt(priorityStr, 10);
  const priority = (isNaN(parsedPriority) ? 2 : parsedPriority) as Priority;

  const issue: Issue = {
    id,
    title,
    status: "open" as Status,
    priority,
    issue_type: issueType,
    description,
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
    ...(assignee ? { assignee } : {}),
    ...(hasDesign
      ? {
          design:
            "## Summary\nThis is a sample design document for visual regression testing.\n\n## Technical Approach\nImplement feature using existing patterns.",
        }
      : {}),
  };

  return (
    <WorkspaceProvider workspaceId="fixture-workspace">
      <div data-testid="fixture-root" style={FIXTURE_ROOT_STYLE}>
        <SplitDetailSummary
          issue={issue}
          isSavingPriority={false}
          isSavingType={false}
          isSavingAssignee={false}
          agents={[]}
          agentTasks={{}}
          onPrioritySave={async () => {}}
          onTypeSave={async () => {}}
          onAssigneeSave={async () => {}}
        />
      </div>
    </WorkspaceProvider>
  );
}
