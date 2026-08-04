/** Matches fleet-db agent name rules (lowercase identifiers). */
export const STORED_AGENT_NAME_RE = /^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$/;

export function normalizeStoredAgentName(name: string): string {
  return name.trim().toLowerCase();
}

export function validateStoredAgentName(name: string): string | null {
  const normalized = normalizeStoredAgentName(name);
  if (!normalized) {
    return "Agent name is required";
  }
  if (!STORED_AGENT_NAME_RE.test(normalized)) {
    return "Use 1–100 lowercase letters, numbers, hyphens, dots, or underscores. Names cannot start or end with punctuation.";
  }
  return null;
}
