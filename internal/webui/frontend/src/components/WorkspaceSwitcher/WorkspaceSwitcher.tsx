/**
 * WorkspaceSwitcher — quick-switcher overlay for workspace selection.
 *
 * Rendered via createPortal to document.body. Supports:
 * - Substring search filtering on workspace name and path
 * - Arrow Up/Down keyboard navigation
 * - Enter to select, Escape to close (via escape layer)
 * - Positional shortcut hints (Cmd/Ctrl+Shift+1-9)
 * - Active workspace indicator
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

import styles from "./WorkspaceSwitcher.module.css";

export interface WorkspaceSwitcherProps {
  isOpen: boolean;
  workspaces: WorkspaceSummary[];
  activeWorkspaceName: string | null;
  onSelect: (name: string) => void;
  onClose: () => void;
}

const isMac =
  typeof navigator !== "undefined" && /Mac/.test(navigator.userAgent);
const modSymbol = isMac ? "\u2318" : "Ctrl+";
const shiftSymbol = isMac ? "\u21e7" : "Shift+";

export function WorkspaceSwitcher({
  isOpen,
  workspaces,
  activeWorkspaceName,
  onSelect,
  onClose,
}: WorkspaceSwitcherProps) {
  const [search, setSearch] = useState("");
  const [highlightIndex, setHighlightIndex] = useState(0);
  const dialogRef = useRef<HTMLDivElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);

  // Register escape layer for proper priority handling
  useRegisterEscapeLayer(LAYER_WORKSPACE_SWITCHER, onClose, isOpen);

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
    (name: string) => {
      onSelect(name);
      onClose();
    },
    [onSelect, onClose],
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
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
        if (ws) handleSelect(ws.name);
      }
    },
    [filtered, highlightIndex, handleSelect],
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
              const isActive = ws.name === activeWorkspaceName;
              const originalIndex = workspaces.indexOf(ws);
              return (
                <button
                  key={ws.name}
                  data-workspace-item
                  className={[
                    styles.item,
                    index === highlightIndex ? styles.highlighted : "",
                    isActive ? styles.active : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  onClick={() => handleSelect(ws.name)}
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
              );
            })
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
