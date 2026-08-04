/**
 * ReposSection displays workspace repos in the sidebar (Aether wireframe
 * REPOS block): folder icon + repo name,
 * with the "+ Add Repo" entry (opens the Add Repo
 * dialog) at the bottom of the section.
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
    <section className={styles.section} aria-labelledby="repos-heading">
      <h2 id="repos-heading" className={styles.heading}>
        Repos
      </h2>
      {repos.length > 0 ? (
        <div className={styles.list}>
          {repos.map((repo) => (
            <button
              key={repo.name}
              type="button"
              className={styles.row}
              aria-label={repo.name}
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
            </button>
          ))}
        </div>
      ) : null}
      {onAddRepo && (
        <button type="button" className={styles.addRepo} onClick={onAddRepo}>
          + Add Repo
        </button>
      )}
    </section>
  );
}
