/**
 * Terminal WebSocket control frames.
 *
 * The server sends PTY output exclusively as *binary* frames, so a *text*
 * frame on the terminal socket is unambiguously a control message: a single
 * JSON object carrying a `type` discriminator.
 *
 * Parsing lives in its own module so the parse-or-fall-through rule — a text
 * frame that is not one of ours must reach xterm untouched — is testable
 * without a WebSocket.
 */

/**
 * Sent once per attach, after the scrollback replay and before live output.
 *
 * `replaced` is true only when *this* attach is the replacement: the tab's
 * metadata outlived a server restart and the shell behind it was respawned.
 * A later client attaching to that same shell gets `reattached: true` with
 * the stored `replaced_at`, so it learns the fact without a REST round-trip
 * but must not redraw the boundary.
 */
export interface TerminalAttachFrame {
  type: "attach";
  reattached: boolean;
  replaced: boolean;
  /** RFC3339 timestamp of the replacement; absent when never replaced. */
  replaced_at?: string;
  /** Server-written enum, e.g. "server_restart". */
  replaced_reason?: string;
}

export type TerminalControlFrame = TerminalAttachFrame;

/** Frame types this client understands. Anything else is not a control frame. */
const KNOWN_TYPES = new Set(["attach"]);

/**
 * Parse a text WebSocket frame as a control message.
 *
 * Returns null — never throws — unless the text parses as a JSON object *and*
 * carries a `type` in the known set. Everything else (raw PTY text, JSON-ish
 * program output, an envelope from a newer server) falls through to the
 * caller, which writes it to the terminal as it always has.
 */
export function parseTerminalControlFrame(
  raw: string,
): TerminalControlFrame | null {
  // Cheap reject before JSON.parse: PTY output is not a JSON object, and this
  // runs on every text frame.
  const trimmed = raw.trim();
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) return null;

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return null;
  }

  const frame = parsed as Record<string, unknown>;
  if (typeof frame.type !== "string" || !KNOWN_TYPES.has(frame.type)) {
    return null;
  }

  const attach: TerminalAttachFrame = {
    type: "attach",
    reattached: frame.reattached === true,
    replaced: frame.replaced === true,
  };
  if (typeof frame.replaced_at === "string" && frame.replaced_at !== "") {
    attach.replaced_at = frame.replaced_at;
  }
  if (
    typeof frame.replaced_reason === "string" &&
    frame.replaced_reason !== ""
  ) {
    attach.replaced_reason = frame.replaced_reason;
  }
  return attach;
}
