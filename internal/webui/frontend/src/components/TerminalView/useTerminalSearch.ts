import {
  useState,
  useCallback,
  useEffect,
  useRef,
  type MutableRefObject,
} from "react";

import { useRegisterEscapeLayer, LAYER_TERMINAL_SEARCH } from "@/hooks/ui";

import type {
  SearchResultInfo,
  TerminalInstanceHandle,
} from "./TerminalInstance";

export interface UseTerminalSearchOptions {
  instanceRefs: MutableRefObject<Map<string, TerminalInstanceHandle>>;
  activeTabId: string;
  isSplitView: boolean;
  focusedPane: "left" | "right";
  rightPaneTabId: string;
  isActive: boolean;
}

export interface UseTerminalSearchReturn {
  isSearchOpen: boolean;
  searchTerm: string;
  caseSensitive: boolean;
  useRegex: boolean;
  searchResult: SearchResultInfo | null;
  searchTargetTabId: string;
  setIsSearchOpen: React.Dispatch<React.SetStateAction<boolean>>;
  handleSearch: (term: string) => void;
  handleFindNext: () => void;
  handleFindPrevious: () => void;
  handleSearchClose: () => void;
  handleToggleCaseSensitive: () => void;
  handleToggleRegex: () => void;
  handleSearchResultChange: (
    tabId: string,
    result: SearchResultInfo | null,
  ) => void;
  handleSearchRequest: () => void;
}

export function useTerminalSearch({
  instanceRefs,
  activeTabId,
  isSplitView,
  focusedPane,
  rightPaneTabId,
  isActive,
}: UseTerminalSearchOptions): UseTerminalSearchReturn {
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const [searchResult, setSearchResult] = useState<SearchResultInfo | null>(
    null,
  );

  // The tab targeted by search: in split mode, use the focused pane's tab
  const searchTargetTabId =
    isSplitView && focusedPane === "right" ? rightPaneTabId : activeTabId;
  const searchTargetTabIdRef = useRef(searchTargetTabId);
  searchTargetTabIdRef.current = searchTargetTabId;

  const closeTerminalSearch = useCallback(() => {
    setIsSearchOpen(false);
    instanceRefs.current.get(searchTargetTabId)?.clearSearch();
    setSearchTerm("");
    setSearchResult(null);
  }, [searchTargetTabId, instanceRefs]);

  useRegisterEscapeLayer(
    LAYER_TERMINAL_SEARCH,
    closeTerminalSearch,
    isActive && isSearchOpen,
  );

  // Re-run search on tab switch while search is open
  useEffect(() => {
    if (isSearchOpen && searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive, regex: useRegex });
    }
  }, [
    searchTargetTabId,
    isSearchOpen,
    searchTerm,
    caseSensitive,
    useRegex,
    instanceRefs,
  ]);

  const handleSearch = useCallback(
    (term: string) => {
      setSearchTerm(term);
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(term, { caseSensitive, regex: useRegex });
    },
    [searchTargetTabId, caseSensitive, useRegex, instanceRefs],
  );

  const handleFindNext = useCallback(() => {
    instanceRefs.current.get(searchTargetTabId)?.findNext();
  }, [searchTargetTabId, instanceRefs]);

  const handleFindPrevious = useCallback(() => {
    instanceRefs.current.get(searchTargetTabId)?.findPrevious();
  }, [searchTargetTabId, instanceRefs]);

  const handleSearchClose = closeTerminalSearch;

  const handleToggleCaseSensitive = useCallback(() => {
    const next = !caseSensitive;
    setCaseSensitive(next);
    if (searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive: next, regex: useRegex });
    }
  }, [searchTargetTabId, searchTerm, caseSensitive, useRegex, instanceRefs]);

  const handleToggleRegex = useCallback(() => {
    const next = !useRegex;
    setUseRegex(next);
    if (searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive, regex: next });
    }
  }, [searchTargetTabId, searchTerm, caseSensitive, useRegex, instanceRefs]);

  // Only process search result changes from the search-targeted tab
  const handleSearchResultChange = useCallback(
    (tabId: string, result: SearchResultInfo | null) => {
      if (tabId === searchTargetTabIdRef.current) {
        setSearchResult(result);
      }
    },
    [],
  );

  // Search request from terminal (Ctrl+Shift+F)
  const handleSearchRequest = useCallback(() => {
    setIsSearchOpen((prev) => !prev);
  }, []);

  return {
    isSearchOpen,
    searchTerm,
    caseSensitive,
    useRegex,
    searchResult,
    searchTargetTabId,
    setIsSearchOpen,
    handleSearch,
    handleFindNext,
    handleFindPrevious,
    handleSearchClose,
    handleToggleCaseSensitive,
    handleToggleRegex,
    handleSearchResultChange,
    handleSearchRequest,
  };
}
