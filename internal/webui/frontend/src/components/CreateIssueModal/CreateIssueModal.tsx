import { useState, useRef, useCallback, useEffect, useMemo } from "react";
import { createPortal } from "react-dom";

import { createIssue } from "@/hooks/api";
import type { CreateIssueRequest } from "@/api/issues";
import {
  useRegisterEscapeLayer,
  LAYER_MODAL,
  useFocusTrap,
  useFocusReturn,
} from "@/hooks/ui";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, IssueType, Priority } from "@/types";
import styles from "./CreateIssueModal.module.css";

export interface CreateIssueModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (issue: Issue) => void | Promise<void>;
  /** Open epics offered in the Epic select (design's New Issue modal). */
  epics?: { id: string; title: string }[];
  initialValues?: {
    title?: string;
    description?: string;
    issueType?: IssueType;
    priority?: Priority;
    sourceRepo?: string;
  };
}

/** Status choices at creation time (design: defaults near the backlog). */
const STATUS_OPTIONS: { value: "open" | "deferred"; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "deferred", label: "Backlog" },
];

const ISSUE_TYPES: { value: IssueType; label: string }[] = [
  { value: "task", label: "Task" },
  { value: "bug", label: "Bug" },
  { value: "feature", label: "Feature" },
  { value: "epic", label: "Epic" },
  { value: "chore", label: "Chore" },
];

const PRIORITIES: { value: Priority; label: string }[] = [
  { value: 0, label: "P0 — Critical" },
  { value: 1, label: "P1 — High" },
  { value: 2, label: "P2 — Medium" },
  { value: 3, label: "P3 — Low" },
  { value: 4, label: "P4 — Backlog" },
];

export function CreateIssueModal({
  isOpen,
  onClose,
  onSuccess,
  epics,
  initialValues,
}: CreateIssueModalProps): JSX.Element | null {
  const { workspaceId, repos, agents = [] } = useWorkspaceContext();
  const [title, setTitle] = useState("");
  const [issueType, setIssueType] = useState<IssueType>("task");
  const [priority, setPriority] = useState<Priority>(2);
  const [sourceRepo, setSourceRepo] = useState("");
  const [parentEpic, setParentEpic] = useState("");
  const [status, setStatus] = useState<"open" | "deferred">("open");
  const [assignee, setAssignee] = useState("");
  const [description, setDescription] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  const dialogRef = useRef<HTMLDivElement>(null);
  const titleRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);
  const wasOpenRef = useRef(false);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const repoOptions = useMemo(
    () =>
      repos
        .map((repo) => ({
          label: repo.name,
          value: repo.source_repo_id || repo.name,
        }))
        .filter((repo) => Boolean(repo.label) && Boolean(repo.value)),
    [repos],
  );
  const defaultSourceRepo =
    repoOptions.length === 1 ? (repoOptions[0]?.value ?? "") : "";

  // Reset form state when modal opens
  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }

    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    setTitle(initialValues?.title ?? "");
    setIssueType(initialValues?.issueType ?? "task");
    setPriority(initialValues?.priority ?? 2);
    setSourceRepo(initialValues?.sourceRepo ?? "");
    setParentEpic("");
    setStatus("open");
    setAssignee("");
    setDescription(initialValues?.description ?? "");
    setIsSubmitting(false);
    setError("");
  }, [
    isOpen,
    initialValues?.title,
    initialValues?.issueType,
    initialValues?.priority,
    initialValues?.sourceRepo,
    initialValues?.description,
  ]);

  useEffect(() => {
    if (!isOpen) return;
    if (repoOptions.length === 1) {
      setSourceRepo(defaultSourceRepo);
    } else if (repoOptions.length === 0) {
      setSourceRepo("");
    }
  }, [isOpen, repoOptions.length, defaultSourceRepo]);

  useRegisterEscapeLayer(LAYER_MODAL, onClose, isOpen);
  useFocusTrap(dialogRef, isOpen, { initialFocus: titleRef });
  useFocusReturn(isOpen);

  const canSubmit = title.trim() !== "" && !isSubmitting;

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setIsSubmitting(true);
      setError("");

      const req: CreateIssueRequest = {
        title: title.trim(),
        issue_type: issueType,
        priority,
      };

      if (description.trim()) {
        req.description = description.trim();
      }
      if (sourceRepo) {
        req.source_repo = sourceRepo;
      }
      // Design's New Issue modal: file straight into an epic, a starting
      // status, and an assignee — all native create-request fields.
      if (parentEpic && issueType !== "epic") {
        req.parent = parentEpic;
      }
      if (status !== "open") {
        req.status = status;
      }
      if (assignee) {
        req.assignee = assignee;
      }

      try {
        const issue = await createIssue(workspaceId, req);
        if (!mountedRef.current) return;
        await onSuccess(issue);
        if (!mountedRef.current) return;
        onClose();
      } catch (err: unknown) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to create issue";
        if (
          typeof err === "object" &&
          err !== null &&
          "body" in err &&
          typeof (err as { body: unknown }).body === "object" &&
          (err as { body: { error?: string } }).body?.error
        ) {
          setError((err as { body: { error: string } }).body.error);
        } else {
          setError(message);
        }
      } finally {
        if (mountedRef.current) {
          setIsSubmitting(false);
        }
      }
    },
    [
      canSubmit,
      title,
      issueType,
      priority,
      sourceRepo,
      parentEpic,
      status,
      assignee,
      description,
      workspaceId,
      onSuccess,
      onClose,
    ],
  );

  if (!isOpen) return null;

  const showRepoSelector = repoOptions.length > 1;
  const showSingleRepo = repoOptions.length === 1;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onClose}
      data-testid="create-issue-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="Create Issue"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className={styles.title}>New Issue</h2>

        <form onSubmit={handleSubmit}>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="issue-title">
              Title
            </label>
            <input
              ref={titleRef}
              id="issue-title"
              className={styles.input}
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Issue title"
              disabled={isSubmitting}
              data-testid="create-issue-title"
            />
          </div>

          <div className={styles.row}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="issue-type">
                Type
              </label>
              <select
                id="issue-type"
                className={styles.select}
                value={issueType}
                onChange={(e) => setIssueType(e.target.value as IssueType)}
                disabled={isSubmitting}
                data-testid="create-issue-type"
              >
                {ISSUE_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="issue-priority">
                Priority
              </label>
              <select
                id="issue-priority"
                className={styles.select}
                value={priority}
                onChange={(e) =>
                  setPriority(Number(e.target.value) as Priority)
                }
                disabled={isSubmitting}
                data-testid="create-issue-priority"
              >
                {PRIORITIES.map((p) => (
                  <option key={p.value} value={p.value}>
                    {p.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className={styles.row}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="issue-epic">
                Epic
              </label>
              <select
                id="issue-epic"
                className={styles.select}
                value={issueType === "epic" ? "" : parentEpic}
                onChange={(e) => setParentEpic(e.target.value)}
                disabled={
                  isSubmitting ||
                  issueType === "epic" ||
                  (epics ?? []).length === 0
                }
                data-testid="create-issue-epic"
              >
                <option value="">
                  {issueType === "epic"
                    ? "Epics have no parent"
                    : (epics ?? []).length === 0
                      ? "No epics yet"
                      : "— None —"}
                </option>
                {(epics ?? []).map((epic) => (
                  <option key={epic.id} value={epic.id}>
                    {epic.title}
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="issue-status">
                Status
              </label>
              <select
                id="issue-status"
                className={styles.select}
                value={status}
                onChange={(e) =>
                  setStatus(e.target.value as "open" | "deferred")
                }
                disabled={isSubmitting}
                data-testid="create-issue-status"
              >
                {STATUS_OPTIONS.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className={styles.row}>
            {(showRepoSelector || showSingleRepo) && (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="issue-source-repo">
                  Repo
                </label>
                <select
                  id="issue-source-repo"
                  className={styles.select}
                  value={sourceRepo}
                  onChange={(e) => setSourceRepo(e.target.value)}
                  disabled={isSubmitting || showSingleRepo}
                  data-testid="create-issue-source-repo"
                >
                  {!showSingleRepo && <option value="">Workspace</option>}
                  {repoOptions.map((repo) => (
                    <option key={repo.value} value={repo.value}>
                      {repo.label}
                    </option>
                  ))}
                </select>
              </div>
            )}

            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="issue-assignee">
                Assignee
              </label>
              <select
                id="issue-assignee"
                className={styles.select}
                value={assignee}
                onChange={(e) => setAssignee(e.target.value)}
                disabled={isSubmitting || agents.length === 0}
                data-testid="create-issue-assignee"
              >
                <option value="">
                  {agents.length === 0 ? "No agents yet" : "Unassigned"}
                </option>
                {agents.map((agent) => (
                  <option key={agent.name} value={agent.name}>
                    {agent.name}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="issue-description">
              Description
            </label>
            <textarea
              id="issue-description"
              className={styles.textarea}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional description"
              disabled={isSubmitting}
              data-testid="create-issue-description"
            />
          </div>

          {error && (
            <p
              className={styles.error}
              data-testid="create-issue-error"
              role="alert"
            >
              {error}
            </p>
          )}

          <div className={styles.actions}>
            <button
              type="button"
              className={styles.cancelButton}
              onClick={onClose}
              disabled={isSubmitting}
              data-testid="create-issue-cancel"
            >
              Cancel
            </button>
            <button
              type="submit"
              className={styles.submitButton}
              disabled={!canSubmit}
              data-testid="create-issue-submit"
            >
              {isSubmitting ? "Creating..." : "Create Issue"}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body,
  );
}
