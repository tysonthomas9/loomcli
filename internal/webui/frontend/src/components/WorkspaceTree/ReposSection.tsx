/**
 * ReposSection displays workspace repos in the sidebar (Aether wireframe
 * REPOS block): folder icon + repo name,
 * with the "+ Add Repo" entry (opens the Add Repo
 * dialog) at the bottom of the section.
 */

import { useCallback, useState } from "react";

import type { RepoInfo } from "@/api/workspace";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { deleteWorkspaceRepo } from "@/hooks/api";
import { useToast } from "@/hooks";

import styles from "./ReposSection.module.css";

export interface ReposSectionProps {
  repos: RepoInfo[];
  workspaceId?: string;
  /** When provided, renders the "+ Add Repo" button at the section bottom. */
  onAddRepo?: () => void;
  /** Called after repo mutations to refresh workspace data. */
  onRepoRemoved?: () => void;
}

export function ReposSection({
  repos,
  workspaceId,
  onAddRepo,
  onRepoRemoved,
}: ReposSectionProps): JSX.Element | null {
  const { showToast } = useToast();
  const [pendingRemoveRepo, setPendingRemoveRepo] = useState<string | null>(
    null,
  );
  const [isRemoving, setIsRemoving] = useState(false);

  const canRemoveRepos = Boolean(workspaceId);

  const handleConfirmRemove = useCallback(async () => {
    if (!workspaceId || !pendingRemoveRepo || isRemoving) return;
    const repoName = pendingRemoveRepo;
    setIsRemoving(true);
    try {
      await deleteWorkspaceRepo(workspaceId, repoName);
      setPendingRemoveRepo(null);
      showToast(`Repo "${repoName}" removed`, { type: "success" });
      onRepoRemoved?.();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to remove repo";
      showToast(message, { type: "error" });
    } finally {
      setIsRemoving(false);
    }
  }, [isRemoving, onRepoRemoved, pendingRemoveRepo, showToast, workspaceId]);

  if (repos.length === 0 && !onAddRepo) return null;

  return (
    <>
      <section className={styles.section} aria-labelledby="repos-heading">
        <h2 id="repos-heading" className={styles.heading}>
          Repos
        </h2>
        {repos.length > 0 ? (
          <div className={styles.list}>
            {repos.map((repo) => (
              <div key={repo.name} className={styles.row}>
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
                {canRemoveRepos && (
                  <button
                    type="button"
                    className={styles.repoMenuButton}
                    aria-label={`More actions for repo ${repo.name}`}
                    title={`More actions for ${repo.name}`}
                    onClick={() => setPendingRemoveRepo(repo.name)}
                  >
                    &#x2026;
                  </button>
                )}
              </div>
            ))}
          </div>
        ) : (
          <p className={styles.emptyState}>No repos in workspace</p>
        )}
        {onAddRepo && (
          <button type="button" className={styles.addRepo} onClick={onAddRepo}>
            + Add Repo
          </button>
        )}
      </section>

      <ConfirmDialog
        isOpen={pendingRemoveRepo !== null}
        title="Remove repo"
        message={`Remove repo "${pendingRemoveRepo ?? ""}" from this workspace? This only removes the workspace association and will not delete anything on disk.`}
        confirmLabel={isRemoving ? "Removing..." : "Remove"}
        variant="danger"
        onConfirm={handleConfirmRemove}
        onCancel={() => {
          if (!isRemoving) setPendingRemoveRepo(null);
        }}
      />
    </>
  );
}
