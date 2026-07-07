/**
 * CollapsedRepoRail — compact repo pills when WorkspaceTree is collapsed.
 */

import type { RepoInfo } from "@/api/workspace";
import { CompactRailHost } from "@/components/CompactRail";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./CollapsedRepoRail.module.css";

export interface CollapsedRepoRailProps {
  repos: RepoInfo[];
  onAddRepo?: (() => void) | undefined;
}

export function CollapsedRepoRail({
  repos,
  onAddRepo,
}: CollapsedRepoRailProps): JSX.Element | null {
  if (repos.length === 0 && !onAddRepo) return null;

  return (
    <nav
      className={styles.rail}
      aria-label="Repos"
      data-testid="collapsed-repo-rail"
    >
      {repos.length === 0 ? (
        <CompactRailHost label="No repos" className={styles.emptyHint}>
          —
        </CompactRailHost>
      ) : (
        repos.map((repo) => {
          const bg = getAvatarColor(repo.name);
          const fg = shouldUseWhiteText(bg) ? "#fff" : "#1f2937";
          return (
            <CompactRailHost
              key={repo.name}
              label={repo.name}
              className={styles.repoPill}
              style={{ backgroundColor: bg, color: fg }}
            >
              {getCompactAvatarInitials(repo.name)}
            </CompactRailHost>
          );
        })
      )}
      {onAddRepo ? (
        <CompactRailHost
          as="button"
          type="button"
          label="Add repo"
          className={styles.addButton}
          onClick={onAddRepo}
        >
          +
        </CompactRailHost>
      ) : null}
    </nav>
  );
}
