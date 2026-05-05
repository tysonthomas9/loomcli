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
}

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
}: CreateIssueModalProps): JSX.Element | null {
  const { workspaceId, repos } = useWorkspaceContext();
  const [title, setTitle] = useState("");
  const [issueType, setIssueType] = useState<IssueType>("task");
  const [priority, setPriority] = useState<Priority>(2);
  const [sourceRepo, setSourceRepo] = useState("");
  const [description, setDescription] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  const dialogRef = useRef<HTMLDivElement>(null);
  const titleRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);

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
    if (isOpen) {
      setTitle("");
      setIssueType("task");
      setPriority(2);
      setSourceRepo("");
      setDescription("");
      setIsSubmitting(false);
      setError("");
    }
  }, [isOpen]);

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
