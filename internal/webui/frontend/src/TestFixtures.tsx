/**
 * Test fixtures for e2e testing.
 * Provides routes to render components that aren't yet integrated into the main app flow.
 * Only included in development builds.
 */

import { useState } from "react";

import { IssueDetailPanel, ToastContainer } from "@/components";
import { SessionNamePrompt } from "@/components/TerminalView/SessionNamePrompt";
import { useToast } from "@/hooks";
import type { IssueDetails, Priority } from "@/types";
import type { Status } from "@/types/status";

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
    <div
      style={{ minHeight: "100vh", background: "var(--bg-primary, #1a1a1a)" }}
    >
      <IssueDetailPanel
        isOpen={true}
        issue={issue}
        onClose={() => window.history.back()}
      />
    </div>
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

/**
 * Test fixture for SessionNamePrompt e2e tests.
 * Renders the prompt with interactive controls to observe callbacks.
 *
 * URL: /test/session-name-prompt?existingNames=foo,bar
 */
export function SessionNamePromptFixture(): JSX.Element {
  const params = new URLSearchParams(window.location.search);
  const initialExisting =
    params.get("existingNames")?.split(",").filter(Boolean) ?? [];

  const [isOpen, setIsOpen] = useState(true);
  const [confirmedName, setConfirmedName] = useState("");
  const [confirmCount, setConfirmCount] = useState(0);
  const [cancelCount, setCancelCount] = useState(0);
  const [existingNames, setExistingNames] =
    useState<string[]>(initialExisting);

  const handleConfirm = (name: string) => {
    setConfirmedName(name);
    setConfirmCount((c) => c + 1);
    setIsOpen(false);
  };

  const handleCancel = () => {
    setCancelCount((c) => c + 1);
    setIsOpen(false);
  };

  const handleReopen = () => {
    setIsOpen(false);
    setTimeout(() => setIsOpen(true), 0);
  };

  return (
    <div
      data-testid="session-name-fixture"
      style={{
        padding: "2rem",
        background: "var(--bg-primary, #1a1a1a)",
        color: "var(--text-primary, #fff)",
        minHeight: "100vh",
      }}
    >
      <h1>SessionNamePrompt Test Fixture</h1>
      <p>
        Confirmed name:{" "}
        <span data-testid="confirmed-name">{confirmedName}</span>
      </p>
      <p>
        Confirm count:{" "}
        <span data-testid="confirm-count">{confirmCount}</span>
      </p>
      <p>
        Cancel count: <span data-testid="cancel-count">{cancelCount}</span>
      </p>

      <div
        style={{
          display: "flex",
          gap: "8px",
          flexWrap: "wrap",
          marginTop: "1rem",
        }}
      >
        <button
          data-testid="reopen-button"
          onClick={handleReopen}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Reopen
        </button>
        <button
          data-testid="set-existing-foo"
          onClick={() => setExistingNames(["foo"])}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Set existing: foo
        </button>
      </div>

      <SessionNamePrompt
        isOpen={isOpen}
        existingNames={existingNames}
        onConfirm={handleConfirm}
        onCancel={handleCancel}
      />
    </div>
  );
}

/**
 * Test fixture for PasteConfirmDialog e2e tests.
 * Renders a simulated paste confirmation dialog with interactive controls.
 *
 * URL: /test/paste-confirm
 */
const MAX_PREVIEW_LINES = 10;

export function PasteConfirmDialogFixture(): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [pasteText, setPasteText] = useState("");
  const [confirmCount, setConfirmCount] = useState(0);
  const [cancelCount, setCancelCount] = useState(0);

  const handleConfirm = () => {
    setConfirmCount((c) => c + 1);
    setIsOpen(false);
  };

  const handleCancel = () => {
    setCancelCount((c) => c + 1);
    setIsOpen(false);
  };

  const openWith = (text: string) => {
    setPasteText(text);
    setIsOpen(true);
  };

  const lines = pasteText
    .split("\n")
    .filter((_, i, arr) => !(i === arr.length - 1 && arr[arr.length - 1] === ""));
  const lineCount = lines.length;
  const visibleLines = lines.slice(0, MAX_PREVIEW_LINES);
  const hiddenCount = lineCount - visibleLines.length;

  return (
    <div
      data-testid="paste-confirm-fixture"
      style={{
        padding: "2rem",
        background: "var(--bg-primary, #1a1a1a)",
        color: "var(--text-primary, #fff)",
        minHeight: "100vh",
      }}
    >
      <h1>PasteConfirmDialog Test Fixture</h1>
      <p>
        Confirm count:{" "}
        <span data-testid="confirm-count">{confirmCount}</span>
      </p>
      <p>
        Cancel count: <span data-testid="cancel-count">{cancelCount}</span>
      </p>

      <div
        style={{
          display: "flex",
          gap: "8px",
          flexWrap: "wrap",
          marginTop: "1rem",
        }}
      >
        <button
          data-testid="open-2-lines"
          onClick={() => openWith("line1\nline2\n")}
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Open (2 lines)
        </button>
        <button
          data-testid="open-10-lines"
          onClick={() =>
            openWith(
              Array.from({ length: 10 }, (_, i) => `line${i + 1}`).join("\n"),
            )
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Open (10 lines)
        </button>
        <button
          data-testid="open-15-lines"
          onClick={() =>
            openWith(
              Array.from({ length: 15 }, (_, i) => `line${i + 1}`).join("\n"),
            )
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Open (15 lines)
        </button>
        <button
          data-testid="open-25-lines"
          onClick={() =>
            openWith(
              Array.from({ length: 25 }, (_, i) => `line${i + 1}`).join("\n"),
            )
          }
          style={{ padding: "8px 16px", cursor: "pointer" }}
        >
          Open (25 lines)
        </button>
      </div>

      {isOpen && (
        <div
          role="alertdialog"
          aria-modal="true"
          aria-labelledby="paste-confirm-title"
          aria-describedby="paste-confirm-desc"
          style={{
            position: "fixed",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 1000,
          }}
          onClick={handleCancel}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              handleConfirm();
            } else if (e.key === "Escape") {
              e.preventDefault();
              handleCancel();
            }
          }}
        >
          <div
            style={{
              background: "var(--bg-secondary, #2a2a2a)",
              borderRadius: "8px",
              padding: "1.5rem",
              maxWidth: "480px",
              width: "100%",
            }}
            onClick={(e) => e.stopPropagation()}
          >
            <h2 id="paste-confirm-title">
              Paste {lineCount} {lineCount === 1 ? "line" : "lines"}?
            </h2>
            <p id="paste-confirm-desc">
              This will send the following text to the terminal:
            </p>
            <pre
              style={{
                background: "var(--bg-primary, #1a1a1a)",
                padding: "0.75rem",
                borderRadius: "4px",
                overflow: "auto",
                maxHeight: "200px",
                fontFamily: "monospace",
                fontSize: "0.85rem",
              }}
            >
              {visibleLines.join("\n")}
            </pre>
            {hiddenCount > 0 && (
              <p
                style={{
                  color: "var(--text-secondary, #999)",
                  fontSize: "0.85rem",
                  marginTop: "0.5rem",
                }}
              >
                ... and {hiddenCount} more {hiddenCount === 1 ? "line" : "lines"}
              </p>
            )}
            <div
              style={{
                display: "flex",
                justifyContent: "flex-end",
                gap: "8px",
                marginTop: "1rem",
              }}
            >
              <button
                type="button"
                onClick={handleCancel}
                style={{ padding: "8px 16px", cursor: "pointer" }}
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleConfirm}
                style={{ padding: "8px 16px", cursor: "pointer" }}
                autoFocus
              >
                Paste
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
