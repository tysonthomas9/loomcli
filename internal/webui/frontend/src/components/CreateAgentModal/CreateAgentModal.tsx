import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";
import { useCreateWorkspaceAgent } from "@/hooks/agents";
import { ApiError } from "@/types/common";

import styles from "./CreateAgentModal.module.css";

export interface CreateAgentModalProps {
  isOpen: boolean;
  workspaceId: string;
  repos: RepoInfo[];
  defaultBackend?: string;
  defaultName?: string;
  defaultRoleName?: "task" | "plan";
  onClose: () => void;
  onSuccess: (agent: WorkspaceAgentInfo) => void;
}

export function CreateAgentModal({
  isOpen,
  workspaceId,
  repos,
  defaultBackend,
  defaultName,
  defaultRoleName,
  onClose,
  onSuccess,
}: CreateAgentModalProps): JSX.Element | null {
  const resolvedDefaultBackend = defaultBackend?.trim() || "codex";
  const resolvedDefaultName = defaultName?.trim() ?? "";
  const resolvedDefaultRoleName = defaultRoleName ?? "task";
  const [name, setName] = useState(resolvedDefaultName);
  const [roleName, setRoleName] = useState<string>(resolvedDefaultRoleName);
  const [backend, setBackend] = useState(resolvedDefaultBackend);
  const [repoName, setRepoName] = useState("");
  const [crossRepo, setCrossRepo] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wasOpenRef = useRef(false);
  const createAgent = useCreateWorkspaceAgent(workspaceId);

  const repoOptions = useMemo(() => repos.map((repo) => repo.name), [repos]);
  const selectedRepo = repoName || repoOptions[0] || "";

  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }

    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    setName(resolvedDefaultName);
    setRoleName(resolvedDefaultRoleName);
    setBackend(resolvedDefaultBackend);
    setRepoName("");
    setCrossRepo(false);
    setIsSubmitting(false);
    setError(null);
  }, [
    isOpen,
    resolvedDefaultName,
    resolvedDefaultRoleName,
    resolvedDefaultBackend,
  ]);

  if (!isOpen) return null;

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    const trimmedName = name.trim();
    const trimmedRole = roleName.trim();
    const trimmedBackend = backend.trim();
    if (!trimmedName) {
      setError("Agent name is required");
      return;
    }
    if (!trimmedRole) {
      setError("Role is required");
      return;
    }
    if (!crossRepo && !selectedRepo) {
      setError("Select a repo or enable workspace scope");
      return;
    }

    setIsSubmitting(true);
    try {
      const request = {
        name: trimmedName,
        role_name: trimmedRole,
        auto: false,
        cross_repo: crossRepo,
        repos: crossRepo ? [] : [selectedRepo],
      };
      const agent = await createAgent({
        ...request,
        ...(trimmedBackend ? { backend: trimmedBackend } : {}),
      });
      onSuccess(agent);
      setName(resolvedDefaultName);
      setRoleName(resolvedDefaultRoleName);
      setBackend(resolvedDefaultBackend);
      setRepoName("");
      setCrossRepo(false);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else if (err instanceof Error) {
        setError(err.message);
      } else {
        setError("Failed to create agent");
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className={styles.overlay} onClick={onClose}>
      <div
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby="create-agent-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className={styles.headerRow}>
          <h2 id="create-agent-title" className={styles.title}>
            New Agent
          </h2>
          <button
            type="button"
            className={styles.closeButton}
            onClick={onClose}
            aria-label="Close"
            disabled={isSubmitting}
          >
            x
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="agent-name">
              Name
            </label>
            <input
              id="agent-name"
              className={styles.input}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="planner"
              disabled={isSubmitting}
              autoFocus
            />
          </div>

          <div className={styles.row}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="agent-role">
                Role
              </label>
              <select
                id="agent-role"
                className={styles.select}
                value={roleName}
                onChange={(event) => setRoleName(event.target.value)}
                disabled={isSubmitting}
              >
                <option value="task">task</option>
                <option value="plan">plan</option>
              </select>
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="agent-backend">
                Backend
              </label>
              <input
                id="agent-backend"
                className={styles.input}
                value={backend}
                onChange={(event) => setBackend(event.target.value)}
                placeholder="claude"
                disabled={isSubmitting}
              />
            </div>
          </div>

          <label className={styles.checkboxRow}>
            <input
              type="checkbox"
              checked={crossRepo}
              onChange={(event) => setCrossRepo(event.target.checked)}
              disabled={isSubmitting}
            />
            <span>Workspace scope</span>
          </label>

          {!crossRepo && (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="agent-repo">
                Repo
              </label>
              <select
                id="agent-repo"
                className={styles.select}
                value={selectedRepo}
                onChange={(event) => setRepoName(event.target.value)}
                disabled={isSubmitting || repoOptions.length === 0}
              >
                {repoOptions.map((repo) => (
                  <option key={repo} value={repo}>
                    {repo}
                  </option>
                ))}
              </select>
            </div>
          )}

          {error && (
            <div className={styles.error} role="alert">
              {error}
            </div>
          )}

          <div className={styles.actions}>
            <button
              type="button"
              className={styles.cancelButton}
              onClick={onClose}
              disabled={isSubmitting}
            >
              Cancel
            </button>
            <button
              type="submit"
              className={styles.submitButton}
              disabled={isSubmitting}
            >
              {isSubmitting ? "Creating..." : "Create Agent"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
