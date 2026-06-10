/**
 * ReposSection displays workspace repos in the sidebar (Aether wireframe REPOS block).
 */

import { useEffect, useRef, useState, type FormEvent } from "react";

import type { RepoInfo } from "@/api/workspace";

import styles from "./ReposSection.module.css";

export interface ReposSectionAddRepoConfig {
  repoPathInput: string;
  onRepoPathInputChange: (value: string) => void;
  onSubmit: (e: FormEvent<HTMLFormElement>) => void | Promise<void>;
  isAdding: boolean;
  error: string | null;
  canBrowseFolders: boolean;
  onBrowse: () => void;
  isBrowsing: boolean;
  browseDisabled: boolean;
  browseTitle?: string | undefined;
  /** Expand the add-repo panel on first render (empty workspaces). */
  defaultExpanded?: boolean | undefined;
}

export interface ReposSectionProps {
  repos: RepoInfo[];
  /** Open issue counts per repo name (non-done tasks). */
  openIssueCountByRepo?: Record<string, number> | undefined;
  addRepo?: ReposSectionAddRepoConfig | undefined;
}

export function ReposSection({
  repos,
  openIssueCountByRepo = {},
  addRepo,
}: ReposSectionProps): JSX.Element {
  const [addRepoOpen, setAddRepoOpen] = useState(
    () => addRepo?.defaultExpanded ?? false,
  );
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (addRepo?.defaultExpanded) {
      setAddRepoOpen(true);
    }
  }, [addRepo?.defaultExpanded]);

  useEffect(() => {
    if (addRepoOpen) {
      inputRef.current?.focus();
    }
  }, [addRepoOpen]);

  useEffect(() => {
    if (repos.length > 0) {
      setAddRepoOpen(false);
    }
  }, [repos.length]);

  const handleToggleAddRepo = () => {
    setAddRepoOpen((prev) => !prev);
    if (addRepoOpen && addRepo) {
      addRepo.onRepoPathInputChange("");
    }
  };

  const handleCancel = () => {
    setAddRepoOpen(false);
    if (addRepo) {
      addRepo.onRepoPathInputChange("");
    }
  };

  return (
    <section className={styles.section} aria-labelledby="repos-heading">
      <h2 id="repos-heading" className={styles.heading}>
        Repos
      </h2>
      {repos.length > 0 ? (
        <div className={styles.list}>
          {repos.map((repo) => {
            const branch =
              repo.current_branch || repo.default_branch || "main";
            const openCount = openIssueCountByRepo[repo.name] ?? 0;

            return (
              <button
                key={repo.name}
                type="button"
                className={styles.row}
                aria-label={`${repo.name}, branch ${branch}, ${openCount} open issues`}
              >
                <span className={styles.rowLeft}>
                  <svg
                    className={styles.icon}
                    width="15"
                    height="15"
                    viewBox="0 0 16 16"
                    fill="none"
                    aria-hidden="true"
                  >
                    <path
                      d="M1.5 2.5C1.5 1.95 1.95 1.5 2.5 1.5H6.29L8.29 3.5H13.5C14.05 3.5 14.5 3.95 14.5 4.5V12.5C14.5 13.05 14.05 13.5 13.5 13.5H2.5C1.95 13.5 1.5 13.05 1.5 12.5V2.5Z"
                      fill="currentColor"
                    />
                  </svg>
                  <span className={styles.name}>{repo.name}</span>
                </span>
                <span className={styles.rowRight}>
                  <span className={styles.countPill}>{openCount}</span>
                  <span className={styles.branchPill}>{branch}</span>
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
      {addRepo ? (
        <div className={styles.addRepoWrap}>
          <button
            type="button"
            className={styles.addRepoTrigger}
            onClick={handleToggleAddRepo}
            aria-expanded={addRepoOpen}
            aria-label="Add repository"
          >
            <span className={styles.addRepoIcon} aria-hidden="true">
              +
            </span>
            Add Repo
          </button>
          {addRepoOpen ? (
            <form
              className={styles.addRepoPanel}
              onSubmit={addRepo.onSubmit}
              aria-label="Add repository form"
            >
              <label className={styles.addRepoLabel} htmlFor="sidebar-repo-url">
                Repository URL
              </label>
              <input
                ref={inputRef}
                id="sidebar-repo-url"
                className={styles.addRepoInput}
                type="text"
                value={addRepo.repoPathInput}
                onChange={(e) => addRepo.onRepoPathInputChange(e.target.value)}
                placeholder="https://github.com/... or /path/to/repo"
                aria-label="Repository path or URL"
                disabled={addRepo.isAdding}
              />
              {addRepo.canBrowseFolders ? (
                <button
                  type="button"
                  className={styles.browseButton}
                  onClick={addRepo.onBrowse}
                  disabled={
                    addRepo.browseDisabled ||
                    addRepo.isAdding ||
                    addRepo.isBrowsing
                  }
                  title={addRepo.browseTitle}
                  aria-label="Browse for repository folder"
                >
                  Browse
                </button>
              ) : null}
              <div className={styles.addRepoActions}>
                <button
                  type="submit"
                  className={styles.submitButton}
                  disabled={
                    addRepo.isAdding || addRepo.repoPathInput.trim() === ""
                  }
                >
                  {addRepo.isAdding ? "Adding..." : "Add"}
                </button>
                <button
                  type="button"
                  className={styles.cancelButton}
                  onClick={handleCancel}
                  disabled={addRepo.isAdding}
                >
                  Cancel
                </button>
              </div>
              {addRepo.error ? (
                <div className={styles.addRepoError} role="alert">
                  {addRepo.error}
                </div>
              ) : null}
            </form>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
