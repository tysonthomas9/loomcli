/**
 * RepoChecksPanel — renders the workspace's repos as a list of
 * preflight checks. Each repo gets a card with checks for: path
 * present, default branch detected, remote configured, and
 * linked-worktree status.
 *
 * The real heavy preflight (path-exists-on-disk, is-git-repo,
 * worktree-creation-rights) is server-side work that lives in a
 * follow-up; this panel uses the data the server already returns via
 * GET /api/workspaces/{ws} to surface what's known today. The
 * onboarding step's `verify-repo` status comes from the same data
 * shape, so the two stay in sync without adding new endpoints.
 */

import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import type { RepoInfo } from "@/api/workspace";

import styles from "./RepoChecksPanel.module.css";

interface Check {
  label: string;
  status: "pass" | "warn" | "fail";
  detail?: string;
}

function checksFor(repo: RepoInfo): Check[] {
  const checks: Check[] = [];
  checks.push({
    label: "Repo path recorded",
    status: repo.path ? "pass" : "fail",
    detail: repo.path,
  });
  checks.push({
    label: "Default branch",
    status: repo.default_branch ? "pass" : "warn",
    detail:
      repo.default_branch ||
      "no branch detected — push-oriented flows will be limited",
  });
  checks.push({
    label: "Git remote",
    status: repo.remote ? "pass" : "warn",
    detail: repo.remote || "no remote — first local run is still available",
  });
  if (repo.is_linked_worktree) {
    checks.push({
      label: "Linked worktree",
      status: "pass",
      detail: "managed by Loom",
    });
  }
  return checks;
}

export function RepoChecksPanel(): JSX.Element {
  const { repos } = useWorkspaceContext();

  return (
    <section
      className={styles.container}
      aria-label="Repo checks"
      data-testid="repo-checks-panel"
      id="repo-checks"
    >
      <p className={styles.subtitle}>
        Preflight checks for each repo attached to this workspace.
        Warnings don&rsquo;t block local runs but may limit push or
        review-oriented flows.
      </p>

      {repos.length === 0 ? (
        <p className={styles.empty}>
          No repos attached yet. Add a repo from the workspace tree, or
          use the onboarding checklist on the kanban view.
        </p>
      ) : (
        <div className={styles.list}>
          {repos.map((repo) => (
            <article
              key={repo.name}
              className={styles.repo}
              data-testid={`repo-checks-row-${repo.name}`}
            >
              <div className={styles.repoHeader}>
                <span className={styles.repoName}>{repo.name}</span>
                {repo.path ? (
                  <span className={styles.repoPath}>{repo.path}</span>
                ) : null}
              </div>
              <ul className={styles.checks}>
                {checksFor(repo).map((c) => (
                  <li key={c.label} className={styles.check}>
                    <span
                      className={`${styles.checkIcon} ${styles[c.status]}`}
                      aria-hidden="true"
                    >
                      {c.status === "pass"
                        ? "✓"
                        : c.status === "warn"
                          ? "!"
                          : "×"}
                    </span>
                    <span className={styles.checkLabel}>{c.label}</span>
                    {c.detail ? (
                      <span className={styles.checkDetail}> — {c.detail}</span>
                    ) : null}
                  </li>
                ))}
              </ul>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
