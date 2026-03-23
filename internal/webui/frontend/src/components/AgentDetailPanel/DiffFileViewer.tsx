/**
 * DiffFileViewer utilities.
 * Parses unified diff patches into structured hunk/line data for rendering.
 */

export interface DiffLine {
  type: "hunk" | "add" | "del" | "context";
  content: string;
  oldNum?: number;
  newNum?: number;
}

export interface Hunk {
  header: string;
  lines: DiffLine[];
}

export interface ParsedPatch {
  hunks: Hunk[];
}

/**
 * Parse a unified diff patch string into structured hunks and lines.
 * Handles multi-hunk patches and correctly ignores lines that start
 * with @@ but are not valid hunk headers (e.g., embedded @@ in file content).
 */
export function parsePatch(patchString: string): ParsedPatch {
  const hunks: Hunk[] = [];
  let currentHunk: Hunk | null = null;
  let oldNum = 0;
  let newNum = 0;

  for (const rawLine of patchString.split("\n")) {
    if (rawLine.startsWith("@@")) {
      const match = rawLine.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (match?.[1] && match[2]) {
        oldNum = parseInt(match[1], 10);
        newNum = parseInt(match[2], 10);
        currentHunk = { header: rawLine, lines: [] };
        hunks.push(currentHunk);
        currentHunk.lines.push({ type: "hunk", content: rawLine });
      } else if (currentHunk) {
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
          content: rawLine.slice(1),
          newNum: newNum++,
        });
      } else if (rawLine.startsWith("-")) {
        currentHunk.lines.push({
          type: "del",
          content: rawLine.slice(1),
          oldNum: oldNum++,
        });
      } else {
        // Context line (starts with space) or empty line
        const content = rawLine.startsWith(" ") ? rawLine.slice(1) : rawLine;
        currentHunk.lines.push({
          type: "context",
          content,
          oldNum: oldNum++,
          newNum: newNum++,
        });
      }
    }
  }

  return { hunks };
}
