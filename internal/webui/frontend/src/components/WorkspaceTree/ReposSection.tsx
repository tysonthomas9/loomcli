/**
 * ReposSection displays a static inventory of repos in the workspace.
 * Shows repo name and default branch for each repo. When workspaceId is
 * provided and onDefaultBranchChange is wired, the branch badge becomes
 * click-to-edit for the repo's default_branch.
 */

import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import type { RepoInfo } from "@/api/workspace";

import styles from "./ReposSection.module.css";

export interface ReposSectionProps {
  repos: RepoInfo[];
  /** Workspace UUID. When empty, branch editing is disabled. */
  workspaceId?: string;
  /**
   * Persist a new default_branch for a repo. Called when the user commits
   * an edit via Enter or blur and the value actually changed.
   */
  onDefaultBranchChange?: (repoName: string, newBranch: string) => void;
}

export function ReposSection({
  repos,
  workspaceId,
  onDefaultBranchChange,
}: ReposSectionProps): JSX.Element | null {
  const [editingRepo, setEditingRepo] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);

  const editable = Boolean(workspaceId && onDefaultBranchChange);

  useEffect(() => {
    if (editingRepo && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [editingRepo]);

  if (repos.length === 0) return null;

  const startEdit = (repo: RepoInfo) => {
    if (!editable) return;
    setEditingRepo(repo.name);
    setDraft(repo.default_branch || "main");
  };

  const cancelEdit = () => {
    setEditingRepo(null);
    setDraft("");
  };

  const commitEdit = (repo: RepoInfo) => {
    const value = draft.trim();
    setEditingRepo(null);
    setDraft("");
    if (!editable || !onDefaultBranchChange) return;
    if (value === "" || value === (repo.default_branch || "main")) return;
    onDefaultBranchChange(repo.name, value);
  };

  const handleKeyDown = (
    e: KeyboardEvent<HTMLInputElement>,
    repo: RepoInfo,
  ) => {
    if (e.key === "Enter") {
      e.preventDefault();
      commitEdit(repo);
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancelEdit();
    }
  };

  return (
    <div className={styles.section}>
      <div className={styles.header}>Repos</div>
      <div className={styles.list}>
        {repos.map((repo) => {
          const isEditing = editingRepo === repo.name;
          const displayBranch = repo.default_branch || "main";
          return (
            <div key={repo.name} className={styles.row}>
              <svg
                className={styles.icon}
                width="14"
                height="14"
                viewBox="0 0 16 16"
                fill="none"
              >
                <path
                  d="M1.5 2.5C1.5 1.95 1.95 1.5 2.5 1.5H6.29L8.29 3.5H13.5C14.05 3.5 14.5 3.95 14.5 4.5V12.5C14.5 13.05 14.05 13.5 13.5 13.5H2.5C1.95 13.5 1.5 13.05 1.5 12.5V2.5Z"
                  fill="currentColor"
                />
              </svg>
              <span className={styles.name}>{repo.name}</span>
              {isEditing ? (
                <input
                  ref={inputRef}
                  type="text"
                  className={styles.branchInput}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => handleKeyDown(e, repo)}
                  onBlur={() => commitEdit(repo)}
                  aria-label={`Default branch for ${repo.name}`}
                />
              ) : editable ? (
                <button
                  type="button"
                  className={`${styles.branch} ${styles.branchEditable}`}
                  onClick={() => startEdit(repo)}
                  title="Click to edit default branch"
                >
                  {displayBranch}
                </button>
              ) : (
                <span className={styles.branch}>
                  {repo.current_branch || displayBranch}
                </span>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
