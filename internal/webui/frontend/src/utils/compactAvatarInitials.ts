const NAME_SEGMENT_SPLIT = /[-_.\s/]+/;

/**
 * Derive up to two uppercase initials for compact avatar pills.
 * Uses the first character of the first two name segments when split on
 * common separators; otherwise uses the first two alphanumeric characters.
 */
export function getCompactAvatarInitials(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";

  const segments = trimmed
    .split(NAME_SEGMENT_SPLIT)
    .map((segment) => segment.replace(/[^a-zA-Z0-9]/g, ""))
    .filter((segment) => segment.length > 0);

  if (segments.length >= 2) {
    const first = segments[0]![0] ?? "";
    const second = segments[1]![0] ?? "";
    const initials = `${first}${second}`.toUpperCase();
    return initials || "?";
  }

  const single = segments[0] ?? trimmed.replace(/[^a-zA-Z0-9]/g, "");
  if (!single) return "?";

  return single.slice(0, 2).toUpperCase();
}
