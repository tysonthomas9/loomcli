import { useMemo } from "react";
import { DiffModeEnum, DiffView } from "@git-diff-view/react";
import "@git-diff-view/react/styles/diff-view-pure.css";

import type { DiffFilePatch } from "@/api/issues";
import { useTheme } from "@/hooks";

import styles from "./GitDiffViewer.module.css";

export interface GitDiffViewerProps {
  patch: DiffFilePatch | null;
  isLoading: boolean;
  error?: string | undefined;
  filePath?: string | undefined;
}

function hasUnifiedFileHeader(patch: string): boolean {
  return /^---\s/m.test(patch) && /^\+\+\+\s/m.test(patch);
}

function normalizeDiffPath(path: string | undefined): string {
  return (path && path.length > 0 ? path : "file").replace(/\\/g, "/");
}

function buildDiffInput(patch: string, filePath: string | undefined): string {
  const normalizedPatch = patch.endsWith("\n") ? patch : `${patch}\n`;
  if (hasUnifiedFileHeader(normalizedPatch)) return normalizedPatch;

  const path = normalizeDiffPath(filePath);
  return `--- a/${path}\n+++ b/${path}\n${normalizedPatch}`;
}

export function GitDiffViewer({
  patch,
  isLoading,
  error,
  filePath,
}: GitDiffViewerProps): JSX.Element {
  const { theme } = useTheme();
  const diffData = useMemo(() => {
    if (!patch?.patch) return null;
    const path = normalizeDiffPath(filePath);
    return {
      oldFile: { fileName: path, content: "" },
      newFile: { fileName: path, content: "" },
      hunks: [buildDiffInput(patch.patch, filePath)],
    };
  }, [filePath, patch?.patch]);

  if (isLoading) {
    return <div className={styles.loading}>Loading diff…</div>;
  }

  if (error) {
    return <div className={styles.error}>{error}</div>;
  }

  if (!patch) {
    return <div className={styles.viewerMessage}>No changes</div>;
  }

  if (patch.is_binary) {
    return (
      <div className={styles.viewerMessage}>
        Binary file — cannot display diff
      </div>
    );
  }

  if (patch.is_too_large) {
    return (
      <div className={styles.viewerMessage}>File too large to display</div>
    );
  }

  if (!patch.patch || !diffData) {
    return <div className={styles.viewerMessage}>No changes</div>;
  }

  return (
    <div className={styles.viewerWrapper} data-testid="git-diff-viewer">
      <DiffView
        className={styles.diffView ?? ""}
        data={diffData}
        diffViewMode={DiffModeEnum.Unified}
        diffViewTheme={theme}
        diffViewFontSize={12}
        diffViewWrap
      />
    </div>
  );
}
