/**
 * IssueDetailView component.
 * Full-area view that replaces the main content area when viewing issue details.
 * Shows issue ID, title, status, and detail content. Back button/Escape returns
 * to the previous view. Sidebar remains visible.
 */

import { useEffect, useState, useCallback } from "react";

import type { Issue, IssueDetails, IssueWithDependencyMetadata } from "@/types";
import type { ViewMode } from "@/components/ViewSwitcher";
import { getReviewType } from "@/utils/issueCategory";
import { formatStatusLabel } from "@/utils/statusFormat";

import styles from "./IssueDetailView.module.css";

/**
 * Props for the IssueDetailView component.
 */
export interface IssueDetailViewProps {
  issue: Issue | IssueDetails | null;
  isLoading: boolean;
  error: string | null;
  previousView: ViewMode;
  onBack: () => void;
  onApprove: (issue: Issue) => Promise<void>;
  onReject: (issue: Issue, comment: string) => Promise<void>;
  onOpenInTerminal?: (issue: Issue | IssueDetails) => void;
  onCopyLink?: () => void;
  onNavigateToIssue?: (issue: Issue) => void;
}

/**
 * Type guard to check if issue has IssueDetails fields.
 */
function isIssueDetails(issue: Issue | IssueDetails): issue is IssueDetails {
  return (
    "dependents" in issue || "dependencies" in issue || "comments" in issue
  );
}

/**
 * Format a view mode for display in the back button.
 */
function formatViewName(view: ViewMode): string {
  switch (view) {
    case "kanban":
      return "Kanban";
    case "table":
      return "Table";
    case "graph":
      return "Graph";
    case "monitor":
      return "Monitor";
    case "observability":
      return "Observability";
    case "terminal":
      return "Terminal";
    case "workspace":
      return "Workspace";
    case "settings":
      return "Settings";
    case "files":
      return "Files";
    default:
      return "Back";
  }
}

/**
 * Format date for display.
 */
function formatDate(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/**
 * Format issue type for display.
 */
function formatIssueType(type: string | undefined): string {
  if (!type) return "Task";
  if (type === "epic") return "Epic";
  if (type === "task") return "Task";
  if (type === "bug") return "Bug";
  if (type === "feature") return "Feature";
  return type;
}

/**
 * Get the CSS class for a dependency status dot.
 */
function getStatusDotClass(status: string | undefined): string {
  switch (status) {
    case "closed":
      return styles.statusDotClosed ?? "";
    case "in_progress":
      return styles.statusDotInProgress ?? "";
    case "blocked":
      return styles.statusDotBlocked ?? "";
    default:
      return styles.statusDotOpen ?? "";
  }
}

/**
 * Render a dependency/dependent issue as a clickable chip.
 */
function renderDependencyChip(
  dep: IssueWithDependencyMetadata,
  onNavigateToIssue?: (issue: Issue) => void,
): JSX.Element {
  const statusClass = dep.status === "closed" ? styles.dependencyClosed : "";
  const isClickable = !!onNavigateToIssue;
  return (
    <li
      key={dep.id}
      className={`${styles.dependencyChip} ${statusClass} ${isClickable ? styles.clickableChip : ""}`}
      onClick={isClickable ? () => onNavigateToIssue(dep) : undefined}
      role={isClickable ? "button" : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onKeyDown={
        isClickable
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onNavigateToIssue(dep);
              }
            }
          : undefined
      }
    >
      <span
        className={`${styles.statusDot} ${getStatusDotClass(dep.status)}`}
        aria-label={dep.status ?? "open"}
      />
      <span className={styles.dependencyId}>{dep.id}</span>
      <span className={styles.dependencyTitle}>{dep.title}</span>
      {dep.dependency_type && (
        <span className={styles.dependencyType}>{dep.dependency_type}</span>
      )}
    </li>
  );
}

/**
 * IssueDetailView renders issue details in the full main content area.
 * Replaces the current view (kanban, table, etc.) while keeping NavRail and sidebar visible.
 */
export function IssueDetailView({
  issue,
  isLoading,
  error,
  previousView,
  onBack,
  onApprove,
  onReject,
  onOpenInTerminal,
  onCopyLink,
  onNavigateToIssue,
}: IssueDetailViewProps): JSX.Element {
  const [showRejectForm, setShowRejectForm] = useState(false);
  const [rejectComment, setRejectComment] = useState("");
  const [isApproving, setIsApproving] = useState(false);
  const [isRejecting, setIsRejecting] = useState(false);

  // Reset state when issue changes
  useEffect(() => {
    setShowRejectForm(false);
    setRejectComment("");
    setIsApproving(false);
    setIsRejecting(false);
  }, [issue?.id]);

  // Escape key handler — layered to avoid conflicts with inner components
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;

      // Don't navigate back if focused on an input, textarea, or contentEditable
      const target = event.target as HTMLElement;
      if (
        target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.isContentEditable
      ) {
        return;
      }

      // Don't navigate back if a dialog or listbox overlay is open
      // (dropdowns like PriorityDropdown, TypeDropdown handle their own Escape)
      if (
        document.querySelector('[role="dialog"]') ||
        document.querySelector('[role="listbox"]')
      ) {
        return;
      }

      // Don't navigate back if reject form is open — close the form instead
      if (showRejectForm) {
        setShowRejectForm(false);
        return;
      }

      onBack();
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onBack, showRejectForm]);

  const handleApprove = useCallback(async () => {
    if (!issue || isApproving) return;
    setIsApproving(true);
    try {
      await onApprove(issue as Issue);
    } catch {
      setIsApproving(false);
    }
  }, [issue, onApprove, isApproving]);

  const handleRejectSubmit = useCallback(async () => {
    if (!issue || isRejecting || !rejectComment.trim()) return;
    setIsRejecting(true);
    try {
      await onReject(issue as Issue, rejectComment.trim());
    } catch {
      setIsRejecting(false);
    }
  }, [issue, onReject, isRejecting, rejectComment]);

  // Loading state
  if (isLoading) {
    return (
      <div className={styles.container} data-testid="issue-detail-view">
        <div className={styles.headerBar}>
          <button
            className={styles.backButton}
            onClick={onBack}
            data-testid="detail-back-button"
          >
            <svg
              className={styles.backButtonIcon}
              viewBox="0 0 16 16"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M10 12L6 8l4-4"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span className={styles.backButtonText}>
              Back to {formatViewName(previousView)}
            </span>
          </button>
        </div>
        <div className={styles.loadingContainer} data-testid="detail-loading">
          <div className={styles.spinner} />
          <p>Loading issue details...</p>
        </div>
      </div>
    );
  }

  // Error state
  if (error) {
    return (
      <div className={styles.container} data-testid="issue-detail-view">
        <div className={styles.headerBar}>
          <button
            className={styles.backButton}
            onClick={onBack}
            data-testid="detail-back-button"
          >
            <svg
              className={styles.backButtonIcon}
              viewBox="0 0 16 16"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M10 12L6 8l4-4"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span className={styles.backButtonText}>
              Back to {formatViewName(previousView)}
            </span>
          </button>
        </div>
        <div className={styles.errorContainer} data-testid="detail-error">
          <p className={styles.errorMessage}>{error}</p>
        </div>
      </div>
    );
  }

  // No issue
  if (!issue) {
    return (
      <div className={styles.container} data-testid="issue-detail-view">
        <div className={styles.headerBar}>
          <button
            className={styles.backButton}
            onClick={onBack}
            data-testid="detail-back-button"
          >
            <svg
              className={styles.backButtonIcon}
              viewBox="0 0 16 16"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M10 12L6 8l4-4"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span className={styles.backButtonText}>
              Back to {formatViewName(previousView)}
            </span>
          </button>
        </div>
        <div className={styles.errorContainer}>
          <p className={styles.errorMessage}>Issue not found</p>
        </div>
      </div>
    );
  }

  const issueHasDetails = isIssueDetails(issue);
  const dependencies = issueHasDetails ? issue.dependencies : undefined;
  const dependents = issueHasDetails ? issue.dependents : undefined;
  const reviewType = getReviewType(issue);
  const isReviewItem = reviewType !== null;

  return (
    <div className={styles.container} data-testid="issue-detail-view">
      {/* Header Bar */}
      <div className={styles.headerBar}>
        <button
          className={styles.backButton}
          onClick={onBack}
          data-testid="detail-back-button"
        >
          <svg
            className={styles.backButtonIcon}
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="M10 12L6 8l4-4"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <span className={styles.backButtonText}>
            Back to {formatViewName(previousView)}
          </span>
        </button>
        <span className={styles.issueIdBadge} data-testid="detail-issue-id">
          {issue.id}
        </span>
        <span
          className={styles.statusBadge}
          data-status={issue.status ?? "open"}
          data-testid="detail-status-badge"
        >
          {formatStatusLabel(issue.status ?? "open")}
        </span>
        <h1 className={styles.headerTitle} data-testid="detail-title">
          {issue.title}
        </h1>
        {onOpenInTerminal && (
          <button
            type="button"
            className={styles.openTerminalButton}
            onClick={() => onOpenInTerminal(issue)}
            data-testid="open-in-terminal-button"
          >
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <rect
                x="1"
                y="2"
                width="14"
                height="12"
                rx="2"
                stroke="currentColor"
                strokeWidth="1.5"
              />
              <path
                d="M4 7l2.5 2L4 11M9 11h3"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            Open in Terminal
          </button>
        )}
        {onCopyLink && (
          <button
            type="button"
            className={styles.copyLinkButton}
            onClick={onCopyLink}
            aria-label="Copy link"
            data-testid="copy-link-button"
          >
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M6.75 9.25l2.5-2.5M10 6.5a2.25 2.25 0 0 1 0 3.18l-1.75 1.75a2.25 2.25 0 0 1-3.18-3.18l.88-.88M6 9.5a2.25 2.25 0 0 1 0-3.18l1.75-1.75a2.25 2.25 0 0 1 3.18 3.18l-.88.88"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            Copy Link
          </button>
        )}
      </div>

      {/* Scrollable Content Area */}
      <div className={styles.contentArea}>
        {/* Metadata */}
        <div className={styles.metadataBar}>
          <span className={styles.metadataItem} data-testid="detail-type">
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M2 4h12M2 8h12M2 12h8"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
            {formatIssueType(issue.issue_type)}
          </span>
          {issue.owner && (
            <span className={styles.metadataItem} data-testid="detail-owner">
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle
                  cx="8"
                  cy="5"
                  r="3"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
                <path
                  d="M2 14c0-2.5 2.5-4 6-4s6 1.5 6 4"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
              {issue.owner}
            </span>
          )}
          {issue.assignee && (
            <span className={styles.metadataItem} data-testid="detail-assignee">
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle
                  cx="8"
                  cy="5"
                  r="3"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
                <path
                  d="M2 14c0-2.5 2.5-4 6-4s6 1.5 6 4"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
              @{issue.assignee}
            </span>
          )}
          {issue.created_at && (
            <span className={styles.metadataItem} data-testid="detail-created">
              Created: {formatDate(issue.created_at)}
            </span>
          )}
          <span className={styles.metadataItem} data-testid="detail-priority">
            P{issue.priority}
          </span>
        </div>

        {/* Review Action Bar */}
        {isReviewItem && !showRejectForm && (
          <div
            className={styles.reviewActionBar}
            data-testid="detail-review-action-bar"
          >
            <button
              type="button"
              className={styles.reviewApproveButton}
              onClick={handleApprove}
              disabled={isApproving}
              aria-label="Approve"
              data-testid="detail-approve-button"
            >
              {isApproving ? "..." : "\u2713"} Approve
            </button>
            <button
              type="button"
              className={styles.reviewRejectButton}
              onClick={() => setShowRejectForm(true)}
              aria-label="Reject"
              data-testid="detail-reject-button"
            >
              {"\u2717"} Reject
            </button>
          </div>
        )}

        {/* Reject Form */}
        {showRejectForm && (
          <div className={styles.reviewActionBar}>
            <textarea
              value={rejectComment}
              onChange={(e) => setRejectComment(e.target.value)}
              placeholder="Reason for rejection..."
              rows={3}
              style={{ flex: 1, resize: "vertical" }}
              data-testid="detail-reject-comment"
            />
            <button
              type="button"
              className={styles.reviewRejectButton}
              onClick={handleRejectSubmit}
              disabled={isRejecting || !rejectComment.trim()}
              data-testid="detail-reject-submit"
            >
              {isRejecting ? "..." : "Submit"}
            </button>
            <button
              type="button"
              className={styles.backButton}
              onClick={() => setShowRejectForm(false)}
              disabled={isRejecting}
            >
              Cancel
            </button>
          </div>
        )}

        {/* Description */}
        {issue.description && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>Description</h3>
            <p className={styles.description}>{issue.description}</p>
          </section>
        )}

        {/* Design */}
        {issue.design && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>Design</h3>
            <p className={styles.description}>{issue.design}</p>
          </section>
        )}

        {/* Notes */}
        {issue.notes && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>Notes</h3>
            <p className={styles.description}>{issue.notes}</p>
          </section>
        )}

        {/* Dependencies */}
        {dependencies && dependencies.length > 0 && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>
              Dependencies ({dependencies.length})
            </h3>
            <ul className={styles.dependencyList}>
              {dependencies.map((dep) =>
                renderDependencyChip(dep, onNavigateToIssue),
              )}
            </ul>
          </section>
        )}

        {/* Dependents */}
        {dependents && dependents.length > 0 && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>
              Blocks ({dependents.length})
            </h3>
            <ul className={styles.dependencyList}>
              {dependents.map((dep) =>
                renderDependencyChip(dep, onNavigateToIssue),
              )}
            </ul>
          </section>
        )}

        {/* Comments */}
        {issueHasDetails && issue.comments && issue.comments.length > 0 && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>
              Comments ({issue.comments.length})
            </h3>
            {issue.comments.map((comment, idx) => (
              <div key={comment.id ?? idx} style={{ marginBottom: 12 }}>
                <div style={{ fontSize: 12, color: "var(--color-text-muted)" }}>
                  {comment.author} &middot;{" "}
                  {comment.created_at ? formatDate(comment.created_at) : ""}
                </div>
                <p className={styles.description}>{comment.text}</p>
              </div>
            ))}
          </section>
        )}

        {/* Labels */}
        {issue.labels && issue.labels.length > 0 && (
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>Labels</h3>
            <div className={styles.labels}>
              {issue.labels.map((label) => (
                <span key={label} className={styles.label}>
                  {label}
                </span>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
