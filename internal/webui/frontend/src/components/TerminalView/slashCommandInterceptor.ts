/**
 * Slash command interceptor for terminal views.
 *
 * Intercepts input data to detect and handle slash commands (e.g., /help, /status).
 * Properly handles ANSI escape sequences using a state-machine CSI parser that
 * consumes variable-length sequences (not just hardcoded 2-byte skip).
 *
 * State machine states for escape sequence parsing:
 * - 'none': Normal character processing
 * - 'escaped': Received ESC byte, waiting for next byte to determine sequence type
 * - 'csi': Inside a CSI sequence (ESC [), consuming until final byte
 * - 'consumeOne': Inside a 2-byte escape (e.g., SS3 ESC O x), consuming one more byte
 */

/** Known slash commands and their handlers. */
export interface SlashCommandHandler {
  command: string;
  description: string;
  handler: (args: string) => void;
}

/** Escape sequence parser state. */
type EscapeState = "none" | "escaped" | "csi" | "consumeOne";

/**
 * Creates a slash command interceptor for terminal input.
 *
 * The interceptor monitors input for lines starting with '/' and captures
 * them as slash commands instead of passing them through to the terminal.
 *
 * @param handlers - Map of command names to their handlers
 * @param onUnknownCommand - Called when an unrecognized slash command is entered
 * @param onBufferChange - Called whenever the command buffer changes (for display)
 */
export function createSlashCommandInterceptor(options: {
  handlers: Map<string, SlashCommandHandler>;
  onUnknownCommand?: (command: string) => void;
  onBufferChange?: (buffer: string) => void;
}) {
  const { handlers, onUnknownCommand, onBufferChange } = options;

  let inCommandMode = false;
  let commandBuffer = "";
  let escapeState: EscapeState = "none";

  /**
   * Process a single character during command mode.
   * All characters in command mode are consumed (not passed through).
   */
  function handleCommandChar(ch: string): void {
    const code = ch.charCodeAt(0);

    // ---- Escape sequence state machine ----
    if (escapeState === "none") {
      if (code === 0x1b) {
        escapeState = "escaped";
        return;
      }
    } else if (escapeState === "escaped") {
      escapeState = ch === "[" ? "csi" : "consumeOne";
      return;
    } else if (escapeState === "csi") {
      if (code >= 0x40 && code <= 0x7e) {
        escapeState = "none";
      } else if (code < 0x20 || code > 0x3f) {
        escapeState = "none";
      }
      return;
    } else if (escapeState === "consumeOne") {
      escapeState = "none";
      return;
    }

    // ---- Normal command character processing ----
    if (ch === "\r" || ch === "\n") {
      executeCommand();
    } else if (code === 0x7f || code === 0x08) {
      if (commandBuffer.length > 1) {
        commandBuffer = commandBuffer.slice(0, -1);
        onBufferChange?.(commandBuffer);
      } else {
        exitCommandMode();
      }
    } else if (code === 0x03) {
      exitCommandMode();
    } else if (code >= 0x20 && code <= 0x7e) {
      commandBuffer += ch;
      onBufferChange?.(commandBuffer);
    }
  }

  /** Execute the current command buffer. */
  function executeCommand() {
    const raw = commandBuffer.slice(1).trim(); // Remove leading '/'
    const spaceIdx = raw.indexOf(" ");
    const cmd = spaceIdx === -1 ? raw : raw.slice(0, spaceIdx);
    const args = spaceIdx === -1 ? "" : raw.slice(spaceIdx + 1).trim();

    const handlerEntry = handlers.get(cmd.toLowerCase());
    if (handlerEntry) {
      handlerEntry.handler(args);
    } else if (cmd) {
      onUnknownCommand?.(cmd);
    }

    exitCommandMode();
  }

  /** Exit command mode and reset state. */
  function exitCommandMode() {
    inCommandMode = false;
    commandBuffer = "";
    escapeState = "none";
    onBufferChange?.("");
  }

  /**
   * Handle incoming terminal data. Call this with each chunk of data.
   * Returns the portion of data that should be passed through to the terminal
   * (i.e., characters NOT consumed by command mode).
   */
  function handleData(data: string): string {
    let passthrough = "";

    for (let i = 0; i < data.length; i++) {
      const ch = data[i];

      if (inCommandMode) {
        handleCommandChar(ch);
      } else if (ch === "/" && commandBuffer === "") {
        // Entering command mode
        inCommandMode = true;
        commandBuffer = "/";
        escapeState = "none";
        onBufferChange?.(commandBuffer);
      } else {
        passthrough += ch;
      }
    }

    return passthrough;
  }

  /** Dispose the interceptor, resetting all state. */
  function dispose() {
    inCommandMode = false;
    commandBuffer = "";
    escapeState = "none";
  }

  return {
    handleData,
    dispose,
    /** Whether currently in command mode (for testing/display). */
    get isInCommandMode() {
      return inCommandMode;
    },
    /** Current command buffer contents (for testing/display). */
    get buffer() {
      return commandBuffer;
    },
  };
}
