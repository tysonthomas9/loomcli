import { Text } from "@codemirror/state";
import { Chunk, diff } from "@codemirror/merge";

export type GitGutterMarkKind = "added" | "changed" | "deleted";

export interface GitGutterLineMark {
  line: number;
  kind: GitGutterMarkKind;
}

export const GIT_GUTTER_SCAN_LIMIT = 20_000;
export const GIT_GUTTER_TIMEOUT_MS = 40;

const diffConfig = {
  scanLimit: GIT_GUTTER_SCAN_LIMIT,
  timeout: GIT_GUTTER_TIMEOUT_MS,
};

function textFromString(value: string): Text {
  return Text.of(value.split("\n"));
}

function lineAt(doc: Text, pos: number): number {
  return doc.lineAt(Math.min(Math.max(0, pos), doc.length)).number;
}

function markRange(
  map: Map<number, GitGutterMarkKind>,
  doc: Text,
  from: number,
  to: number,
  kind: GitGutterMarkKind,
): void {
  const start = lineAt(doc, from);
  const end = lineAt(doc, Math.max(from, to - 1));
  for (let line = start; line <= end; line++) {
    map.set(line, kind);
  }
}

export function computeGitGutterLineMarks(
  base: string,
  current: string,
): GitGutterLineMark[] {
  if (base === current) return [];
  const changes = diff(base, current, diffConfig);
  if (changes.length === 0) return [];

  const baseDoc = textFromString(base);
  const currentDoc = textFromString(current);
  const chunks = Chunk.build(baseDoc, currentDoc, diffConfig);
  if (chunks.length === 0) {
    return [{ line: 1, kind: "changed" }];
  }

  const marks = new Map<number, GitGutterMarkKind>();
  for (const chunk of chunks) {
    if (chunk.fromB === chunk.toB && chunk.fromA < chunk.toA) {
      const line = lineAt(currentDoc, chunk.fromB);
      marks.set(line, "deleted");
    } else if (chunk.fromA === chunk.toA && chunk.fromB < chunk.toB) {
      markRange(marks, currentDoc, chunk.fromB, chunk.endB, "added");
    } else {
      markRange(marks, currentDoc, chunk.fromB, chunk.endB, "changed");
    }
  }
  return [...marks.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([line, kind]) => ({ line, kind }));
}
