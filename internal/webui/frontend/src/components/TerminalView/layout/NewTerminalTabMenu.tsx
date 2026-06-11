/**
 * Dropdown menu anchored to the terminal tab bar "+" control.
 * Selecting a backend creates a new lead session tab immediately.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { KNOWN_BACKEND_DEFAULTS } from "@/utils/workspace";

import styles from "./NewTerminalTabMenu.module.css";

export interface NewTerminalTabMenuProps {
  availableBackends: string[];
  isLoading?: boolean;
  disabled?: boolean;
  onSelect: (backend: string) => void;
  /** Called when the trigger is clicked while disabled (e.g. max tabs). */
  onDisabledAttempt?: () => void;
}

export function NewTerminalTabMenu({
  availableBackends,
  isLoading = false,
  disabled = false,
  onSelect,
  onDisabledAttempt,
}: NewTerminalTabMenuProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  /** Plain shell first, then configured AI backends (no provider subtitles). */
  const menuBackends = useMemo(() => {
    const aiBackends = availableBackends.filter((b) => b !== "shell");
    return ["shell", ...aiBackends];
  }, [availableBackends]);

  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        rootRef.current &&
        !rootRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setFocusedIndex(-1);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Move focus into the menu on open so Arrow/Home/End/Enter reach the
  // keydown handler (the trigger is a sibling, not an ancestor, of the menu).
  useEffect(() => {
    if (isOpen) menuRef.current?.focus();
  }, [isOpen]);

  const handleTriggerClick = useCallback(() => {
    if (disabled) {
      onDisabledAttempt?.();
      return;
    }
    setIsOpen((prev) => !prev);
    if (!isOpen) {
      setFocusedIndex(menuBackends.length > 0 ? 0 : -1);
    }
  }, [disabled, isOpen, menuBackends.length, onDisabledAttempt]);

  const handleSelect = useCallback(
    (backend: string) => {
      setIsOpen(false);
      setFocusedIndex(-1);
      onSelect(backend);
    },
    [onSelect],
  );

  const handleMenuKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (!isOpen) return;

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          if (menuBackends.length === 0) break;
          setFocusedIndex((i) => Math.min(i + 1, menuBackends.length - 1));
          break;
        case "ArrowUp":
          event.preventDefault();
          if (menuBackends.length === 0) break;
          setFocusedIndex((i) => Math.max(i - 1, 0));
          break;
        case "Enter": {
          event.preventDefault();
          const backend = menuBackends[focusedIndex];
          if (backend) handleSelect(backend);
          break;
        }
        case "Escape":
          event.stopPropagation();
          setIsOpen(false);
          setFocusedIndex(-1);
          triggerRef.current?.focus();
          break;
        case "Home":
          event.preventDefault();
          if (menuBackends.length > 0) setFocusedIndex(0);
          break;
        case "End":
          event.preventDefault();
          if (menuBackends.length > 0) setFocusedIndex(menuBackends.length - 1);
          break;
      }
    },
    [isOpen, menuBackends, focusedIndex, handleSelect],
  );

  return (
    // Keydown is handled at the root so navigation works whether focus sits
    // on the trigger or inside the menu.
    <div ref={rootRef} className={styles.menuRoot} onKeyDown={handleMenuKeyDown}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        disabled={isLoading}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        aria-label="New terminal tab"
        title={
          disabled
            ? "Maximum terminal tabs reached"
            : "New terminal tab"
        }
        data-open={isOpen || undefined}
        data-testid="terminal-new-tab-button"
      >
        +
      </button>

      {isOpen && (
        <div
          ref={menuRef}
          className={styles.menu}
          role="menu"
          aria-label="New terminal session"
          data-testid="new-terminal-tab-menu"
          tabIndex={-1}
        >
          <div className={styles.menuHeader}>New session</div>
          {isLoading ? (
            <div className={styles.statusText} data-testid="new-tab-menu-loading">
              Loading backends...
            </div>
          ) : menuBackends.length === 0 ? (
            <div className={styles.statusText} data-testid="new-tab-menu-empty">
              No backends available
            </div>
          ) : (
            menuBackends.map((backend, index) => {
              const info = KNOWN_BACKEND_DEFAULTS[backend];
              const label = info?.displayName ?? backend;
              return (
                <button
                  key={backend}
                  type="button"
                  role="menuitem"
                  className={styles.option}
                  data-focused={index === focusedIndex || undefined}
                  onClick={() => handleSelect(backend)}
                  data-testid={`new-tab-backend-${backend}`}
                >
                  <span
                    className={styles.brandDot}
                    style={{
                      backgroundColor: info?.brandColor ?? "var(--color-text-muted)",
                    }}
                    aria-hidden="true"
                  />
                  <span className={styles.displayName}>{label}</span>
                </button>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
