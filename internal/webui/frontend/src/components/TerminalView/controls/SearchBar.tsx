/**
 * SearchBar component for terminal search overlay.
 * VS Code-style search bar positioned at the top-right of the terminal view.
 */

import { useEffect, useRef } from "react";

import styles from "./SearchBar.module.css";

interface SearchBarProps {
  value: string;
  onSearch: (term: string) => void;
  onFindNext: () => void;
  onFindPrevious: () => void;
  onClose: () => void;
  matchIndex: number | null;
  matchCount: number | null;
  caseSensitive: boolean;
  regex: boolean;
  onToggleCaseSensitive: () => void;
  onToggleRegex: () => void;
}

export function SearchBar({
  value,
  onSearch,
  onFindNext,
  onFindPrevious,
  onClose,
  matchIndex,
  matchCount,
  caseSensitive,
  regex,
  onToggleCaseSensitive,
  onToggleRegex,
}: SearchBarProps): JSX.Element {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault();
      if (e.shiftKey) {
        onFindPrevious();
      } else {
        onFindNext();
      }
    }
  };

  const renderMatchCounter = () => {
    if (!value) return null;
    if (matchCount === null) return null;
    if (matchCount === 0) {
      return <span className={styles.noResults}>No results</span>;
    }
    if (matchIndex === -1) {
      return (
        <span className={styles.searchCounter}>{matchCount}+ matches</span>
      );
    }
    return (
      <span className={styles.searchCounter}>
        {(matchIndex ?? 0) + 1} of {matchCount}
      </span>
    );
  };

  return (
    <div
      className={styles.searchOverlay}
      role="search"
      aria-label="Search terminal output"
      data-testid="terminal-search-bar"
    >
      <input
        ref={inputRef}
        id="terminal-search-input"
        type="text"
        className={styles.searchInput}
        value={value}
        onChange={(e) => onSearch(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Find..."
        aria-label="Search terminal"
        data-testid="terminal-search-input"
      />
      <button
        type="button"
        className={`${styles.searchToggle} ${caseSensitive ? styles.searchToggleActive : ""}`}
        onClick={onToggleCaseSensitive}
        aria-label="Match Case"
        aria-pressed={caseSensitive}
        title="Match Case"
        data-testid="search-toggle-case"
      >
        Aa
      </button>
      <button
        type="button"
        className={`${styles.searchToggle} ${regex ? styles.searchToggleActive : ""}`}
        onClick={onToggleRegex}
        aria-label="Use Regular Expression"
        aria-pressed={regex}
        title="Use Regular Expression"
        data-testid="search-toggle-regex"
      >
        .*
      </button>
      {renderMatchCounter()}
      <button
        type="button"
        className={styles.searchButton}
        onClick={onFindPrevious}
        aria-label="Previous match"
        title="Previous match (Shift+Enter)"
      >
        &#x25B2;
      </button>
      <button
        type="button"
        className={styles.searchButton}
        onClick={onFindNext}
        aria-label="Next match"
        title="Next match (Enter)"
      >
        &#x25BC;
      </button>
      <button
        type="button"
        className={styles.searchButton}
        onClick={onClose}
        aria-label="Close search"
        title="Close (Escape)"
      >
        &#x2715;
      </button>
    </div>
  );
}
