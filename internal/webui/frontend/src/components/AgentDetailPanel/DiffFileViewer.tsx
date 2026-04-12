/**
 * DiffFileViewer - renders unified diff for a single file.
 * Parses patch string into hunks and renders colored lines.
 */

import type { DiffFilePatch } from "@/api/issues";

import styles from "./DiffTab.module.css";

interface DiffLine {
  type: "add" | "del" | "context" | "hunk";
  content: string;
  oldNum?: number;
  newNum?: number;
}

interface Hunk {
  header: string;
  lines: DiffLine[];
}

export interface ParsedPatch {
  hunks: Hunk[];
}

/** Parse a unified diff patch string into structured hunks. */
export function parsePatch(patchString: string): ParsedPatch {
  const hunks: Hunk[] = [];
  let currentHunk: Hunk | null = null;
  let oldNum = 0;
  let newNum = 0;

  for (const rawLine of patchString.split("\n")) {
    if (rawLine.startsWith("@@")) {
      // Parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
      const match = rawLine.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (match?.[1] && match[2]) {
        oldNum = parseInt(match[1], 10);
        newNum = parseInt(match[2], 10);
        currentHunk = { header: rawLine, lines: [] };
        hunks.push(currentHunk);
        currentHunk.lines.push({ type: "hunk", content: rawLine });
      } else if (currentHunk) {
        // Line starts with @@ but isn't a valid hunk header — treat as context
        currentHunk.lines.push({
          type: "context",
          content: rawLine,
          oldNum: oldNum++,
          newNum: newNum++,
        });
      }
    } else if (currentHunk) {
      if (rawLine.startsWith("+")) {
        currentHunk.lines.push({
          type: "add",
          content: rawLine,
          newNum: newNum++,
        });
      } else if (rawLine.startsWith("-")) {
        currentHunk.lines.push({
          type: "del",
          content: rawLine,
          oldNum: oldNum++,
        });
      } else {
        // Context line (starts with space) or unknown — treat as context
        currentHunk.lines.push({
          type: "context",
          content: rawLine,
          oldNum: oldNum++,
          newNum: newNum++,
        });
      }
    }
  }

  return { hunks };
}

interface DiffFileViewerProps {
  patch: DiffFilePatch | null;
  isLoading: boolean;
  error?: string | undefined;
}

export function DiffFileViewer({
  patch,
  isLoading,
  error,
}: DiffFileViewerProps): JSX.Element {
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

  if (!patch.patch) {
    return <div className={styles.viewerMessage}>No changes</div>;
  }

  const parsed = parsePatch(patch.patch);

  return (
    <div className={styles.viewerWrapper}>
      <pre className={styles.diffPre}>
        {parsed.hunks.map((hunk, hi) =>
          hunk.lines.map((line, li) => (
            <div
              key={`${hi}-${li}`}
              className={styles.diffLine}
              data-type={line.type}
            >
              {line.type === "hunk" ? (
                line.content
              ) : (
                <>
                  <span className={styles.lineNumber}>{line.oldNum ?? ""}</span>
                  <span className={styles.lineNumber}>{line.newNum ?? ""}</span>
                  {line.content}
                </>
              )}
            </div>
          )),
        )}
      </pre>
    </div>
  );
}
