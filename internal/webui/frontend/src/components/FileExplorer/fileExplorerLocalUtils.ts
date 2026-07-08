import type { FileEntry } from "@/api/workspace";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import { resolveTreeDropMove } from "./gitDecorations";
import type { ExplorerLens } from "./workspaceFileBrowserTypes";

export const TREE_WIDTH_KEY = "loom:file-browser:tree-width";
export const DELETE_FILE_SKIP_KEY = "file-browser-delete-files-without-confirm";
export const FILE_EXPLORER_LENS_KEY = "file-explorer-lens";
export const DEFAULT_TREE_WIDTH = 320;
export const MIN_TREE_WIDTH = 240;
export const MAX_TREE_WIDTH = 400;

export const DEFAULT_GROUP_WIDTH = 560;
export const MIN_GROUP_WIDTH = 320;
export const MAX_GROUP_WIDTH = 1100;
export const QUICK_OPEN_STALE_MS = 10_000;

export function clampTreeWidth(w: number): number {
  return Math.min(MAX_TREE_WIDTH, Math.max(MIN_TREE_WIDTH, w));
}

export function getStoredTreeWidth(): number {
  try {
    const raw = localStorage.getItem(TREE_WIDTH_KEY);
    if (raw !== null) {
      const n = Number(raw);
      if (Number.isFinite(n) && n > 0) return clampTreeWidth(n);
    }
  } catch {
    // localStorage unavailable
  }
  return DEFAULT_TREE_WIDTH;
}

export function storeTreeWidth(w: number): void {
  try {
    localStorage.setItem(TREE_WIDTH_KEY, String(w));
  } catch {
    // localStorage unavailable
  }
}

export function getStoredLens(workspaceId: string): ExplorerLens {
  return wsGet(workspaceId, FILE_EXPLORER_LENS_KEY) === "changes"
    ? "changes"
    : "files";
}

export function storeLens(workspaceId: string, lens: ExplorerLens): void {
  wsSet(workspaceId, FILE_EXPLORER_LENS_KEY, lens);
}

export function basename(path: string): string {
  return path.split("/").pop() || path;
}

export function dirname(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

export function joinPath(parent: string, child: string): string {
  const cleanChild = child.replace(/^\/+|\/+$/g, "");
  return parent ? `${parent}/${cleanChild}` : cleanChild;
}

export function pathMatchesPrefix(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}/`);
}

export function shallowRecordEqual(
  a: Record<string, string> | undefined,
  b: Record<string, string>,
): boolean {
  if (!a) return false;
  const aEntries = Object.entries(a);
  const bEntries = Object.entries(b);
  if (aEntries.length !== bEntries.length) return false;
  return bEntries.every(([key, value]) => a[key] === value);
}

export function isConflictError(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "status" in err &&
    (err as { status?: unknown }).status === 409
  );
}

export function sortedEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => a.name.localeCompare(b.name));
}

export function duplicateName(name: string, siblings: FileEntry[]): string {
  const taken = new Set(siblings.map((entry) => entry.name));
  const dot = name.lastIndexOf(".");
  const hasExt = dot > 0;
  const stem = hasExt ? name.slice(0, dot) : name;
  const ext = hasExt ? name.slice(dot) : "";
  let candidate = `${stem} copy${ext}`;
  let n = 2;
  while (taken.has(candidate)) {
    candidate = `${stem} copy ${n}${ext}`;
    n += 1;
  }
  return candidate;
}

export function resolveMoveToTarget(
  fromPath: string,
  targetFolderPath: string,
): { from: string; to: string } | null {
  if (targetFolderPath === "") {
    const to = basename(fromPath);
    return to && to !== fromPath ? { from: fromPath, to } : null;
  }
  return resolveTreeDropMove(fromPath, targetFolderPath);
}
