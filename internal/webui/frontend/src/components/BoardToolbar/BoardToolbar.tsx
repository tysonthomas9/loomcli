/**
 * BoardToolbar — Kanban/List board header (Aether wireframe board-head).
 * Tabs on the left, centered search, New Issue on the right.
 */

import type { RefObject } from "react";

import type { ViewMode } from "@/types";
import { SearchScopeIndicator } from "@/components/search/SearchScopeIndicator";
import { SearchInput } from "@/components/search/SearchInput";
import { ViewSubSwitcher } from "@/components/ViewSubSwitcher/ViewSubSwitcher";

import styles from "./BoardToolbar.module.css";

export interface BoardToolbarProps {
  activeView: ViewMode;
  onViewChange: (view: ViewMode) => void;
  searchValue: string;
  onSearchChange: (value: string) => void;
  onSearchClear: () => void;
  searchInputRef?: RefObject<HTMLInputElement> | undefined;
  searchScopeName?: string | null | undefined;
  onScopeClear?: (() => void) | undefined;
  searchPlaceholder?: string;
  onNewIssue: () => void;
}

export function BoardToolbar({
  activeView,
  onViewChange,
  searchValue,
  onSearchChange,
  onSearchClear,
  searchInputRef,
  searchScopeName,
  onScopeClear,
  searchPlaceholder = "Search tasks...",
  onNewIssue,
}: BoardToolbarProps): JSX.Element {
  return (
    <header className={styles.toolbar} data-testid="board-toolbar">
      <ViewSubSwitcher
        activeView={activeView}
        onChange={onViewChange}
        embedded
      />

      <div className={styles.searchRegion}>
        {searchScopeName && onScopeClear ? (
          <SearchScopeIndicator
            scopeName={searchScopeName}
            onClear={onScopeClear}
          />
        ) : null}
        <SearchInput
          ref={searchInputRef}
          value={searchValue}
          onChange={onSearchChange}
          onClear={onSearchClear}
          placeholder={searchPlaceholder}
          size="md"
          className={styles.searchInput ?? ""}
        />
      </div>

      <div className={styles.actions}>
        <button
          type="button"
          className={styles.newIssueButton}
          onClick={onNewIssue}
          aria-label="New Issue"
          data-testid="new-issue-button"
        >
          + New Issue
        </button>
      </div>
    </header>
  );
}
