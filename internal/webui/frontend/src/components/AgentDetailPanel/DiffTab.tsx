/**
 * DiffTab component for the AgentDetailPanel.
 * Displays file list and expandable file diffs with per-file error handling.
 *
 * Per-file patch errors are tracked independently via `patchErrors` from useDiff.
 * A failed fetch for file A does NOT show file A's error on file B.
 */

import { useState, useCallback } from "react";

import { useDiff, type DiffFile } from "@/hooks/useDiff";

/** Props for a single file diff viewer. */
export interface DiffFileViewerProps {
  file: DiffFile;
  patch?: string;
  language?: string;
  isLoading: boolean;
  error?: string;
}

/** Inline DiffFileViewer. */
function DiffFileViewer({
  file,
  patch,
  isLoading,
  error,
}: DiffFileViewerProps) {
  return (
    <div data-testid={`diff-file-${file.path}`}>
      {error && (
        <div data-testid={`diff-error-${file.path}`} className="diff-error">
          Error: {error}
        </div>
      )}
      {isLoading && (
        <div data-testid={`diff-loading-${file.path}`}>Loading...</div>
      )}
      {patch && <pre data-testid={`diff-patch-${file.path}`}>{patch}</pre>}
    </div>
  );
}

/** Props for DiffTab. */
export interface DiffTabProps {
  agentId: string | null;
  toCommit: string;
  fromCommit?: string;
}

/**
 * DiffTab renders the file list and expandable diffs for an agent.
 */
export function DiffTab({ agentId, toCommit, fromCommit }: DiffTabProps) {
  const { files, isLoading, error, patchCache, patchErrors, fetchPatch } =
    useDiff({ agentId, toCommit, fromCommit });

  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(
    () => new Set(),
  );

  const toggleFile = useCallback(
    (path: string) => {
      setExpandedFiles((prev) => {
        const next = new Set(prev);
        if (next.has(path)) {
          next.delete(path);
        } else {
          next.add(path);
          // Fetch patch when expanding
          if (!patchCache.has(path)) {
            fetchPatch(path);
          }
        }
        return next;
      });
    },
    [patchCache, fetchPatch],
  );

  // File-list level error
  if (error && files.length === 0) {
    return (
      <div data-testid="diff-tab-error">
        Failed to load diff: {error.message}
      </div>
    );
  }

  if (isLoading) {
    return <div data-testid="diff-tab-loading">Loading diff...</div>;
  }

  if (files.length === 0) {
    return <div data-testid="diff-tab-empty">No changes</div>;
  }

  return (
    <div data-testid="diff-tab">
      {files.map((file) => {
        const isExpanded = expandedFiles.has(file.path);
        const cachedPatch = patchCache.get(file.path);
        const fileError = patchErrors.get(file.path);

        return (
          <div key={file.path} data-testid={`diff-file-row-${file.path}`}>
            <button
              data-testid={`diff-toggle-${file.path}`}
              onClick={() => toggleFile(file.path)}
            >
              {file.path} (+{file.additions} -{file.deletions})
            </button>
            {isExpanded && (
              <DiffFileViewer
                file={file}
                patch={cachedPatch?.patch}
                isLoading={!cachedPatch && !fileError}
                error={fileError?.message}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}
