export interface PresentedErrorMessage {
  message: string;
  raw: string;
}

const TECHNICAL_ERROR_SEGMENTS = [
  /^api error\b/i,
  /^create agent\b/i,
  /^domain$/i,
  /^fleetdb$/i,
  /^http\s+\d{3}\b/i,
  /^(get|post|put|patch|delete)\s+\/\S+/i,
];

function sentenceCase(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return trimmed;
  const firstLetter = trimmed.search(/[A-Za-z]/);
  if (firstLetter < 0) return trimmed;
  const letter = trimmed.charAt(firstLetter);
  return (
    trimmed.slice(0, firstLetter) +
    letter.toUpperCase() +
    trimmed.slice(firstLetter + 1)
  );
}

function isTechnicalSegment(segment: string): boolean {
  return TECHNICAL_ERROR_SEGMENTS.some((pattern) => pattern.test(segment));
}

function cleanServerTail(raw: string): string {
  const segments = raw
    .split(":")
    .map((part) => part.trim())
    .filter(Boolean);
  if (segments.length === 0) return raw.trim();

  const meaningful = segments.filter((segment) => !isTechnicalSegment(segment));
  const candidates = meaningful.length > 0 ? meaningful : segments;
  for (let i = candidates.length - 1; i >= 0; i -= 1) {
    const candidate = candidates[i];
    if (!candidate) continue;
    if (i > 0 && candidate.toLowerCase() === candidates[i - 1]?.toLowerCase()) {
      continue;
    }
    return candidate;
  }
  return candidates[candidates.length - 1] ?? raw.trim();
}

export function presentServerErrorMessage(
  rawMessage: string,
  status?: number,
): PresentedErrorMessage {
  const raw = rawMessage.trim() || "Failed to create agent";
  const lower = raw.toLowerCase();

  if (lower.includes("workspace has no repos for agent")) {
    return {
      raw,
      message:
        "This workspace has no repos yet — add one from the sidebar first.",
    };
  }

  const looksLikeConflict =
    status === 409 ||
    /\bhttp\s+409\b/i.test(raw) ||
    /\bapi error:\s*409\b/i.test(raw);
  if (looksLikeConflict && lower.includes("already exists")) {
    return {
      raw,
      message: "An agent with this name already exists.",
    };
  }

  return {
    raw,
    message: sentenceCase(cleanServerTail(raw)),
  };
}
