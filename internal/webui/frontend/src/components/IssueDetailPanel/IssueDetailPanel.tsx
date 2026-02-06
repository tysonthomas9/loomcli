/**
 * IssueDetailPanel component.
 * Slide-out side panel that displays detailed information about a selected issue.
 * Features improved information hierarchy with sticky header, collapsible sections,
 * and markdown rendering for design field.
 */

import { useEffect, useRef, useState, useCallback } from 'react';

import {
  updateIssue,
  addDependency,
  removeDependency,
  getTaskLogPhases,
  getTaskLogStreamUrl,
} from '@/api';
import { useLogStream } from '@/hooks';
import type {
  Issue,
  IssueDetails,
  IssueWithDependencyMetadata,
  Priority,
  IssueType,
  DependencyType,
  Comment,
} from '@/types';
import type { Status } from '@/types/status';
import { getReviewType } from '@/utils/reviewType';

import { CommentForm } from './CommentForm';
import { CommentsSection } from './CommentsSection';
import { DependencySection } from './DependencySection';
import { EditableDescription } from './EditableDescription';
import { IssueHeader } from './IssueHeader';
import { MarkdownRenderer } from './MarkdownRenderer';
import { PriorityDropdown } from './PriorityDropdown';
import { RejectCommentForm } from './RejectCommentForm';
import { TypeDropdown } from './TypeDropdown';
import { ErrorToast } from '../ErrorToast';
import { LogViewer } from '../LogViewer';
import styles from './IssueDetailPanel.module.css';

/**
 * Props for CollapsibleSection.
 */
interface CollapsibleSectionProps {
  title: string;
  count?: number;
  defaultExpanded?: boolean;
  children: React.ReactNode;
  testId?: string;
}

/**
 * Collapsible section with chevron indicator.
 */
function CollapsibleSection({
  title,
  count,
  defaultExpanded = true,
  children,
  testId,
}: CollapsibleSectionProps): JSX.Element {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded);

  return (
    <section className={styles.collapsibleSection} data-testid={testId}>
      <button
        type="button"
        className={styles.collapsibleHeader}
        onClick={() => setIsExpanded(!isExpanded)}
        aria-expanded={isExpanded}
      >
        <span className={styles.collapsibleTitle}>
          {title}
          {count !== undefined && <span className={styles.collapsibleCount}>({count})</span>}
        </span>
        <svg
          className={`${styles.chevron} ${isExpanded ? styles.chevronExpanded : ''}`}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M6 4l4 4-4 4"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
      {isExpanded && <div className={styles.collapsibleContent}>{children}</div>}
    </section>
  );
}

/**
 * Blocking banner component - shows when issue has open dependencies.
 * Displays as a visual indicator (non-interactive).
 */
interface BlockingBannerProps {
  openBlockerCount: number;
}

function BlockingBanner({ openBlockerCount }: BlockingBannerProps): JSX.Element | null {
  if (openBlockerCount === 0) return null;

  return (
    <div className={styles.blockingBanner} role="alert" data-testid="blocking-banner">
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d="M8 1L1 15h14L8 1z"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path d="M8 6v3M8 11.5v.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      </svg>
      Blocked by {openBlockerCount} {openBlockerCount === 1 ? 'issue' : 'issues'}
    </div>
  );
}

/**
 * Format issue type for display.
 */
function formatIssueType(type: IssueType | undefined): string {
  if (!type) return 'Task';
  if (type === 'epic') return 'Epic';
  if (type === 'task') return 'Task';
  if (type === 'bug') return 'Bug';
  if (type === 'feature') return 'Feature';
  return type;
}

/**
 * Format date for display.
 */
function formatDate(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

/**
 * Props for the IssueDetailPanel component.
 */
export interface IssueDetailPanelProps {
  /** Whether the panel is open */
  isOpen: boolean;
  /** The issue to display (null when closed or loading) */
  issue: Issue | IssueDetails | null;
  /** Callback when panel should close */
  onClose: () => void;
  /** Whether the issue details are loading */
  isLoading?: boolean;
  /** Error message if loading failed */
  error?: string | null;
  /** Additional CSS class name */
  className?: string;
  /** Children to render in the panel content area (overrides default content) */
  children?: React.ReactNode;
  /** Callback when approve button is clicked (only for review items) */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment (only for review items) */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
}

/**
 * Type guard to check if issue has IssueDetails fields.
 * Checks for fields that indicate this is a detailed issue response.
 * Note: The backend may omit empty arrays (dependents, dependencies),
 * but always includes comments array in IssueDetails responses.
 */
function isIssueDetails(issue: Issue | IssueDetails): issue is IssueDetails {
  // Check for any IssueDetails-specific field that the backend includes
  // Comments is always present in /api/issues/{id} responses
  return 'dependents' in issue || 'dependencies' in issue || 'comments' in issue;
}

/**
 * Render a dependency/dependent issue link.
 */
function renderDependencyItem(dep: IssueWithDependencyMetadata): JSX.Element {
  const statusClass = dep.status === 'closed' ? styles.dependencyClosed : '';
  return (
    <li key={dep.id} className={`${styles.dependencyItem} ${statusClass}`}>
      <span className={styles.dependencyId}>{dep.id}</span>
      <span className={styles.dependencyTitle}>{dep.title}</span>
      {dep.dependency_type && <span className={styles.dependencyType}>{dep.dependency_type}</span>}
    </li>
  );
}

/**
 * Props for the DefaultContent component.
 */
interface DefaultContentProps {
  issue: Issue | IssueDetails | null;
  isLoading: boolean;
  error: string | null;
  onClose: () => void;
  onRetry?: () => void;
  /** Callback when issue is updated (e.g., title changed) */
  onIssueUpdate?: (issue: Issue) => void;
  /** Callback when approve button is clicked */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
  /** Whether the panel is in fullscreen mode */
  isFullscreen?: boolean;
  /** Callback to toggle fullscreen mode */
  onToggleFullscreen?: () => void;
}

/**
 * Default content renderer for issue details.
 */
function DefaultContent({
  issue,
  isLoading,
  error,
  onClose,
  onRetry,
  onIssueUpdate,
  onApprove,
  onReject,
  isFullscreen,
  onToggleFullscreen,
}: DefaultContentProps): JSX.Element {
  const [isSavingTitle, setIsSavingTitle] = useState(false);
  const [isSavingStatus, setIsSavingStatus] = useState(false);
  const [isSavingPriority, setIsSavingPriority] = useState(false);
  const [isSavingType, setIsSavingType] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [showRejectForm, setShowRejectForm] = useState(false);
  const [isApproving, setIsApproving] = useState(false);
  const [isRejecting, setIsRejecting] = useState(false);
  const [rejectError, setRejectError] = useState<string | null>(null);

  // Log tab state (only for task-type issues)
  type LogTabType = 'details' | 'planning' | 'implementation';
  const [activeLogTab, setActiveLogTab] = useState<LogTabType>('details');
  const [availablePhases, setAvailablePhases] = useState<string[]>([]);

  // Check if this is a task-type issue (log tabs only apply to tasks)
  const isTaskType = issue?.issue_type === 'task';

  // Fetch available log phases when issue changes
  useEffect(() => {
    if (!issue || !isTaskType) {
      setAvailablePhases([]);
      setActiveLogTab('details');
      return;
    }

    let cancelled = false;
    getTaskLogPhases(issue.id)
      .then((phases) => {
        if (!cancelled) {
          setAvailablePhases(phases);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setAvailablePhases([]);
        }
      });

    return () => {
      cancelled = true;
    };
    // issue is intentionally excluded - we only need to refetch when ID changes or type changes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issue?.id, isTaskType]);

  // Reset tab when issue changes
  useEffect(() => {
    setActiveLogTab('details');
  }, [issue?.id]);

  // Determine which log URL to connect to
  const shouldConnectToLogs =
    isTaskType && (activeLogTab === 'planning' || activeLogTab === 'implementation');
  const logStreamUrl =
    issue && shouldConnectToLogs ? getTaskLogStreamUrl(issue.id, activeLogTab) : '';

  // Log stream hook
  const {
    lines: logLines,
    state: logConnectionState,
    lastError: logError,
    connect: connectLogs,
    disconnect: disconnectLogs,
    clearLines: clearLogLines,
  } = useLogStream({
    url: logStreamUrl,
    autoConnect: false,
  });

  // Connect/disconnect logs based on tab
  useEffect(() => {
    if (shouldConnectToLogs && logStreamUrl) {
      clearLogLines();
      connectLogs();
    } else {
      disconnectLogs();
    }
  }, [shouldConnectToLogs, logStreamUrl, connectLogs, disconnectLogs, clearLogLines]);

  // Local state for comments to enable optimistic updates
  const hasDetails = issue && isIssueDetails(issue);
  const initialComments = hasDetails ? issue.comments : undefined;
  const [localComments, setLocalComments] = useState<Comment[] | undefined>(initialComments);

  // Sync local comments when issue changes (e.g., different issue selected)
  useEffect(() => {
    if (issue && isIssueDetails(issue)) {
      setLocalComments(issue.comments);
    } else {
      setLocalComments(undefined);
    }
  }, [issue]);

  // Handler for when a new comment is added
  const handleCommentAdded = useCallback((newComment: Comment) => {
    setLocalComments((prev) => {
      if (!prev) return [newComment];
      return [...prev, newComment];
    });
  }, []);

  const handleTitleSave = useCallback(
    async (newTitle: string) => {
      if (!issue) return;

      setIsSavingTitle(true);
      try {
        const updatedIssue = await updateIssue(issue.id, { title: newTitle });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingTitle(false);
      }
    },
    [issue, onIssueUpdate]
  );

  const handleStatusChange = useCallback(
    async (newStatus: Status) => {
      if (!issue) return;

      setIsSavingStatus(true);
      setStatusError(null);
      try {
        const updatedIssue = await updateIssue(issue.id, { status: newStatus });
        onIssueUpdate?.(updatedIssue);
      } catch (err) {
        const message = err instanceof Error ? err.message : 'Failed to update status';
        setStatusError(message);
      } finally {
        setIsSavingStatus(false);
      }
    },
    [issue, onIssueUpdate]
  );

  const handlePrioritySave = useCallback(
    async (newPriority: Priority) => {
      if (!issue) return;

      setIsSavingPriority(true);
      try {
        const updatedIssue = await updateIssue(issue.id, { priority: newPriority });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingPriority(false);
      }
    },
    [issue, onIssueUpdate]
  );

  const handleTypeSave = useCallback(
    async (newType: IssueType) => {
      if (!issue) return;

      setIsSavingType(true);
      try {
        const updatedIssue = await updateIssue(issue.id, { issue_type: newType });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingType(false);
      }
    },
    [issue, onIssueUpdate]
  );

  const handleAddDependency = useCallback(
    async (dependsOnId: string, type: DependencyType) => {
      if (!issue) return;
      await addDependency(issue.id, dependsOnId, type);
      // The parent component should refresh issue details via SSE or manual refetch
    },
    [issue]
  );

  const handleRemoveDependency = useCallback(
    async (dependsOnId: string) => {
      if (!issue) return;
      await removeDependency(issue.id, dependsOnId);
      // The parent component should refresh issue details via SSE or manual refetch
    },
    [issue]
  );

  // Approve handler
  const handleApprove = useCallback(async () => {
    if (!issue || !onApprove || isApproving) return;
    setIsApproving(true);
    try {
      await onApprove(issue as Issue);
    } catch {
      setIsApproving(false);
    }
  }, [issue, onApprove, isApproving]);

  // Reject button click - show form
  const handleRejectClick = useCallback(() => {
    setShowRejectForm(true);
    setRejectError(null);
  }, []);

  // Reject form cancel
  const handleRejectCancel = useCallback(() => {
    if (isRejecting) return;
    setShowRejectForm(false);
    setRejectError(null);
  }, [isRejecting]);

  // Reject form submit
  const handleRejectSubmit = useCallback(
    async (comment: string) => {
      if (!issue || !onReject || isRejecting) return;
      setIsRejecting(true);
      setRejectError(null);
      try {
        await onReject(issue as Issue, comment);
        // On success, panel will update via status change
      } catch (err) {
        setIsRejecting(false);
        const message = err instanceof Error ? err.message : 'Failed to reject';
        setRejectError(message);
      }
    },
    [issue, onReject, isRejecting]
  );

  // Reset reject form state when issue changes
  useEffect(() => {
    setShowRejectForm(false);
    setIsApproving(false);
    setIsRejecting(false);
    setRejectError(null);
  }, [issue?.id]);

  // Loading state
  if (isLoading) {
    return (
      <div className={styles.loadingContainer} data-testid="panel-loading">
        <div className={styles.spinner} />
        <p>Loading issue details...</p>
      </div>
    );
  }

  // Error state
  if (error) {
    return (
      <div className={styles.errorContainer} data-testid="panel-error">
        <p className={styles.errorMessage}>{error}</p>
        {onRetry && (
          <button className={styles.retryButton} onClick={onRetry}>
            Retry
          </button>
        )}
      </div>
    );
  }

  // No issue
  if (!issue) {
    return (
      <div className={styles.emptyContainer}>
        <p>No issue selected</p>
      </div>
    );
  }

  const issueHasDetails = isIssueDetails(issue);
  const dependencies = issueHasDetails ? issue.dependencies : undefined;
  const dependents = issueHasDetails ? issue.dependents : undefined;

  // Determine if this is a review item
  const reviewType = getReviewType(issue);
  const isReviewItem = reviewType !== null;

  // Calculate open blocker count for banner
  const openBlockerCount = dependencies?.filter((d) => d.status !== 'closed').length ?? 0;

  // Auto-collapse logic for Design/Notes (collapse if long)
  const shouldCollapseDesign =
    issue.design && (issue.design.length > 200 || issue.design.split('\n').length > 5);
  const shouldCollapseNotes =
    issue.notes && (issue.notes.length > 200 || issue.notes.split('\n').length > 5);

  return (
    <>
      {/* Sticky Header Wrapper */}
      <div className={styles.stickyHeaderWrapper}>
        {/* Header with ID, status dropdown, priority badge, close button, and title */}
        <IssueHeader
          issue={issue}
          onClose={onClose}
          onTitleSave={handleTitleSave}
          isSavingTitle={isSavingTitle}
          onStatusChange={handleStatusChange}
          isSavingStatus={isSavingStatus}
          showPriority={true}
          sticky={true}
          isReviewItem={isReviewItem}
          isApproving={isApproving}
          isFullscreen={isFullscreen ?? false}
          {...(onToggleFullscreen && { onToggleFullscreen })}
          {...(onApprove && { onApprove: handleApprove })}
          {...(onReject && { onReject: handleRejectClick })}
        />

        {/* Metadata Bar */}
        <div className={styles.metadataBar}>
          <span className={styles.metadataItem} data-testid="metadata-type">
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
            <span className={styles.metadataItem} data-testid="metadata-owner">
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle cx="8" cy="5" r="3" stroke="currentColor" strokeWidth="1.5" />
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
            <span className={styles.metadataItem} data-testid="metadata-assignee">
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle cx="8" cy="5" r="3" stroke="currentColor" strokeWidth="1.5" />
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
            <span className={styles.metadataItem} data-testid="metadata-created">
              Created: {formatDate(issue.created_at)}
            </span>
          )}
        </div>
      </div>

      {/* Reject Comment Form (shown below header when rejecting) */}
      {showRejectForm && onReject && (
        <RejectCommentForm
          issueId={issue.id}
          onSubmit={handleRejectSubmit}
          onCancel={handleRejectCancel}
          isSubmitting={isRejecting}
          error={rejectError}
        />
      )}

      {/* Blocking Banner */}
      <BlockingBanner openBlockerCount={openBlockerCount} />

      {/* Log Tab Bar (only for task-type issues with available phases) */}
      {isTaskType && (availablePhases.length > 0 || activeLogTab !== 'details') && (
        <div className={styles.logTabBar}>
          <button
            type="button"
            className={`${styles.logTab} ${activeLogTab === 'details' ? styles.activeLogTab : ''}`}
            onClick={() => setActiveLogTab('details')}
            aria-selected={activeLogTab === 'details'}
            role="tab"
          >
            Details
          </button>
          {availablePhases.includes('planning') && (
            <button
              type="button"
              className={`${styles.logTab} ${activeLogTab === 'planning' ? styles.activeLogTab : ''}`}
              onClick={() => setActiveLogTab('planning')}
              aria-selected={activeLogTab === 'planning'}
              role="tab"
            >
              Planning
            </button>
          )}
          {availablePhases.includes('implementation') && (
            <button
              type="button"
              className={`${styles.logTab} ${activeLogTab === 'implementation' ? styles.activeLogTab : ''}`}
              onClick={() => setActiveLogTab('implementation')}
              aria-selected={activeLogTab === 'implementation'}
              role="tab"
            >
              Implementation
            </button>
          )}
        </div>
      )}

      {/* Log Viewer (shown when Planning or Implementation tab is active) */}
      {shouldConnectToLogs ? (
        <div className={styles.logsContainer}>
          <LogViewer
            lines={logLines}
            connectionState={logConnectionState}
            error={logError}
            height="100%"
          />
        </div>
      ) : (
        /* Scrollable Content (Details tab) */
        <div className={styles.scrollableContent}>
          {isFullscreen && issue.design ? (
            /* Two-column layout in fullscreen when design exists */
            <div className={styles.twoColumnLayout}>
              <div className={styles.leftColumn}>
                <div className={styles.detailContent}>
                  {/* Priority/Type dropdowns for editing */}
                  <div className={styles.statusRow}>
                    <PriorityDropdown
                      priority={issue.priority as Priority}
                      onSave={handlePrioritySave}
                      isSaving={isSavingPriority}
                    />
                    <TypeDropdown
                      type={issue.issue_type}
                      onSave={handleTypeSave}
                      isSaving={isSavingType}
                    />
                  </div>

                  {/* Description */}
                  <section className={styles.section}>
                    <h3 className={styles.sectionTitle}>Description</h3>
                    <EditableDescription
                      description={issue.description}
                      isEditable={true}
                      onSave={async (newDescription) => {
                        const updatedIssue = await updateIssue(issue.id, {
                          description: newDescription,
                        });
                        onIssueUpdate?.(updatedIssue);
                      }}
                    />
                  </section>

                  {/* Dependencies (blocking this issue) - editable */}
                  {hasDetails && (
                    <DependencySection
                      issueId={issue.id}
                      dependencies={dependencies ?? []}
                      onAddDependency={handleAddDependency}
                      onRemoveDependency={handleRemoveDependency}
                      disabled={isLoading}
                    />
                  )}

                  {/* Dependents (this issue blocks) */}
                  {dependents && dependents.length > 0 && (
                    <section className={styles.section}>
                      <h3 className={styles.sectionTitle}>Blocks ({dependents.length})</h3>
                      <ul className={styles.dependencyList}>
                        {dependents.map(renderDependencyItem)}
                      </ul>
                    </section>
                  )}

                  {/* Comments */}
                  <CommentsSection comments={localComments} />
                  <CommentForm issueId={issue.id} onCommentAdded={handleCommentAdded} />

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
              <div className={styles.rightColumn}>
                <div className={styles.detailContent}>
                  {/* Design (always expanded in fullscreen) */}
                  <section className={styles.section}>
                    <h3 className={styles.sectionTitle}>Design</h3>
                    <MarkdownRenderer content={issue.design} />
                  </section>

                  {/* Notes */}
                  {issue.notes && (
                    <section className={styles.section}>
                      <h3 className={styles.sectionTitle}>Notes</h3>
                      <MarkdownRenderer content={issue.notes} />
                    </section>
                  )}
                </div>
              </div>
            </div>
          ) : (
            /* Single-column layout (panel mode or fullscreen without design) */
            <div className={styles.detailContent}>
              {/* Priority/Type dropdowns for editing */}
              <div className={styles.statusRow}>
                <PriorityDropdown
                  priority={issue.priority as Priority}
                  onSave={handlePrioritySave}
                  isSaving={isSavingPriority}
                />
                <TypeDropdown
                  type={issue.issue_type}
                  onSave={handleTypeSave}
                  isSaving={isSavingType}
                />
              </div>

              {/* Description */}
              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>Description</h3>
                <EditableDescription
                  description={issue.description}
                  isEditable={true}
                  onSave={async (newDescription) => {
                    const updatedIssue = await updateIssue(issue.id, {
                      description: newDescription,
                    });
                    onIssueUpdate?.(updatedIssue);
                  }}
                />
              </section>

              {/* Design (collapsible, markdown rendered) */}
              {issue.design && (
                <CollapsibleSection
                  title="Design"
                  defaultExpanded={!shouldCollapseDesign}
                  testId="design-section"
                >
                  <MarkdownRenderer content={issue.design} />
                </CollapsibleSection>
              )}

              {/* Notes (collapsible) */}
              {issue.notes && (
                <CollapsibleSection
                  title="Notes"
                  defaultExpanded={!shouldCollapseNotes}
                  testId="notes-section"
                >
                  <MarkdownRenderer content={issue.notes} />
                </CollapsibleSection>
              )}

              {/* Dependencies (blocking this issue) - editable */}
              {hasDetails && (
                <DependencySection
                  issueId={issue.id}
                  dependencies={dependencies ?? []}
                  onAddDependency={handleAddDependency}
                  onRemoveDependency={handleRemoveDependency}
                  disabled={isLoading}
                />
              )}

              {/* Dependents (this issue blocks) */}
              {dependents && dependents.length > 0 && (
                <section className={styles.section}>
                  <h3 className={styles.sectionTitle}>Blocks ({dependents.length})</h3>
                  <ul className={styles.dependencyList}>{dependents.map(renderDependencyItem)}</ul>
                </section>
              )}

              {/* Comments */}
              <CommentsSection comments={localComments} />
              <CommentForm issueId={issue.id} onCommentAdded={handleCommentAdded} />

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
          )}
        </div>
      )}

      {/* Error toast for status change failures */}
      {statusError && (
        <ErrorToast
          message={statusError}
          onDismiss={() => setStatusError(null)}
          testId="status-error-toast"
        />
      )}
    </>
  );
}

/**
 * IssueDetailPanel displays issue details in a slide-out panel from the right edge.
 * Features:
 * - Smooth slide-in/out animation with CSS transforms
 * - Backdrop overlay that dims the background
 * - Closes on backdrop click or Escape key
 * - Locks body scroll when open
 * - Accessible with proper ARIA attributes
 * - Default content rendering with loading/error states
 */
export function IssueDetailPanel({
  isOpen,
  issue,
  onClose,
  isLoading,
  error,
  className,
  children,
  onApprove,
  onReject,
}: IssueDetailPanelProps): JSX.Element {
  const panelRef = useRef<HTMLElement>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const handleToggleFullscreen = useCallback(() => {
    setIsFullscreen((prev) => !prev);
  }, []);

  // Reset fullscreen when panel closes
  useEffect(() => {
    if (!isOpen) {
      setIsFullscreen(false);
    }
  }, [isOpen]);

  // Handle Escape key: fullscreen -> panel, panel -> close
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        if (isFullscreen) {
          setIsFullscreen(false);
        } else {
          onClose();
        }
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, isFullscreen, onClose]);

  // Lock body scroll when open, restoring previous value on close.
  // Note: Only ONE panel should be open at a time. Multiple concurrent panels
  // would require a scroll lock manager to handle overflow restoration properly.
  useEffect(() => {
    if (isOpen) {
      const previousOverflow = document.body.style.overflow;
      document.body.style.overflow = 'hidden';
      return () => {
        document.body.style.overflow = previousOverflow;
      };
    }
  }, [isOpen]);

  // Focus management: focus the panel when opened, restore focus on close
  useEffect(() => {
    if (isOpen && panelRef.current) {
      const previouslyFocused = document.activeElement as HTMLElement | null;
      panelRef.current.focus();
      return () => {
        // Check element is still in DOM before restoring focus (could be unmounted)
        if (previouslyFocused && document.contains(previouslyFocused) && previouslyFocused.focus) {
          previouslyFocused.focus();
        }
      };
    }
  }, [isOpen]);

  // Build root class name
  const rootClassName = [
    styles.overlay,
    isOpen && styles.open,
    isFullscreen && styles.fullscreen,
    className,
  ]
    .filter(Boolean)
    .join(' ');

  // Determine content: children override default, otherwise render default content
  const content = children ?? (
    <DefaultContent
      issue={issue}
      isLoading={isLoading ?? false}
      error={error ?? null}
      onClose={onClose}
      isFullscreen={isFullscreen}
      onToggleFullscreen={handleToggleFullscreen}
      {...(onApprove !== undefined && { onApprove })}
      {...(onReject !== undefined && { onReject })}
    />
  );

  return (
    <div
      className={rootClassName}
      onClick={onClose}
      data-testid="issue-detail-overlay"
      aria-hidden={!isOpen}
    >
      <aside
        ref={panelRef}
        className={styles.panel}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={issue ? `Details for ${issue.title}` : 'Issue details'}
        tabIndex={-1}
        data-testid="issue-detail-panel"
        data-state={isOpen ? 'open' : 'closed'}
        data-loading={isLoading ? 'true' : 'false'}
        data-error={error ? 'true' : 'false'}
      >
        <div className={styles.content}>{content}</div>
      </aside>
    </div>
  );
}
