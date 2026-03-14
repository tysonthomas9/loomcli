/**
 * SearchBar component for terminal search overlay.
 * VS Code-style search bar positioned at the top-right of the terminal view.
 */

import { useEffect, useRef } from "react";

import styles from "./TerminalView.module.css";

interface SearchBarProps {
  value: string;
  onSearch: (term: string) => void;
  onFindNext: () => void;
  onFindPrevious: () => void;
  onClose: () => void;
}

export function SearchBar({
  value,
  onSearch,
  onFindNext,
  onFindPrevious,
  onClose,
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

  return (
    <div className={styles.searchOverlay} data-testid="terminal-search-bar">
      <input
        ref={inputRef}
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
