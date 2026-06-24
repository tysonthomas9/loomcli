/**
 * WorkspaceSelectorBar displays the active workspace name at the top of the
 * sidebar. Clicking opens the WorkspaceSwitcher overlay for switching.
 */

import { useState, useCallback } from "react";

import type { WorkspaceSummary } from "@/api/workspace";
import { WorkspaceSwitcher } from "@/components/WorkspaceSwitcher";
import { shouldUseWhiteText } from "@/utils/colorUtils";
import { getWorkspaceColor } from "@/utils/workspace";

import styles from "./WorkspaceSelectorBar.module.css";

export interface WorkspaceSelectorBarProps {
  /** Full selector row or collapsed avatar dot (wireframe pin 24). */
  variant?: "full" | "collapsed" | undefined;
  /** Display name of the active workspace */
  workspaceName: string;
  /** All workspace summaries for the switcher */
  workspaces: WorkspaceSummary[];
  /** Active workspace UUID */
  activeWorkspaceId: string;
  /** Called with workspace name on selection */
  onWorkspaceSwitch: (workspaceName: string) => void;
  /** Called when "+ New Workspace" is clicked in the switcher */
  onAddWorkspace?: (() => void) | undefined;
}

function workspaceInitial(name: string): string {
  const trimmed = name.trim();
  return trimmed ? trimmed[0]!.toUpperCase() : "?";
}

export function WorkspaceSelectorBar({
  variant = "full",
  workspaceName,
  workspaces,
  activeWorkspaceId,
  onWorkspaceSwitch,
  onAddWorkspace,
}: WorkspaceSelectorBarProps): JSX.Element {
  const [isSwitcherOpen, setIsSwitcherOpen] = useState(false);

  const handleOpen = useCallback(() => {
    setIsSwitcherOpen(true);
  }, []);

  const handleClose = useCallback(() => {
    setIsSwitcherOpen(false);
  }, []);

  const handleSelect = useCallback(
    (wsId: string) => {
      const ws = workspaces.find((w) => w.id === wsId);
      if (ws) {
        onWorkspaceSwitch(ws.name);
      }
      setIsSwitcherOpen(false);
    },
    [workspaces, onWorkspaceSwitch],
  );

  const workspaceColor = getWorkspaceColor(workspaceName);
  const initial = workspaceInitial(workspaceName);

  return (
    <>
      {variant === "collapsed" ? (
        <button
          type="button"
          className={styles.collapsedWorkspaceDot}
          onClick={handleOpen}
          aria-haspopup="dialog"
          aria-expanded={isSwitcherOpen}
          title={workspaceName}
          aria-label={`Active workspace: ${workspaceName}. Click to switch.`}
        >
          <span
            className={styles.collapsedWorkspaceAvatar}
            style={{
              backgroundColor: workspaceColor,
              color: shouldUseWhiteText(workspaceColor) ? "#fff" : "#111",
            }}
          >
            {initial}
          </span>
        </button>
      ) : (
        <button
          type="button"
          className={styles.selector}
          onClick={handleOpen}
          aria-haspopup="dialog"
          aria-expanded={isSwitcherOpen}
          aria-label={`Active workspace: ${workspaceName}. Click to switch.`}
        >
          <span
            className={styles.dot}
            style={{ backgroundColor: workspaceColor }}
          />
          <span className={styles.name}>{workspaceName}</span>
        </button>
      )}

      <WorkspaceSwitcher
        isOpen={isSwitcherOpen}
        workspaces={workspaces}
        activeWorkspaceId={activeWorkspaceId}
        onSelect={handleSelect}
        onClose={handleClose}
        onAddWorkspace={onAddWorkspace}
      />
    </>
  );
}
