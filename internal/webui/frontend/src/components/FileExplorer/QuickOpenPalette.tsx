import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { createPortal } from "react-dom";

import { SearchInput } from "@/components/search";

import { rankQuickOpenPaths } from "./quickOpen";
import styles from "./FileExplorer.module.css";

interface QuickOpenPaletteProps {
  isOpen: boolean;
  paths: string[];
  mru: string[];
  isLoading: boolean;
  error: string | null;
  truncated: boolean;
  onClose: () => void;
  onOpen: (path: string) => void;
}

function dirname(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

function basename(path: string): string {
  return path.split("/").pop() || path;
}

export function QuickOpenPalette({
  isOpen,
  paths,
  mru,
  isLoading,
  error,
  truncated,
  onClose,
  onOpen,
}: QuickOpenPaletteProps): JSX.Element | null {
  const [query, setQuery] = useState("");
  const [highlightIndex, setHighlightIndex] = useState(0);
  const resultsRef = useRef<HTMLDivElement>(null);

  const matches = useMemo(
    () => rankQuickOpenPaths(paths, query, mru),
    [paths, query, mru],
  );

  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setHighlightIndex(0);
    }
  }, [isOpen]);

  useEffect(() => {
    setHighlightIndex((prev) =>
      prev >= matches.length ? Math.max(0, matches.length - 1) : prev,
    );
  }, [matches.length]);

  useEffect(() => {
    const item = resultsRef.current?.querySelectorAll("[data-quick-open-item]")[
      highlightIndex
    ];
    item?.scrollIntoView({ block: "nearest" });
  }, [highlightIndex]);

  if (!isOpen) return null;

  const openHighlighted = () => {
    const match = matches[highlightIndex];
    if (!match) return;
    onOpen(match.path);
    onClose();
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      setHighlightIndex((prev) => (prev < matches.length - 1 ? prev + 1 : 0));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setHighlightIndex((prev) => (prev > 0 ? prev - 1 : matches.length - 1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      openHighlighted();
    }
  };

  return createPortal(
    <div
      className={styles.paletteOverlay}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      onKeyDown={handleKeyDown}
    >
      <div
        className={styles.paletteDialog}
        role="dialog"
        aria-modal="true"
        aria-label="Quick open"
      >
        <div className={styles.paletteSearch}>
          <SearchInput
            value={query}
            onChange={setQuery}
            placeholder="Quick open file..."
            autoFocus
            size="md"
            aria-label="Quick open file"
          />
        </div>
        {truncated && (
          <div className={styles.limitBanner} role="status">
            File index is truncated.
          </div>
        )}
        <div className={styles.paletteResults} ref={resultsRef}>
          {isLoading ? (
            <div className={styles.emptyState}>Loading files...</div>
          ) : error ? (
            <div className={styles.error}>{error}</div>
          ) : matches.length === 0 ? (
            <div className={styles.emptyState}>No files found</div>
          ) : (
            matches.map((match, index) => (
              <button
                key={match.path}
                type="button"
                data-quick-open-item
                className={styles.paletteItem}
                data-highlighted={index === highlightIndex || undefined}
                onClick={() => {
                  onOpen(match.path);
                  onClose();
                }}
                onMouseEnter={() => setHighlightIndex(index)}
              >
                <span className={styles.paletteName}>
                  {basename(match.path)}
                </span>
                <span className={styles.palettePath}>
                  {dirname(match.path)}
                </span>
              </button>
            ))
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
