import type { DiffFilePatch } from "@/api/issues";
import { DiffFileViewer } from "@/components/AgentDetailPanel";

import styles from "./SessionDiffViewer.module.css";

type DiffStatus = "M" | "A" | "D" | "R";

export interface SessionDiffFile {
  path: string;
  status: DiffStatus;
  patch: DiffFilePatch;
}

function markerPath(line: string | undefined): string | null {
  if (!line) return null;
  let path = line.slice(4).trim();
  if (path === "/dev/null") return null;

  if (path.startsWith('"') && path.endsWith('"')) {
    try {
      path = JSON.parse(path) as string;
    } catch {
      path = path.slice(1, -1);
    }
  }

  return path.replace(/^[ab]\//, "");
}

function splitFilePatches(diff: string): string[] {
  const lines = diff.trimEnd().split("\n");
  const starts = lines.flatMap((line, index) =>
    line.startsWith("diff --git ") ? [index] : [],
  );

  if (starts.length === 0) return diff.trim() ? [diff.trimEnd()] : [];

  return starts.map((start, index) =>
    lines.slice(start, starts[index + 1] ?? lines.length).join("\n"),
  );
}

/** Adapt a saved, potentially multi-file git patch for the shared file viewer. */
export function parseSessionDiff(diff: string): SessionDiffFile[] {
  return splitFilePatches(diff).map((rawPatch, index) => {
    const lines = rawPatch.split("\n");
    const oldMarker = lines.find((line) => line.startsWith("--- "));
    const newMarker = lines.find((line) => line.startsWith("+++ "));
    const oldPath = markerPath(oldMarker);
    const newPath = markerPath(newMarker);
    const renameFrom = lines
      .find((line) => line.startsWith("rename from "))
      ?.slice("rename from ".length);
    const renameTo = lines
      .find((line) => line.startsWith("rename to "))
      ?.slice("rename to ".length);

    let status: DiffStatus = "M";
    if (renameFrom || renameTo) {
      status = "R";
    } else if (
      lines.some((line) => line.startsWith("new file mode ")) ||
      oldMarker === "--- /dev/null"
    ) {
      status = "A";
    } else if (
      lines.some((line) => line.startsWith("deleted file mode ")) ||
      newMarker === "+++ /dev/null"
    ) {
      status = "D";
    }

    let additions = 0;
    let deletions = 0;
    let inHunk = false;
    for (const line of lines) {
      if (line.startsWith("@@")) {
        inHunk = true;
      } else if (inHunk && line.startsWith("+")) {
        additions += 1;
      } else if (inHunk && line.startsWith("-")) {
        deletions += 1;
      }
    }

    return {
      path:
        renameTo ??
        newPath ??
        renameFrom ??
        oldPath ??
        `Changed file ${index + 1}`,
      status,
      patch: {
        patch: rawPatch,
        is_binary: lines.some(
          (line) =>
            line.startsWith("Binary files ") || line === "GIT binary patch",
        ),
        is_too_large: false,
        additions,
        deletions,
      },
    };
  });
}

interface SessionDiffViewerProps {
  diff: string;
}

export function SessionDiffViewer({
  diff,
}: SessionDiffViewerProps): JSX.Element {
  const files = parseSessionDiff(diff);
  const additions = files.reduce((sum, file) => sum + file.patch.additions, 0);
  const deletions = files.reduce((sum, file) => sum + file.patch.deletions, 0);

  return (
    <div className={styles.diff} data-testid="session-diff-viewer">
      <div className={styles.summary}>
        <span className={styles.summaryCount}>
          {files.length} {files.length === 1 ? "file" : "files"} changed
        </span>
        <span className={styles.additions}>+{additions}</span>
        <span className={styles.deletions}>-{deletions}</span>
      </div>
      {files.map((file, index) => (
        <section className={styles.file} key={`${file.path}-${index}`}>
          <header className={styles.fileHeader}>
            <span className={styles.status} data-status={file.status}>
              {file.status}
            </span>
            <span className={styles.path} title={file.path}>
              {file.path}
            </span>
            <span className={styles.fileStats}>
              <span className={styles.additions}>+{file.patch.additions}</span>
              <span className={styles.deletions}>-{file.patch.deletions}</span>
            </span>
          </header>
          <DiffFileViewer patch={file.patch} isLoading={false} />
        </section>
      ))}
    </div>
  );
}
