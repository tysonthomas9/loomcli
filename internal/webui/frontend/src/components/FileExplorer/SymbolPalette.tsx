import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { createPortal } from "react-dom";

import { SearchInput } from "@/components/search";
import type { FileSymbol } from "@/utils/lezerSymbols";

import { scoreQuickOpenPath } from "./quickOpen";
import styles from "./FileExplorer.module.css";

interface SymbolPaletteProps {
  isOpen: boolean;
  symbols: FileSymbol[];
  onClose: () => void;
  onOpen: (symbol: FileSymbol) => void;
}

function rankSymbols(symbols: FileSymbol[], query: string): FileSymbol[] {
  const q = query.trim();
  return symbols
    .map((symbol, index) => {
      const target = `${symbol.name} ${symbol.kind}`;
      const match = scoreQuickOpenPath(target, q, null);
      return {
        symbol,
        score: q ? (match?.score ?? null) : 1000 - index,
      };
    })
    .filter(
      (entry): entry is { symbol: FileSymbol; score: number } =>
        entry.score !== null,
    )
    .sort((a, b) => b.score - a.score || a.symbol.line - b.symbol.line)
    .slice(0, 80)
    .map((entry) => entry.symbol);
}

export function SymbolPalette({
  isOpen,
  symbols,
  onClose,
  onOpen,
}: SymbolPaletteProps): JSX.Element | null {
  const [query, setQuery] = useState("");
  const [highlightIndex, setHighlightIndex] = useState(0);
  const resultsRef = useRef<HTMLDivElement>(null);

  const matches = useMemo(() => rankSymbols(symbols, query), [symbols, query]);

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
    const item =
      resultsRef.current?.querySelectorAll("[data-symbol-item]")[
        highlightIndex
      ];
    item?.scrollIntoView({ block: "nearest" });
  }, [highlightIndex]);

  if (!isOpen) return null;

  const openHighlighted = () => {
    const symbol = matches[highlightIndex];
    if (!symbol) return;
    onOpen(symbol);
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
        aria-label="Go to symbol in file"
      >
        <div className={styles.paletteSearch}>
          <SearchInput
            value={query}
            onChange={setQuery}
            placeholder="Go to symbol..."
            autoFocus
            size="md"
            aria-label="Go to symbol in file"
          />
        </div>
        <div className={styles.paletteResults} ref={resultsRef}>
          {matches.length === 0 ? (
            <div className={styles.emptyState}>No symbols found</div>
          ) : (
            matches.map((symbol, index) => (
              <button
                key={`${symbol.name}:${symbol.line}:${symbol.kind}`}
                type="button"
                data-symbol-item
                className={styles.paletteItem}
                data-highlighted={index === highlightIndex || undefined}
                onClick={() => {
                  onOpen(symbol);
                  onClose();
                }}
                onMouseEnter={() => setHighlightIndex(index)}
              >
                <span className={styles.paletteName}>{symbol.name}</span>
                <span className={styles.palettePath}>
                  {symbol.kind} - line {symbol.line}
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
