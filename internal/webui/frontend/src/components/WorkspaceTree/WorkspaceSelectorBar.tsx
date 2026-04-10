/**
 * WorkspaceSelectorBar displays the active workspace name at the top of the
 * sidebar. Clicking opens the WorkspaceSwitcher overlay for switching.
 */

import { useState, useCallback } from "react";

import type { WorkspaceSummary } from "@/api/workspace";
import { WorkspaceSwitcher } from "@/components/WorkspaceSwitcher";
import { getWorkspaceColor } from "@/utils/workspace";

import styles from "./WorkspaceSelectorBar.module.css";

export interface WorkspaceSelectorBarProps {
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

export function WorkspaceSelectorBar({
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

  return (
    <>
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
          style={{ backgroundColor: getWorkspaceColor(workspaceName) }}
        />
        <span className={styles.name}>{workspaceName}</span>
        <span className={styles.chevron}>&#x25BE;</span>
      </button>

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
