/** Check only opaque wire framing. Fleet owns the payload's identity/position schema. */
export function isRecoveryEnvelope(
  value: unknown,
  prefix: "s1." | "c2.",
): value is string {
  if (
    typeof value !== "string" ||
    value.length > 1024 ||
    !value.startsWith(prefix)
  )
    return false;
  const payload = value.slice(prefix.length);
  if (!/^[A-Za-z0-9_-]+$/.test(payload)) return false;
  try {
    const decoded = atob(
      payload.replace(/-/g, "+").replace(/_/g, "/") +
        "=".repeat((4 - (payload.length % 4)) % 4),
    );
    return (
      decoded.length > 0 &&
      btoa(decoded)
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "") === payload
    );
  } catch {
    return false;
  }
}
