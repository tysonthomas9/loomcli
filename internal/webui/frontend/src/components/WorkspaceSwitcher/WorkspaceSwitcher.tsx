/**
 * WorkspaceSwitcher — quick-switcher overlay for workspace selection.
 *
 * Rendered via createPortal to document.body. Supports:
 * - Substring search filtering on workspace name and path
 * - Arrow Up/Down keyboard navigation
 * - Enter to select, Escape to close (via escape layer)
 * - Positional shortcut hints (Cmd/Ctrl+Shift+1-9)
 * - Active workspace indicator (by ID)
 * - Optional management actions (rename, delete, set/clear default) via
 *   per-item overflow button + right-click context menu
 */

import { useState, useRef, useCallback, useEffect } from "react";
import { createPortal } from "react-dom";

import type { WorkspaceSummary } from "@/api/workspace";
import { SearchInput } from "@/components/search";
// Direct path import bypasses the WorkspaceTree barrel — the barrel
// re-exports WorkspaceSelectorBar, which itself imports WorkspaceSwitcher,
// so going through the barrel would form a runtime cycle. The component
// boundaries linter allowlists this exception (see scripts/check-component-boundaries.mjs).
import { WorkspaceContextMenu } from "@/components/WorkspaceTree/menus/WorkspaceContextMenu";
import {
  useRegisterEscapeLayer,
  useFocusTrap,
  useFocusReturn,
  LAYER_WORKSPACE_SWITCHER,
} from "@/hooks";

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
  /**
   * Rename callback. When provided (along with onDelete), each workspace item
   * shows a three-dot overflow button and supports right-click for a context
   * menu with Rename/Remove/Set as default actions.
   */
  onRename?: ((wsId: string, newName: string) => Promise<void>) | undefined;
  /** Delete callback. Receives (wsId, wsName) and is expected to open a
   * confirmation dialog in the parent. */
  onDelete?: ((wsId: string, wsName: string) => void) | undefined;
  /** Set-default callback. Optional, gates rendering of the menu item. */
  onSetDefault?: ((wsName: string) => void) | undefined;
  /** Clear-default callback. Optional, gates rendering of the menu item. */
  onClearDefault?: (() => void) | undefined;
  /** UUID of the default workspace, used to flip the menu's star toggle. */
  defaultWorkspaceId?: string | undefined;
}

const isMac =
  typeof navigator !== "undefined" && /Mac/.test(navigator.userAgent);
const modSymbol = isMac ? "⌘" : "Ctrl+";
const shiftSymbol = isMac ? "⇧" : "Shift+";

export function WorkspaceSwitcher({
  isOpen,
  workspaces,
  activeWorkspaceId,
  onSelect,
  onClose,
  onAddWorkspace,
  onRename,
  onDelete,
  onSetDefault,
  onClearDefault,
  defaultWorkspaceId,
}: WorkspaceSwitcherProps) {
  const [search, setSearch] = useState("");
  const [highlightIndex, setHighlightIndex] = useState(0);
  const [contextMenu, setContextMenu] = useState<{
    wsId: string;
    wsName: string;
    x: number;
    y: number;
  } | null>(null);
  const [editingWsId, setEditingWsId] = useState<string | null>(null);
  const [draftName, setDraftName] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  const dialogRef = useRef<HTMLDivElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const renameInputRef = useRef<HTMLInputElement>(null);
  // Synchronous in-flight guard for rename — React state updates batch
  // across the await, so onBlur cannot rely on isSaving alone.
  const isSavingRef = useRef(false);

  const hasManagementActions = !!(onRename || onDelete);

  // Suspend the overlay-level escape layer while a rename is active so the
  // input's own Escape handler can cancel without also closing the switcher.
  useRegisterEscapeLayer(
    LAYER_WORKSPACE_SWITCHER,
    onClose,
    isOpen && editingWsId === null,
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

  // Reset state when opening/closing
  useEffect(() => {
    if (isOpen) {
      setSearch("");
      setHighlightIndex(0);
    } else {
      setContextMenu(null);
      setEditingWsId(null);
      setDraftName("");
      setRenameError(null);
      setIsSaving(false);
      isSavingRef.current = false;
    }
  }, [isOpen]);

  // Auto-focus + select rename input when entering edit mode
  useEffect(() => {
    if (editingWsId && renameInputRef.current) {
      renameInputRef.current.focus();
      renameInputRef.current.select();
    }
  }, [editingWsId]);

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
      // While renaming, defer to the input's own keydown handler
      if (editingWsId !== null) return;
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
    [filtered, highlightIndex, handleSelect, editingWsId],
  );

  const openContextMenu = useCallback(
    (ws: WorkspaceSummary, x: number, y: number) => {
      // Ignore menu open requests while another rename is active to avoid
      // racing inputs across two items.
      if (editingWsId !== null) return;
      setContextMenu({ wsId: ws.id, wsName: ws.name, x, y });
    },
    [editingWsId],
  );

  const handleOverflowClick = useCallback(
    (e: React.MouseEvent, ws: WorkspaceSummary) => {
      e.stopPropagation();
      e.preventDefault();
      openContextMenu(ws, e.clientX, e.clientY);
    },
    [openContextMenu],
  );

  const handleItemContextMenu = useCallback(
    (e: React.MouseEvent, ws: WorkspaceSummary) => {
      if (!hasManagementActions) return;
      e.preventDefault();
      e.stopPropagation();
      openContextMenu(ws, e.clientX, e.clientY);
    },
    [hasManagementActions, openContextMenu],
  );

  const handleCloseContextMenu = useCallback(() => {
    setContextMenu(null);
  }, []);

  const handleStartRename = useCallback(() => {
    if (!contextMenu) return;
    setEditingWsId(contextMenu.wsId);
    setDraftName(contextMenu.wsName);
    setRenameError(null);
  }, [contextMenu]);

  const handleCancelRename = useCallback(() => {
    setEditingWsId(null);
    setDraftName("");
    setRenameError(null);
  }, []);

  const handleSaveRename = useCallback(async () => {
    if (!editingWsId || !onRename) return;
    if (isSavingRef.current) return;
    const trimmed = draftName.trim();
    const original = workspaces.find((w) => w.id === editingWsId)?.name ?? "";
    if (!trimmed) {
      setRenameError("Name cannot be empty");
      setTimeout(() => renameInputRef.current?.focus(), 0);
      return;
    }
    if (trimmed === original) {
      handleCancelRename();
      return;
    }
    isSavingRef.current = true;
    setIsSaving(true);
    setRenameError(null);
    try {
      await onRename(editingWsId, trimmed);
      handleCancelRename();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to rename workspace";
      setRenameError(message);
      setTimeout(() => renameInputRef.current?.focus(), 0);
    } finally {
      isSavingRef.current = false;
      setIsSaving(false);
    }
  }, [editingWsId, onRename, draftName, workspaces, handleCancelRename]);

  const handleRenameKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveRename();
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        handleCancelRename();
      }
    },
    [handleSaveRename, handleCancelRename],
  );

  const handleStartRemove = useCallback(() => {
    if (!contextMenu || !onDelete) return;
    const { wsId, wsName } = contextMenu;
    setContextMenu(null);
    onClose();
    onDelete(wsId, wsName);
  }, [contextMenu, onDelete, onClose]);

  const handleMenuSetDefault = useCallback(() => {
    if (!contextMenu || !onSetDefault) return;
    onSetDefault(contextMenu.wsName);
  }, [contextMenu, onSetDefault]);

  const handleMenuClearDefault = useCallback(() => {
    if (!onClearDefault) return;
    onClearDefault();
  }, [onClearDefault]);

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
              const isEditing = editingWsId === ws.id;
              const itemClass = [
                styles.item,
                index === highlightIndex ? styles.highlighted : "",
                isActive ? styles.active : "",
                isEditing ? styles.itemEditing : "",
              ]
                .filter(Boolean)
                .join(" ");

              const meta = (
                <div className={styles.itemMeta}>
                  <span className={styles.repoCount}>
                    {ws.repo_count} repo{ws.repo_count !== 1 ? "s" : ""}
                  </span>
                  {originalIndex < 9 && !isEditing && (
                    <span className={styles.shortcutHint}>
                      {modSymbol}
                      {shiftSymbol}
                      {originalIndex + 1}
                    </span>
                  )}
                  {hasManagementActions && !isEditing && (
                    <button
                      type="button"
                      className={styles.overflowButton}
                      onClick={(e) => handleOverflowClick(e, ws)}
                      aria-label={`More actions for ${ws.name}`}
                      data-testid={`workspace-switcher-overflow-${ws.id}`}
                    >
                      &#x2026;
                    </button>
                  )}
                </div>
              );

              if (isEditing) {
                return (
                  <div
                    key={ws.id}
                    data-workspace-item
                    className={itemClass}
                    onContextMenu={(e) => handleItemContextMenu(e, ws)}
                  >
                    {isActive && (
                      <span className={styles.activeIndicator}>&#10003;</span>
                    )}
                    <div className={styles.itemInfo}>
                      <input
                        ref={renameInputRef}
                        type="text"
                        className={styles.renameInput}
                        value={draftName}
                        onChange={(e) => setDraftName(e.target.value)}
                        onKeyDown={handleRenameKeyDown}
                        onBlur={() => {
                          if (!isSavingRef.current) handleSaveRename();
                        }}
                        disabled={isSaving}
                        aria-label="Rename workspace"
                        data-testid="workspace-switcher-rename-input"
                      />
                      {renameError ? (
                        <div
                          className={styles.renameError}
                          role="alert"
                          data-testid="workspace-switcher-rename-error"
                        >
                          {renameError}
                        </div>
                      ) : (
                        <div className={styles.itemPath}>{ws.path}</div>
                      )}
                    </div>
                    {meta}
                  </div>
                );
              }

              const handleItemActivate = () => {
                if (contextMenu?.wsId === ws.id) return;
                handleSelect(ws.id);
              };

              if (hasManagementActions) {
                // Use a div+role=button to legally host the nested overflow
                // <button>. Click and Enter/Space activate the row.
                return (
                  <div
                    key={ws.id}
                    data-workspace-item
                    className={itemClass}
                    role="button"
                    tabIndex={0}
                    aria-label={`Switch to workspace ${ws.name}`}
                    onClick={handleItemActivate}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        handleItemActivate();
                      }
                    }}
                    onMouseEnter={() => setHighlightIndex(index)}
                    onContextMenu={(e) => handleItemContextMenu(e, ws)}
                  >
                    {isActive && (
                      <span className={styles.activeIndicator}>&#10003;</span>
                    )}
                    <div className={styles.itemInfo}>
                      <div className={styles.itemName}>{ws.name}</div>
                      <div className={styles.itemPath}>{ws.path}</div>
                    </div>
                    {meta}
                  </div>
                );
              }

              return (
                <button
                  key={ws.id}
                  data-workspace-item
                  className={itemClass}
                  onClick={handleItemActivate}
                  onMouseEnter={() => setHighlightIndex(index)}
                  onContextMenu={(e) => handleItemContextMenu(e, ws)}
                >
                  {isActive && (
                    <span className={styles.activeIndicator}>&#10003;</span>
                  )}
                  <div className={styles.itemInfo}>
                    <div className={styles.itemName}>{ws.name}</div>
                    <div className={styles.itemPath}>{ws.path}</div>
                  </div>
                  {meta}
                </button>
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
      </div>
      {contextMenu && (
        <WorkspaceContextMenu
          isOpen={true}
          position={{ x: contextMenu.x, y: contextMenu.y }}
          onRename={handleStartRename}
          onRemove={handleStartRemove}
          onClose={handleCloseContextMenu}
          isDefault={
            defaultWorkspaceId !== undefined &&
            contextMenu.wsId === defaultWorkspaceId
          }
          {...(onSetDefault ? { onSetDefault: handleMenuSetDefault } : {})}
          {...(onClearDefault
            ? { onClearDefault: handleMenuClearDefault }
            : {})}
        />
      )}
    </div>,
    document.body,
  );
}
