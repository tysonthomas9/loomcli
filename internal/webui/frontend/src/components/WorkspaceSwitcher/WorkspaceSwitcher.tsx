/**
 * WorkspaceSwitcher — quick-switcher overlay for workspace selection.
 *
 * Rendered via createPortal to document.body. Supports:
 * - Substring search filtering on workspace name and path
 * - Arrow Up/Down keyboard navigation
 * - Enter to select, Escape to close (via escape layer)
 * - Positional shortcut hints (Cmd/Ctrl+Shift+1-9)
 * - Active workspace indicator (by ID)
 */

import { useState, useRef, useCallback, useEffect } from "react";
import { createPortal } from "react-dom";

import type { WorkspaceSummary } from "@/api/workspace";
import { SearchInput } from "@/components/search";
import {
  useRegisterEscapeLayer,
  useFocusTrap,
  useFocusReturn,
  LAYER_WORKSPACE_SWITCHER,
} from "@/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { WorkspaceContextMenu } from "@/components/WorkspaceTree/menus/WorkspaceContextMenu";

import { useWorkspaceManagement } from "./useWorkspaceManagement";
import styles from "./WorkspaceSwitcher.module.css";

export interface WorkspaceSwitcherProps {
  isOpen: boolean;
  workspaces: WorkspaceSummary[];
  /** Active workspace UUID for indicator */
  activeWorkspaceId: string;
  /** Called with workspace ID on selection */
  onSelect: (id: string) => void;
  onClose: () => void;
  /** Called when "+ New Workspace" is clicked. Omit to hide the button. */
  onAddWorkspace?: (() => void) | undefined;
}

const isMac =
  typeof navigator !== "undefined" && /Mac/.test(navigator.userAgent);
const modSymbol = isMac ? "\u2318" : "Ctrl+";
const shiftSymbol = isMac ? "\u21e7" : "Shift+";

export function WorkspaceSwitcher({
  isOpen,
  workspaces,
  activeWorkspaceId,
  onSelect,
  onClose,
  onAddWorkspace,
}: WorkspaceSwitcherProps) {
  const [search, setSearch] = useState("");
  const [highlightIndex, setHighlightIndex] = useState(0);
  const dialogRef = useRef<HTMLDivElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);

  // Rename / remove / context-menu state for the per-workspace overflow actions.
  const mgmt = useWorkspaceManagement();

  // Escape sub-layers stacked just above the base switcher layer. The registry
  // fires only the highest-priority active layer, so Escape resolves to the open
  // sub-state first (context menu, then an active rename), then closes the
  // switcher itself. Computed at render (not module scope) so importing this
  // module never touches LAYER_WORKSPACE_SWITCHER — some suites mock @/hooks
  // without it and never render the switcher.
  const contextMenuLayer = LAYER_WORKSPACE_SWITCHER + 2;
  const renameLayer = LAYER_WORKSPACE_SWITCHER + 1;
  useRegisterEscapeLayer(
    LAYER_WORKSPACE_SWITCHER,
    onClose,
    isOpen && mgmt.contextMenu === null && mgmt.editing === null,
  );
  useRegisterEscapeLayer(
    contextMenuLayer,
    mgmt.closeContextMenu,
    isOpen && mgmt.contextMenu !== null,
  );
  useRegisterEscapeLayer(
    renameLayer,
    mgmt.cancelRename,
    isOpen && mgmt.editing !== null,
  );

  // Focus management: trap focus inside dialog, restore on close
  useFocusTrap(dialogRef, isOpen);
  useFocusReturn(isOpen);

  // Filter workspaces by substring match on name and path
  const filtered = search
    ? workspaces.filter((ws) => {
        const term = search.toLowerCase();
        return (
          ws.name.toLowerCase().includes(term) ||
          ws.path.toLowerCase().includes(term)
        );
      })
    : workspaces;

  // Reset state when opening
  useEffect(() => {
    if (isOpen) {
      setSearch("");
      setHighlightIndex(0);
    }
  }, [isOpen]);

  // Clamp highlight index when filtered results change
  useEffect(() => {
    setHighlightIndex((prev) =>
      prev >= filtered.length ? Math.max(0, filtered.length - 1) : prev,
    );
  }, [filtered.length]);

  // Scroll highlighted item into view
  useEffect(() => {
    if (!resultsRef.current) return;
    const items = resultsRef.current.querySelectorAll(`[data-workspace-item]`);
    const item = items[highlightIndex];
    if (item) {
      item.scrollIntoView({ block: "nearest" });
    }
  }, [highlightIndex]);

  const handleSelect = useCallback(
    (id: string) => {
      onSelect(id);
      onClose();
    },
    [onSelect, onClose],
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      // While renaming, arrow/Enter belong to the rename input, not list nav.
      if (mgmt.editing !== null) return;
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setHighlightIndex((prev) =>
          prev < filtered.length - 1 ? prev + 1 : 0,
        );
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        setHighlightIndex((prev) =>
          prev > 0 ? prev - 1 : filtered.length - 1,
        );
      } else if (event.key === "Enter") {
        event.preventDefault();
        const ws = filtered[highlightIndex];
        if (ws) handleSelect(ws.id);
      }
    },
    [filtered, highlightIndex, handleSelect, mgmt.editing],
  );

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      onKeyDown={handleKeyDown}
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="Switch workspace"
      >
        <div className={styles.searchWrapper}>
          <SearchInput
            value={search}
            onChange={setSearch}
            placeholder="Switch workspace..."
            autoFocus
            size="md"
            aria-label="Search workspaces"
          />
        </div>
        <div className={styles.results} ref={resultsRef}>
          {filtered.length === 0 ? (
            <div className={styles.emptyState}>No workspaces found</div>
          ) : (
            filtered.map((ws, index) => {
              const isActive = ws.id === activeWorkspaceId;
              const originalIndex = workspaces.indexOf(ws);
              const isEditing = mgmt.editing?.id === ws.id;

              // A row being renamed swaps its switch button for an inline input
              // (an <input> can't live inside a <button>).
              if (isEditing) {
                return (
                  <div key={ws.id} className={styles.itemRow}>
                    <div className={styles.renameContainer}>
                      <input
                        ref={mgmt.renameInputRef}
                        type="text"
                        className={styles.renameInput}
                        value={mgmt.draftName}
                        onChange={(e) => mgmt.setDraftName(e.target.value)}
                        onBlur={mgmt.saveRename}
                        onKeyDown={mgmt.handleRenameKeyDown}
                        disabled={mgmt.isSaving}
                        aria-label="Rename workspace"
                        data-testid="workspace-rename-input"
                      />
                      {mgmt.renameError && (
                        <span
                          className={styles.renameError}
                          role="alert"
                          data-testid="workspace-rename-error"
                        >
                          {mgmt.renameError}
                        </span>
                      )}
                    </div>
                  </div>
                );
              }

              return (
                <div key={ws.id} className={styles.itemRow}>
                  <button
                    type="button"
                    data-workspace-item
                    className={[
                      styles.item,
                      index === highlightIndex ? styles.highlighted : "",
                      isActive ? styles.active : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                    onClick={() => handleSelect(ws.id)}
                    onMouseEnter={() => setHighlightIndex(index)}
                  >
                    {isActive && (
                      <span className={styles.activeIndicator}>&#10003;</span>
                    )}
                    <div className={styles.itemInfo}>
                      <div className={styles.itemName}>{ws.name}</div>
                      <div className={styles.itemPath}>{ws.path}</div>
                    </div>
                    <div className={styles.itemMeta}>
                      <span className={styles.repoCount}>
                        {ws.repo_count} repo{ws.repo_count !== 1 ? "s" : ""}
                      </span>
                      {originalIndex < 9 && (
                        <span className={styles.shortcutHint}>
                          {modSymbol}
                          {shiftSymbol}
                          {originalIndex + 1}
                        </span>
                      )}
                    </div>
                  </button>
                  {/* Overflow → rename/remove. Shown on every row; the active
                      workspace can be renamed but not removed (its Remove action
                      is hidden — see showRemove on the context menu below). */}
                  <button
                    type="button"
                    className={styles.overflowButton}
                    onClick={(e) => mgmt.openContextMenu(e, ws)}
                    aria-label={`More actions for ${ws.name}`}
                    data-testid={`workspace-overflow-${ws.name}`}
                  >
                    &#x2026;
                  </button>
                </div>
              );
            })
          )}
        </div>
        {onAddWorkspace && (
          <button
            type="button"
            className={styles.addButton}
            onClick={() => {
              onClose();
              onAddWorkspace();
            }}
          >
            + New Workspace
          </button>
        )}

        <WorkspaceContextMenu
          isOpen={mgmt.contextMenu !== null}
          position={mgmt.contextMenu ?? { x: 0, y: 0 }}
          onRename={mgmt.startRename}
          onRemove={mgmt.startRemove}
          onClose={mgmt.closeContextMenu}
          showRemove={mgmt.contextMenu?.workspaceId !== activeWorkspaceId}
        />

        <ConfirmDialog
          isOpen={mgmt.confirmDeleteOpen}
          title="Remove workspace"
          message={`Are you sure you want to remove "${mgmt.pendingDeleteName}"? Git worktrees will be kept on disk.`}
          confirmLabel="Remove"
          variant="danger"
          onConfirm={mgmt.confirmDelete}
          onCancel={mgmt.cancelDelete}
        />
      </div>
    </div>,
    document.body,
  );
}
