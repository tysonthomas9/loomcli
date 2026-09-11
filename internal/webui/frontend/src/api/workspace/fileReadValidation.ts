import type { DirListData, FileReadData } from "./files";

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Scope-relative paths are sent with POSIX separators; the server cleans dot
// and empty components. Traversal components remain invalid on the server.
function cleanPath(path = ""): string {
  return (
    path
      .split("/")
      .filter((part) => part && part !== ".")
      .join("/") || "."
  );
}

export function validateDirectoryRead(
  data: unknown,
  path?: string,
): asserts data is DirListData {
  if (
    !record(data) ||
    data.path !== cleanPath(path) ||
    !Array.isArray(data.entries)
  )
    throw new Error("Invalid directory response");
  const names = new Set<string>();
  for (const entry of data.entries) {
    if (
      !record(entry) ||
      typeof entry.name !== "string" ||
      !entry.name ||
      entry.name === "." ||
      entry.name === ".." ||
      entry.name.includes("/") ||
      entry.name.includes("\0") ||
      names.has(entry.name) ||
      typeof entry.is_dir !== "boolean" ||
      !Number.isSafeInteger(entry.size) ||
      (entry.size as number) < 0 ||
      typeof entry.mod_time !== "string" ||
      !Number.isFinite(Date.parse(entry.mod_time))
    )
      throw new Error("Invalid directory entry");
    names.add(entry.name);
  }
}

export function validateFileRead(
  data: unknown,
  path: string,
  revisionPreview = false,
): asserts data is FileReadData {
  if (
    !record(data) ||
    data.path !== cleanPath(path) ||
    !Number.isSafeInteger(data.size) ||
    (data.size as number) < 0 ||
    typeof data.binary !== "boolean" ||
    typeof data.truncated !== "boolean" ||
    typeof data.version !== "string" ||
    (!revisionPreview && !data.version) ||
    (!data.binary && typeof data.content !== "string") ||
    (data.content !== undefined && typeof data.content !== "string")
  )
    throw new Error("Invalid file read response");
}
