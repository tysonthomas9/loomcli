/**
 * HighlightText component for rendering text with search matches
 * wrapped in <mark> tags for visual highlighting.
 */

import { escapeRegex } from "@/utils/escapeRegex";

import styles from "./HighlightText.module.css";

export interface HighlightTextProps {
  /** The text to render */
  text: string;
  /** The search term to highlight (empty = no highlighting) */
  searchTerm: string;
}

/**
 * Renders text with matching substrings wrapped in <mark> tags.
 * Case-insensitive matching. Handles multiple occurrences.
 * Returns plain text when searchTerm is empty or has no matches.
 */
export function HighlightText({
  text,
  searchTerm,
}: HighlightTextProps): JSX.Element {
  if (!searchTerm) return <>{text}</>;

  const escaped = escapeRegex(searchTerm);
  const parts = text.split(new RegExp(`(${escaped})`, "gi"));

  if (parts.length === 1) return <>{text}</>;

  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <mark key={i} className={styles.highlight}>
            {part}
          </mark>
        ) : (
          part
        ),
      )}
    </>
  );
}
