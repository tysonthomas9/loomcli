/**
 * ReposSection displays a static inventory of repos in the workspace.
 * Shows repo name and default branch for each repo, with the "+ Add Repo"
 * entry at the bottom of the section (Aether V3 sidebar placement).
 */

import type { RepoInfo } from "@/api/workspace";

import styles from "./ReposSection.module.css";

export interface ReposSectionProps {
  repos: RepoInfo[];
  /** When provided, renders the "+ Add Repo" button at the section bottom. */
  onAddRepo?: () => void;
}

export function ReposSection({
  repos,
  onAddRepo,
}: ReposSectionProps): JSX.Element | null {
  if (repos.length === 0 && !onAddRepo) return null;

  return (
    <div className={styles.section}>
      <div className={styles.header}>Repos</div>
      <div className={styles.list}>
        {repos.map((repo) => (
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
            <span className={styles.branch}>
              {repo.current_branch || repo.default_branch || "main"}
            </span>
          </div>
        ))}
      </div>
      {onAddRepo && (
        <button type="button" className={styles.addRepo} onClick={onAddRepo}>
          + Add Repo
        </button>
      )}
    </div>
  );
}
