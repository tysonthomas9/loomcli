export interface ReplacementOptions {
  query: string;
  replacement: string;
  regex: boolean;
  caseSensitive: boolean;
}

export interface ReplacementPreview {
  path: string;
  before: string;
  after: string;
  diffLines: string[];
}

export function parseGlobText(value: string): string[] {
  return value
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function applyReplacement(
  content: string,
  options: ReplacementOptions,
): string {
  if (!options.query) return content;
  const flags = options.caseSensitive ? "g" : "gi";
  const pattern = options.regex
    ? new RegExp(options.query, flags)
    : new RegExp(escapeRegExp(options.query), flags);
  return content.replace(pattern, options.replacement);
}

export function buildDiffPreview(before: string, after: string): string[] {
  const beforeLines = before.split("\n");
  const afterLines = after.split("\n");
  const lines: string[] = [];
  const max = Math.max(beforeLines.length, afterLines.length);
  for (let i = 0; i < max && lines.length < 16; i += 1) {
    if (beforeLines[i] === afterLines[i]) continue;
    if (beforeLines[i] !== undefined) lines.push(`- ${beforeLines[i]}`);
    if (afterLines[i] !== undefined) lines.push(`+ ${afterLines[i]}`);
  }
  if (lines.length === 16) lines.push("...");
  return lines;
}

export function createReplacementPreview(
  path: string,
  before: string,
  options: ReplacementOptions,
): ReplacementPreview | null {
  const after = applyReplacement(before, options);
  if (after === before) return null;
  return {
    path,
    before,
    after,
    diffLines: buildDiffPreview(before, after),
  };
}
