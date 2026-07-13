import { useCallback, useMemo, useState, type FormEvent } from "react";

import type {
  FileScopeRef,
  FileSearchData,
  FileSearchFileResult,
  FileSearchRequest,
} from "@/api/workspace";
import {
  readScopedFile,
  searchScopedFiles,
  writeScopedFile,
} from "@/hooks/api";

import {
  createReplacementPreview,
  parseGlobText,
  type ReplacementPreview,
} from "./searchReplace";
import styles from "./FileExplorer.module.css";

interface FileSearchPanelProps {
  workspaceId: string;
  scopeRef: FileScopeRef;
  onOpenResult: (path: string, line: number) => void;
  onFilesChanged: (paths: string[]) => void;
  onClose: () => void;
}

function uniqueResultPaths(results: FileSearchFileResult[]): string[] {
  return [...new Set(results.map((result) => result.path))];
}

export function FileSearchPanel({
  workspaceId,
  scopeRef,
  onOpenResult,
  onFilesChanged,
  onClose,
}: FileSearchPanelProps): JSX.Element {
  const [query, setQuery] = useState("");
  const [replacement, setReplacement] = useState("");
  const [regex, setRegex] = useState(false);
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [defaultExcludes, setDefaultExcludes] = useState(true);
  const [includeText, setIncludeText] = useState("");
  const [excludeText, setExcludeText] = useState("");
  const [data, setData] = useState<FileSearchData | null>(null);
  const [previews, setPreviews] = useState<ReplacementPreview[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  const [isPreviewing, setIsPreviewing] = useState(false);
  const [isApplying, setIsApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const affectedPaths = useMemo(
    () => uniqueResultPaths(data?.results ?? []),
    [data],
  );

  const searchRequest = useCallback(() => {
    const include = parseGlobText(includeText);
    const exclude = defaultExcludes ? undefined : parseGlobText(excludeText);
    const request: FileSearchRequest = {
      query,
      regex,
      caseSensitive,
    };
    if (include.length > 0) request.include = include;
    if (exclude !== undefined) request.exclude = exclude;
    return request;
  }, [caseSensitive, defaultExcludes, excludeText, includeText, query, regex]);

  const runSearch = useCallback(
    async (event?: FormEvent) => {
      event?.preventDefault();
      if (!query.trim()) return;
      setIsSearching(true);
      setError(null);
      setPreviews([]);
      try {
        const result = await searchScopedFiles(
          workspaceId,
          scopeRef,
          searchRequest(),
        );
        setData(result);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setIsSearching(false);
      }
    },
    [query, scopeRef, searchRequest, workspaceId],
  );

  const buildPreview = useCallback(async () => {
    if (!data || affectedPaths.length === 0 || !query.trim()) return;
    setIsPreviewing(true);
    setError(null);
    try {
      const next: ReplacementPreview[] = [];
      for (const path of affectedPaths) {
        const file = await readScopedFile(workspaceId, scopeRef, path);
        if (file.binary || file.truncated || file.content === undefined) {
          continue;
        }
        const preview = createReplacementPreview(path, file.content, {
          query,
          replacement,
          regex,
          caseSensitive,
        });
        if (preview) next.push(preview);
      }
      setPreviews(next);
      if (next.length === 0) {
        setError("No replaceable text found in the current results.");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsPreviewing(false);
    }
  }, [
    affectedPaths,
    caseSensitive,
    data,
    query,
    regex,
    replacement,
    scopeRef,
    workspaceId,
  ]);

  const applyPreviews = useCallback(async () => {
    if (previews.length === 0) return;
    setIsApplying(true);
    setError(null);
    try {
      for (const preview of previews) {
        await writeScopedFile(
          workspaceId,
          scopeRef,
          preview.path,
          preview.after,
        );
      }
      onFilesChanged(previews.map((preview) => preview.path));
      setPreviews([]);
      await runSearch();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsApplying(false);
    }
  }, [onFilesChanged, previews, runSearch, scopeRef, workspaceId]);

  return (
    <aside className={styles.searchPanel} aria-label="File search">
      <div className={styles.searchPanelHeader}>
        <span>Search</span>
        <button
          type="button"
          className={styles.iconButton}
          aria-label="Close search"
          onClick={onClose}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M4 4l8 8M12 4l-8 8"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
      <form className={styles.searchForm} onSubmit={runSearch}>
        <input
          className={styles.filterInput}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search files..."
          aria-label="Search files"
          autoFocus
        />
        <input
          className={styles.filterInput}
          value={replacement}
          onChange={(event) => setReplacement(event.target.value)}
          placeholder="Replace..."
          aria-label="Replace with"
        />
        <div className={styles.searchOptions}>
          <label>
            <input
              type="checkbox"
              checked={caseSensitive}
              onChange={(event) => setCaseSensitive(event.target.checked)}
            />
            <span>Case</span>
          </label>
          <label>
            <input
              type="checkbox"
              checked={regex}
              onChange={(event) => setRegex(event.target.checked)}
            />
            <span>Regex</span>
          </label>
          <label>
            <input
              type="checkbox"
              checked={defaultExcludes}
              onChange={(event) => setDefaultExcludes(event.target.checked)}
            />
            <span>Defaults</span>
          </label>
        </div>
        <input
          className={styles.filterInput}
          value={includeText}
          onChange={(event) => setIncludeText(event.target.value)}
          placeholder="Include globs"
          aria-label="Include globs"
        />
        {!defaultExcludes && (
          <input
            className={styles.filterInput}
            value={excludeText}
            onChange={(event) => setExcludeText(event.target.value)}
            placeholder="Exclude globs"
            aria-label="Exclude globs"
          />
        )}
        <div className={styles.searchActions}>
          <button
            type="submit"
            className={styles.secondaryButton}
            disabled={!query.trim() || isSearching}
          >
            {isSearching ? "Searching..." : "Search"}
          </button>
          <button
            type="button"
            className={styles.secondaryButton}
            disabled={!data || affectedPaths.length === 0 || isPreviewing}
            onClick={() => void buildPreview()}
          >
            {isPreviewing ? "Previewing..." : "Preview"}
          </button>
        </div>
      </form>
      {error && <div className={styles.searchError}>{error}</div>}
      {data?.limitHit && (
        <div className={styles.limitBanner} role="status">
          Search limit reached.
        </div>
      )}
      {previews.length > 0 && (
        <section className={styles.replacePreview}>
          <div className={styles.replacePreviewHeader}>
            <span>{previews.length} file preview</span>
            <button
              type="button"
              className={styles.dangerButton}
              disabled={isApplying}
              onClick={() => void applyPreviews()}
            >
              {isApplying ? "Applying..." : "Apply"}
            </button>
          </div>
          <div className={styles.replacePreviewList}>
            {previews.map((preview) => (
              <details key={preview.path} open>
                <summary>{preview.path}</summary>
                <pre>{preview.diffLines.join("\n")}</pre>
              </details>
            ))}
          </div>
        </section>
      )}
      <div className={styles.searchResults}>
        {!data ? (
          <div className={styles.emptyState}>No search run</div>
        ) : data.results.length === 0 ? (
          <div className={styles.emptyState}>No results</div>
        ) : (
          data.results.map((result) => (
            <div key={result.path} className={styles.searchResultGroup}>
              <div className={styles.searchResultPath}>{result.path}</div>
              {result.matches.map((match, index) => (
                <button
                  type="button"
                  key={`${result.path}:${match.line}:${match.col}:${index}`}
                  className={styles.searchMatch}
                  onClick={() => onOpenResult(result.path, match.line)}
                >
                  <span className={styles.searchMatchLine}>{match.line}</span>
                  <span className={styles.searchMatchPreview}>
                    {match.preview}
                  </span>
                </button>
              ))}
            </div>
          ))
        )}
      </div>
    </aside>
  );
}
