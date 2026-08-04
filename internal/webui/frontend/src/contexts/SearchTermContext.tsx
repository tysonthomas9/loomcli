/**
 * Context for passing the current search term to deeply nested components
 * (IssueCard, IssueNode) without prop drilling through intermediate layers.
 */

import { createContext, useContext } from "react";

const SearchTermContext = createContext<string>("");

export const SearchTermProvider = SearchTermContext.Provider;

export function useSearchTerm(): string {
  return useContext(SearchTermContext);
}
