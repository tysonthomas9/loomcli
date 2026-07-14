export type GitDecorationKind = "modified" | "added" | "deleted" | "conflict";

export interface GitDecoration {
  kind: GitDecorationKind;
  conflict: boolean;
}

export interface FolderGitDecoration {
  changed: boolean;
  conflict: boolean;
}

export interface TreeDropMove {
  from: string;
  to: string;
}

const conflictCodes = new Set(["DD", "AU", "UD", "UA", "DU", "AA", "UU"]);

function cleanPath(path: string): string {
  return path.replace(/^\/+|\/+$/g, "");
}

function basename(path: string): string {
  return cleanPath(path).split("/").pop() ?? cleanPath(path);
}

function joinPath(parent: string, child: string): string {
  const cleanParent = cleanPath(parent);
  const cleanChild = cleanPath(child);
  return cleanParent ? `${cleanParent}/${cleanChild}` : cleanChild;
}

function parentPaths(path: string): string[] {
  const segments = cleanPath(path).split("/").filter(Boolean);
  const parents: string[] = [];
  for (let i = 1; i < segments.length; i += 1) {
    parents.push(segments.slice(0, i).join("/"));
  }
  return parents;
}

export function isConflictStatus(xy: string | undefined): boolean {
  if (!xy || xy.length < 2) return false;
  return conflictCodes.has(xy.slice(0, 2));
}

export function gitDecorationForStatus(
  xy: string | undefined,
): GitDecoration | null {
  if (!xy || xy.length < 2) return null;
  const code = xy.slice(0, 2);
  if (isConflictStatus(code)) {
    return { kind: "conflict", conflict: true };
  }
  if (code === "??" || code.includes("A")) {
    return { kind: "added", conflict: false };
  }
  if (code.includes("D")) {
    return { kind: "deleted", conflict: false };
  }
  if (code.trim().length > 0) {
    return { kind: "modified", conflict: false };
  }
  return null;
}

export function buildFolderGitDecorations(
  statuses: Record<string, string>,
): Map<string, FolderGitDecoration> {
  const folders = new Map<string, FolderGitDecoration>();
  for (const [path, xy] of Object.entries(statuses)) {
    const decoration = gitDecorationForStatus(xy);
    if (!decoration) continue;
    for (const parent of parentPaths(path)) {
      const current = folders.get(parent) ?? {
        changed: false,
        conflict: false,
      };
      current.changed = true;
      current.conflict = current.conflict || decoration.conflict;
      folders.set(parent, current);
    }
  }
  return folders;
}

export function resolveTreeDropMove(
  sourcePath: string,
  targetFolderPath: string,
): TreeDropMove | null {
  const from = cleanPath(sourcePath);
  const folder = cleanPath(targetFolderPath);
  if (!from || !folder) return null;
  if (from === folder || folder.startsWith(`${from}/`)) return null;
  const to = joinPath(folder, basename(from));
  if (!to || to === from) return null;
  return { from, to };
}
