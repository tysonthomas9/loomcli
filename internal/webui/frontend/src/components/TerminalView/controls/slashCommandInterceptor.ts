/**
 * SlashCommandInterceptor — plain TypeScript class for intercepting
 * slash commands in the terminal input stream.
 *
 * When the first character typed is '/', the interceptor enters "command mode":
 * keystrokes are locally echoed to the terminal and buffered (not sent to the
 * WebSocket). On Enter, the buffer is parsed and executed.
 *
 * If the first character is not '/', all data passes through to the WebSocket
 * immediately with zero overhead.
 */

import type { Terminal } from "@xterm/xterm";

import {
  COMMAND_REGISTRY,
  formatSystemMessage,
  parseSlashCommand,
} from "./slashCommands";

export class SlashCommandInterceptor {
  private buffer = "";
  private commandMode = false;
  private executing = false;
  private disposed = false;
  private escapeState: "none" | "escaped" | "csi" | "consumeOne" = "none";
  private terminal: Terminal;
  private workspaceId: string;

  constructor(terminal: Terminal, workspaceId: string) {
    this.terminal = terminal;
    this.workspaceId = workspaceId;
  }

  /**
   * Handle incoming terminal data. Called from the onInput callback.
   * @param data - Raw input data from xterm onData
   * @param sendToWs - Function to send data to the WebSocket
   */
  handleData(data: string, sendToWs: (data: string) => void): void {
    // If currently executing a command, block new input
    if (this.executing) {
      return;
    }

    // Check first character for command mode entry
    if (this.buffer === "" && !this.commandMode) {
      if (data.length > 0 && data[0] === "/") {
        this.commandMode = true;
        this.buffer = "";
        // Process each character
        for (const ch of data) {
          this.handleCommandChar(ch, sendToWs);
        }
        return;
      }
      // Not a slash command — pass through
      sendToWs(data);
      return;
    }

    if (this.commandMode) {
      for (const ch of data) {
        this.handleCommandChar(ch, sendToWs);
      }
      return;
    }

    // Not in command mode — pass through
    sendToWs(data);
  }

  private handleCommandChar(
    ch: string,
    sendToWs: (data: string) => void,
  ): void {
    // State-machine CSI parser: consume escape sequences of any length
    if (this.escapeState !== "none") {
      if (this.escapeState === "escaped") {
        // Second byte after ESC
        if (ch === "[") {
          // CSI sequence: ESC [ <params> <final>
          this.escapeState = "csi";
        } else {
          // SS3 (ESC O x) or other 2-byte (ESC x y): consume one more byte
          this.escapeState = "consumeOne";
        }
        return;
      }
      if (this.escapeState === "csi") {
        if (ch === "\x1b") {
          // Abort current CSI, begin new escape sequence
          this.escapeState = "escaped";
          return;
        }
        const code = ch.charCodeAt(0);
        if (code >= 0x40 && code <= 0x7e) {
          // Final byte — sequence complete
          this.escapeState = "none";
        }
        // Parameter bytes (0x30–0x3F) and intermediate bytes (0x20–0x2F)
        // stay in 'csi' state.
        return;
      }
      if (this.escapeState === "consumeOne") {
        // Consume the last byte of a 3-byte sequence (e.g., SS3)
        this.escapeState = "none";
        return;
      }
    }

    // Detect escape sequence start
    if (ch === "\x1b") {
      this.escapeState = "escaped";
      return;
    }

    // Ctrl+C — cancel command mode
    if (ch === "\x03") {
      this.terminal.write("^C\r\n");
      this.exitCommandMode();
      return;
    }

    // Backspace / Delete
    if (ch === "\x7f" || ch === "\b") {
      if (this.buffer.length > 0) {
        this.buffer = this.buffer.slice(0, -1);
        // Erase character on screen
        this.terminal.write("\b \b");
        // If we backspaced past everything, exit command mode
        if (this.buffer.length === 0) {
          this.exitCommandMode();
        }
      }
      return;
    }

    // Enter — execute command
    if (ch === "\r" || ch === "\n") {
      this.terminal.write("\r\n");
      this.executeCommand(sendToWs);
      return;
    }

    // Printable character — buffer and echo
    this.buffer += ch;
    this.terminal.write(ch);
  }

  private async executeCommand(
    sendToWs: (data: string) => void,
  ): Promise<void> {
    const line = this.buffer;
    this.executing = true;
    this.exitCommandMode();

    try {
      const parsed = parseSlashCommand(line);
      if (!parsed) {
        // Should not happen (buffer starts with '/'), but handle gracefully
        if (!this.disposed) {
          this.terminal.write(
            formatSystemMessage("Invalid command.", "error") + "\r\n",
          );
        }
      } else {
        const cmd = COMMAND_REGISTRY.get(parsed.name);
        if (!cmd) {
          if (!this.disposed) {
            this.terminal.write(
              formatSystemMessage(
                `Unknown command '/${parsed.name}'. Type /help for available commands.`,
                "error",
              ) + "\r\n",
            );
          }
        } else {
          try {
            const result = await cmd.execute(parsed.args, this.workspaceId);
            if (!this.disposed) {
              this.terminal.write(result + "\r\n");
            }
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : "Command failed";
            if (!this.disposed) {
              this.terminal.write(formatSystemMessage(msg, "error") + "\r\n");
            }
          }
        }
      }

      // Send bare carriage return to get a fresh shell prompt
      if (!this.disposed && sendToWs) {
        sendToWs("\r");
      }
    } finally {
      this.executing = false;
    }
  }

  private exitCommandMode(): void {
    this.buffer = "";
    this.commandMode = false;
    this.escapeState = "none";
  }

  /**
   * Cleanup — reset all internal state.
   */
  dispose(): void {
    this.disposed = true;
    this.buffer = "";
    this.commandMode = false;
    this.executing = false;
    this.escapeState = "none";
  }
}
