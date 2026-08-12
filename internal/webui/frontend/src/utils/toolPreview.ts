/**
 * Shared preview helpers for collapsed tool-call pills. Both the run transcript
 * (which holds tool input as a parsed value) and the PR reviewer chat (which
 * receives it pre-serialized) label a pill with the same salient argument.
 */

/** Max characters of an arg preview before it is elided. */
const PREVIEW_MAX = 60;

/**
 * Input keys worth showing on a collapsed pill, most specific first — the thing
 * a tool call is "about" (which file, which command, which query).
 */
const PREVIEW_KEYS = [
  "file_path",
  "filePath",
  "path",
  "notebook_path",
  "url",
  "pattern",
  "command",
  "query",
  "skill",
] as const;

export function truncate(value: string, max: number = PREVIEW_MAX): string {
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
}

/** Short preview of the most salient tool-input arg (path, command, etc.). */
export function argPreview(input: unknown): string {
  if (input == null) return "";
  if (typeof input === "string") return truncate(input);
  if (typeof input !== "object") return "";
  const rec = input as Record<string, unknown>;
  for (const key of PREVIEW_KEYS) {
    const v = rec[key];
    if (typeof v === "string" && v) return truncate(v);
  }
  return "";
}

/**
 * argPreview for a tool input that arrives as a serialized string: parses it
 * when it looks like JSON, and otherwise (or when no salient key is present)
 * previews the raw text with whitespace collapsed.
 */
export function argPreviewFromJSON(input: string | undefined): string {
  const raw = (input ?? "").trim();
  if (!raw) return "";
  const rawPreview = (): string => truncate(raw.replace(/\s+/g, " "));
  if (!raw.startsWith("{") && !raw.startsWith("[")) return rawPreview();
  try {
    const parsed: unknown = JSON.parse(raw);
    return argPreview(parsed) || rawPreview();
  } catch {
    return rawPreview();
  }
}
