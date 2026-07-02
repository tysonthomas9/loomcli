import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import {
  createTerminalWorktree,
  type CreateTerminalWorktreeRequest,
  type TerminalWorktreeErrorBody,
  type TerminalWorktreeResult,
  type RepoInfo,
} from "@/hooks/api";
import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import {
  publishTerminalWorktreesChanged,
  requestTerminalGroupSelect,
  type TerminalWorktreeGroup,
} from "@/utils/terminalSidebarBridge";

import styles from "./CreateTerminalWorktreeModal.module.css";

export interface CreateTerminalWorktreeModalProps {
  isOpen: boolean;
  workspaceId: string;
  repos: RepoInfo[];
  onClose: () => void;
}

interface ParsedWorktreeError {
  message: string;
  kind?: string;
  results: TerminalWorktreeResult[];
}

function isTerminalWorktreeErrorBody(
  body: unknown,
): body is TerminalWorktreeErrorBody {
  return (
    typeof body === "object" &&
    body !== null &&
    "error" in body &&
    typeof (body as { error?: unknown }).error === "string"
  );
}

function parseWorktreeError(err: unknown): ParsedWorktreeError {
  const body = (err as { body?: unknown } | null)?.body;
  if (isTerminalWorktreeErrorBody(body)) {
    return {
      message: body.error,
      kind: body.kind,
      results: Array.isArray(body.results) ? body.results : [],
    };
  }
  return {
    message:
      err instanceof Error ? err.message : "Failed to create worktree group",
    results: [],
  };
}

function resultLabel(status: TerminalWorktreeResult["status"]): string {
  return status.replace("_", " ");
}

function toBridgeGroup(
  group: Awaited<ReturnType<typeof createTerminalWorktree>>["group"],
): TerminalWorktreeGroup {
  return {
    id: group.id,
    label: group.name,
    members: group.members.map((member) => ({
      repoName: member.repo_name,
      ...(member.base_branch ? { baseBranch: member.base_branch } : {}),
      baseDetached: member.base_detached,
      reusedBranch: member.reused_branch,
    })),
  };
}

function branchLabel(repo: RepoInfo): string {
  return repo.current_branch || repo.default_branch || "HEAD";
}

export function CreateTerminalWorktreeModal({
  isOpen,
  workspaceId,
  repos,
  onClose,
}: CreateTerminalWorktreeModalProps): JSX.Element | null {
  const localRepos = useMemo(
    () => repos.filter((repo) => !repo.is_linked_worktree),
    [repos],
  );
  const [name, setName] = useState("");
  const [selectedRepos, setSelectedRepos] = useState<string[]>([]);
  const [baseBranch, setBaseBranch] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<ParsedWorktreeError | null>(null);
  const [results, setResults] = useState<TerminalWorktreeResult[]>([]);
  const nameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!isOpen) return;
    setName("");
    setSelectedRepos(localRepos.map((repo) => repo.name));
    setBaseBranch("");
    setIsSubmitting(false);
    setError(null);
    setResults([]);
    const id = window.setTimeout(() => nameRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [isOpen, localRepos]);

  const toggleRepo = (repoName: string): void => {
    setSelectedRepos((current) =>
      current.includes(repoName)
        ? current.filter((name) => name !== repoName)
        : [...current, repoName],
    );
  };

  const valid =
    workspaceId.length > 0 &&
    name.trim().length > 0 &&
    selectedRepos.length > 0 &&
    !isSubmitting;

  const closeIfIdle = (): void => {
    if (!isSubmitting) onClose();
  };

  const handleSubmit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (!valid) return;
    setIsSubmitting(true);
    setError(null);
    setResults([]);

    const request: CreateTerminalWorktreeRequest = {
      name: name.trim(),
      repos: selectedRepos,
      ...(baseBranch.trim() ? { base: baseBranch.trim() } : {}),
    };

    try {
      const response = await createTerminalWorktree(workspaceId, request);
      const bridgeGroup = toBridgeGroup(response.group);
      publishTerminalWorktreesChanged(bridgeGroup);
      requestTerminalGroupSelect(bridgeGroup.id);
      onClose();
    } catch (err) {
      const parsed = parseWorktreeError(err);
      setError(parsed);
      setResults(parsed.results);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <AetherModal
      isOpen={isOpen}
      title="New Worktree"
      onClose={closeIfIdle}
      overlayTestId="terminal-worktree-overlay"
      dialogClassName={aetherModalStyles.dialogWide}
      footer={
        <>
          <button
            type="button"
            className={aetherModalStyles.linkButton}
            onClick={closeIfIdle}
            disabled={isSubmitting}
          >
            Cancel
          </button>
          <button
            type="submit"
            form="terminal-worktree-form"
            className={aetherModalStyles.primaryButton}
            disabled={!valid}
          >
            {isSubmitting ? "Creating..." : "Create Worktree"}
          </button>
        </>
      }
    >
      <form
        id="terminal-worktree-form"
        className={styles.form}
        onSubmit={handleSubmit}
      >
        <div className={styles.fieldGroup}>
          <label className={styles.label} htmlFor="terminal-worktree-name">
            Worktree name
          </label>
          <input
            id="terminal-worktree-name"
            ref={nameRef}
            className={`${styles.input} ${styles.mono}`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="feature-auth"
            disabled={isSubmitting}
            required
          />
          <p className={styles.helper}>
            Names become branch names; / is not supported in v1.
          </p>
        </div>

        <fieldset className={styles.repoFieldset}>
          <legend className={styles.label}>Repos</legend>
          {localRepos.length === 0 ? (
            <p className={styles.emptyHint}>No local repos available.</p>
          ) : (
            <div className={styles.repoList}>
              {localRepos.map((repo) => (
                <label key={repo.name} className={styles.repoOption}>
                  <input
                    type="checkbox"
                    checked={selectedRepos.includes(repo.name)}
                    onChange={() => toggleRepo(repo.name)}
                    disabled={isSubmitting}
                  />
                  <span className={styles.repoText}>
                    <span className={styles.repoName}>{repo.name}</span>
                    <span className={styles.repoBranch}>
                      {branchLabel(repo)}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          )}
        </fieldset>

        <div className={styles.fieldGroup}>
          <label className={styles.label} htmlFor="terminal-worktree-base">
            Base branch
          </label>
          <input
            id="terminal-worktree-base"
            className={`${styles.input} ${styles.mono}`}
            value={baseBranch}
            onChange={(event) => setBaseBranch(event.target.value)}
            placeholder="main"
            disabled={isSubmitting}
          />
          <p className={styles.helper}>
            Empty forks each repo from local HEAD; set a branch to prefer
            origin/base with local fallback.
          </p>
        </div>

        {error ? (
          <div className={styles.error} role="alert">
            <strong>{error.message}</strong>
            {error.kind ? <span>Kind: {error.kind}</span> : null}
          </div>
        ) : null}

        {results.length > 0 ? (
          <div className={styles.results} data-testid="worktree-results">
            {results.map((result) => (
              <div
                key={`${result.repo}-${result.status}`}
                className={styles.resultRow}
              >
                <span className={styles.resultRepo}>{result.repo}</span>
                <span
                  className={styles.resultStatus}
                  data-status={result.status}
                >
                  {resultLabel(result.status)}
                </span>
                {result.message ? (
                  <span className={styles.resultMessage}>{result.message}</span>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </form>
    </AetherModal>
  );
}
