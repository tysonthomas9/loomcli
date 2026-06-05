import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";
import { useCreateWorkspaceAgent } from "@/hooks/agents";
import { useBackends } from "@/hooks/workspace";
import { ApiError } from "@/types/common";

import styles from "./CreateAgentModal.module.css";

/**
 * Role options as a segmented control (Aether Wireframe V3 Add-Agent dialog).
 * Loom's backend validates roles — only task/plan are defined, so "lead" is
 * intentionally absent (creating a lead agent 404s "role not found").
 */
const ROLE_OPTIONS: { value: string; label: string }[] = [
  { value: "task", label: "Task" },
  { value: "plan", label: "Plan" },
];

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
  const { backends } = useBackends();

  const repoOptions = useMemo(() => repos.map((repo) => repo.name), [repos]);
  const selectedRepo = repoName || repoOptions[0] || "";

  // Real backend list (Aether V3 made this a dropdown, not free text). Keep the
  // resolved/current value selectable even if it isn't in the live list.
  const backendOptions = useMemo(() => {
    const opts = backends.map((b) => ({ value: b.name, label: b.displayName }));
    if (backend && !opts.some((o) => o.value === backend)) {
      opts.unshift({ value: backend, label: backend });
    }
    return opts.length > 0 ? opts : [{ value: backend, label: backend }];
  }, [backends, backend]);

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
              <span className={styles.label} id="agent-role-label">
                Role
              </span>
              <div
                className={styles.segControl}
                role="group"
                aria-labelledby="agent-role-label"
              >
                {ROLE_OPTIONS.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    className={styles.segOption}
                    data-active={roleName === option.value || undefined}
                    aria-pressed={roleName === option.value}
                    onClick={() => setRoleName(option.value)}
                    disabled={isSubmitting}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="agent-backend">
                AI Backend
              </label>
              <select
                id="agent-backend"
                className={styles.select}
                value={backend}
                onChange={(event) => setBackend(event.target.value)}
                disabled={isSubmitting}
              >
                {backendOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
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
              disabled={isSubmitting || name.trim() === ""}
            >
              {isSubmitting ? "Creating..." : "Create Agent"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
