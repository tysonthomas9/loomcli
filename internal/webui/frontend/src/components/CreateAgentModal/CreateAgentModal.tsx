import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";
import { useCreateWorkspaceAgent } from "@/hooks/agents";
import { useBackends } from "@/hooks/workspace";
import { ApiError } from "@/types/common";

import styles from "./CreateAgentModal.module.css";

const ROLE_OPTIONS: { value: string; label: string }[] = [
  { value: "task", label: "Task" },
  { value: "plan", label: "Plan" },
  { value: "lead", label: "Lead" },
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
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wasOpenRef = useRef(false);
  const nameRef = useRef<HTMLInputElement>(null);
  const createAgent = useCreateWorkspaceAgent(workspaceId);
  const { backends } = useBackends();

  const repoOptions = useMemo(
    () =>
      repos
        .filter((repo) => !repo.is_linked_worktree)
        .map((repo) => repo.name),
    [repos],
  );
  const defaultRepos = useMemo(
    () => (repoOptions[0] ? [repoOptions[0]] : []),
    [repoOptions],
  );

  const crossRepo = selectedRepos.length === 0;
  const toggleRepo = (repo: string): void =>
    setSelectedRepos((prev) =>
      prev.includes(repo) ? prev.filter((r) => r !== repo) : [...prev, repo],
    );

  const backendOptions = useMemo(() => {
    const opts = backends.map((b) => ({ value: b.name, label: b.displayName }));
    if (backend && !opts.some((o) => o.value === backend)) {
      opts.unshift({ value: backend, label: backend });
    }
    return opts.length > 0 ? opts : [{ value: backend, label: backend }];
  }, [backend, backends]);

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
    setSelectedRepos(defaultRepos);
    setIsSubmitting(false);
    setError(null);
  }, [
    isOpen,
    resolvedDefaultName,
    resolvedDefaultRoleName,
    resolvedDefaultBackend,
    defaultRepos,
  ]);

  useEffect(() => {
    if (isOpen) {
      nameRef.current?.focus();
    }
  }, [isOpen]);

  const canSubmit = name.trim() !== "" && !isSubmitting;

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

    setIsSubmitting(true);
    try {
      const request = {
        name: trimmedName,
        role_name: trimmedRole,
        auto: false,
        cross_repo: crossRepo,
        repos: crossRepo ? [] : selectedRepos,
      };
      const agent = await createAgent({
        ...request,
        ...(trimmedBackend ? { backend: trimmedBackend } : {}),
      });
      onSuccess(agent);
      setName(resolvedDefaultName);
      setRoleName(resolvedDefaultRoleName);
      setBackend(resolvedDefaultBackend);
      setSelectedRepos(defaultRepos);
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

  const repoHint = crossRepo
    ? "No repo selected — the agent gets workspace-wide scope."
    : "Pick every repo this agent works in. Leave all unselected for workspace scope.";

  return (
    <AetherModal
      isOpen={isOpen}
      title="New Agent"
      ariaLabel="New Agent"
      onClose={onClose}
      overlayTestId="create-agent-overlay"
      closeTestId="create-agent-close"
      footer={
        <>
          <button
            type="button"
            className={aetherModalStyles.linkButton}
            onClick={onClose}
            disabled={isSubmitting}
          >
            Cancel
          </button>
          <button
            type="submit"
            form="create-agent-form"
            className={`${aetherModalStyles.primaryButton}${isSubmitting ? ` ${aetherModalStyles.submitting}` : ""}`}
            disabled={!canSubmit}
            data-testid="create-agent-submit"
          >
            {isSubmitting ? "Creating..." : "Create Agent"}
          </button>
        </>
      }
    >
      <form id="create-agent-form" onSubmit={handleSubmit}>
        <div className={styles.fieldGroup}>
          <label className={styles.label} htmlFor="agent-name">
            Name
          </label>
          <input
            id="agent-name"
            ref={nameRef}
            className={styles.input}
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="planner"
            disabled={isSubmitting}
            data-testid="create-agent-name"
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
              data-testid="create-agent-backend"
            >
              {backendOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className={styles.fieldGroup}>
          <span className={styles.label} id="agent-repos-label">
            Repos / Worktrees
          </span>
          {repoOptions.length === 0 ? (
            <p className={styles.emptyHint} data-testid="create-agent-no-repos">
              No repos yet — add one from the sidebar first. This agent will
              run with workspace scope.
            </p>
          ) : (
            <div
              className={styles.repoChips}
              role="group"
              aria-labelledby="agent-repos-label"
              data-testid="create-agent-repo-chips"
            >
              {repoOptions.map((repo) => {
                const on = selectedRepos.includes(repo);
                return (
                  <button
                    key={repo}
                    type="button"
                    className={styles.repoChip}
                    data-active={on || undefined}
                    aria-pressed={on}
                    onClick={() => toggleRepo(repo)}
                    disabled={isSubmitting}
                  >
                    <span className={styles.repoChipBox} aria-hidden="true">
                      {on ? "✓" : ""}
                    </span>
                    {repo}
                  </button>
                );
              })}
            </div>
          )}
          <p className={styles.hint}>{repoHint}</p>
        </div>

        {error && (
          <div className={styles.error} role="alert" data-testid="create-agent-error">
            {error}
          </div>
        )}
      </form>
    </AetherModal>
  );
}
