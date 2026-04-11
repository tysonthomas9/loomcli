/**
 * BackendSelectorDropdown component.
 * Searchable dropdown for selecting an AI backend (Claude, Codex, OpenCode, etc.)
 * with brand-colored indicators and availability status.
 */

import { useState, useCallback, useRef, useEffect, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import type { BackendInfo } from "@/utils/backendDefaults";

import styles from "./BackendSelectorDropdown.module.css";

export interface BackendSelectorDropdownProps {
  /** Available backends with metadata */
  backends: BackendInfo[];
  /** Currently selected backend name */
  selectedBackend: string;
  /** Callback when a backend is selected */
  onSelect: (name: string) => Promise<void>;
  /** Whether the dropdown is disabled */
  disabled?: boolean;
  /** Whether a save is in progress */
  isSaving?: boolean;
  /** Additional CSS class name */
  className?: string;
  /** Placeholder text when no backend is selected */
  placeholder?: string;
}

export function BackendSelectorDropdown({
  backends,
  selectedBackend,
  onSelect,
  disabled,
  isSaving,
  className,
  placeholder = "Select backend",
}: BackendSelectorDropdownProps): JSX.Element {
  const [, setSearchParams] = useSearchParams();
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticBackend, setOptimisticBackend] = useState(selectedBackend);
  const [searchQuery, setSearchQuery] = useState("");
  const [focusedIndex, setFocusedIndex] = useState(-1);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticBackend(selectedBackend);
  }, [selectedBackend]);

  // Focus search input when dropdown opens
  useEffect(() => {
    if (isOpen) {
      requestAnimationFrame(() => {
        searchRef.current?.focus();
      });
    }
  }, [isOpen]);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setSearchQuery("");
        setFocusedIndex(-1);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Filter backends by search query
  const filteredBackends = useMemo(() => {
    if (!searchQuery) return backends;
    const q = searchQuery.toLowerCase();
    return backends.filter(
      (b) =>
        b.name.toLowerCase().includes(q) ||
        b.displayName.toLowerCase().includes(q) ||
        b.provider.toLowerCase().includes(q),
    );
  }, [backends, searchQuery]);

  // Get enabled (available) items for keyboard navigation
  const enabledIndices = useMemo(() => {
    return filteredBackends
      .map((b, i) => (b.available ? i : -1))
      .filter((i) => i >= 0);
  }, [filteredBackends]);

  const handleTriggerClick = useCallback(() => {
    if (disabled || isSaving) return;
    setError(null);
    setIsOpen((prev) => !prev);
    setSearchQuery("");
    if (!isOpen) {
      const currentIndex = filteredBackends.findIndex(
        (b) => b.name === optimisticBackend,
      );
      setFocusedIndex(currentIndex);
    }
  }, [disabled, isSaving, isOpen, filteredBackends, optimisticBackend]);

  const handleSelect = useCallback(
    async (backend: BackendInfo) => {
      if (!backend.available) return;
      if (backend.name === selectedBackend) {
        setIsOpen(false);
        setSearchQuery("");
        setFocusedIndex(-1);
        return;
      }

      const previousBackend = selectedBackend;
      setOptimisticBackend(backend.name);
      setIsOpen(false);
      setSearchQuery("");
      setFocusedIndex(-1);
      setError(null);

      try {
        await onSelect(backend.name);
      } catch (err) {
        setOptimisticBackend(previousBackend);
        const message =
          err instanceof Error ? err.message : "Failed to select backend";
        setError(message);
      }
    },
    [selectedBackend, onSelect],
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (!isOpen) return;

      switch (event.key) {
        case "ArrowDown": {
          event.preventDefault();
          if (enabledIndices.length === 0) break;
          const currentEnabledPos = enabledIndices.indexOf(focusedIndex);
          const nextPos = Math.min(
            currentEnabledPos + 1,
            enabledIndices.length - 1,
          );
          setFocusedIndex(enabledIndices[nextPos] ?? focusedIndex);
          break;
        }
        case "ArrowUp": {
          event.preventDefault();
          if (enabledIndices.length === 0) break;
          const currentEnabledPos = enabledIndices.indexOf(focusedIndex);
          const prevPos = Math.max(
            currentEnabledPos <= 0 ? 0 : currentEnabledPos - 1,
            0,
          );
          setFocusedIndex(enabledIndices[prevPos] ?? focusedIndex);
          break;
        }
        case "Enter": {
          event.preventDefault();
          const focused = filteredBackends[focusedIndex];
          if (focused && focused.available) {
            handleSelect(focused);
          }
          break;
        }
        case "Home": {
          event.preventDefault();
          if (enabledIndices.length > 0) {
            setFocusedIndex(enabledIndices[0] ?? -1);
          }
          break;
        }
        case "End": {
          event.preventDefault();
          if (enabledIndices.length > 0) {
            setFocusedIndex(enabledIndices[enabledIndices.length - 1] ?? -1);
          }
          break;
        }
        case "Escape": {
          event.stopPropagation();
          setIsOpen(false);
          setSearchQuery("");
          setFocusedIndex(-1);
          triggerRef.current?.focus();
          break;
        }
      }
    },
    [isOpen, focusedIndex, enabledIndices, filteredBackends, handleSelect],
  );

  // Find the selected backend info for the trigger display
  const selectedInfo = backends.find((b) => b.name === optimisticBackend);
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.backendSelector, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        disabled={isDisabled}
        data-saving={isSaving || undefined}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label={`Backend: ${selectedInfo?.displayName ?? placeholder}. Click to change.`}
        data-testid="backend-selector-trigger"
      >
        {selectedInfo && (
          <span
            className={styles.brandDot}
            style={{ backgroundColor: selectedInfo.brandColor }}
          />
        )}
        <span className={styles.triggerText}>
          {selectedInfo?.displayName ?? placeholder}
        </span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div
          className={styles.menu}
          role="listbox"
          aria-label="Select backend"
          data-testid="backend-selector-menu"
          onKeyDown={handleKeyDown}
        >
          <input
            ref={searchRef}
            type="text"
            className={styles.searchInput}
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value);
              setFocusedIndex(-1);
            }}
            placeholder="Search backends..."
            data-testid="backend-selector-search"
          />
          <div className={styles.optionsList}>
            {filteredBackends.length === 0 ? (
              <div
                className={styles.emptyState}
                data-testid="backend-selector-empty"
              >
                {backends.length === 0
                  ? "No backends configured"
                  : "No matching backends"}
              </div>
            ) : (
              filteredBackends.map((backend, index) => (
                <div
                  key={backend.name}
                  className={styles.option}
                  data-selected={
                    backend.name === optimisticBackend || undefined
                  }
                  data-focused={index === focusedIndex || undefined}
                  data-disabled={!backend.available || undefined}
                  role="option"
                  aria-selected={backend.name === optimisticBackend}
                  aria-disabled={!backend.available}
                  onClick={() => handleSelect(backend)}
                  data-testid={`backend-option-${backend.name}`}
                >
                  <span
                    className={styles.brandDot}
                    style={{ backgroundColor: backend.brandColor }}
                  />
                  <span className={styles.optionContent}>
                    <span className={styles.displayName}>
                      {backend.displayName}
                    </span>
                    <span className={styles.provider}>{backend.provider}</span>
                  </span>
                  {!backend.available && (
                    <button
                      type="button"
                      className={styles.configureLink}
                      onClick={(e) => {
                        e.stopPropagation();
                        setSearchParams(
                          { view: "settings" },
                          { replace: true },
                        );
                      }}
                      data-testid={`backend-configure-${backend.name}`}
                    >
                      Configure in Settings
                    </button>
                  )}
                  {backend.name === optimisticBackend && backend.available && (
                    <span className={styles.checkmark} aria-hidden="true">
                      ✓
                    </span>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      )}

      {isSaving && (
        <span
          className={styles.savingIndicator}
          aria-label="Saving..."
          data-testid="backend-selector-saving"
        />
      )}

      {error && (
        <span
          className={styles.error}
          role="alert"
          data-testid="backend-selector-error"
        >
          {error}
        </span>
      )}
    </div>
  );
}
