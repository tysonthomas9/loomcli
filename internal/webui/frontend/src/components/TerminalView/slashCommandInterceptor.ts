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

import {
  COMMAND_REGISTRY,
  formatSystemMessage,
  parseSlashCommand,
} from "./slashCommands";

export type WriteFn = (data: string) => void;

export class SlashCommandInterceptor {
  private buffer = "";
  private commandMode = false;
  private executing = false;
  private disposed = false;
  private escapeSeqRemaining = 0;
  private write: WriteFn;
  private workspaceId: string;

  constructor(write: WriteFn, workspaceId: string) {
    this.write = write;
    this.workspaceId = workspaceId;
  }

  /**
   * Handle incoming terminal data. Called from the onInput callback.
   * @param data - Raw input data from the terminal's onData
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
    // Skip remaining bytes of an escape sequence
    if (this.escapeSeqRemaining > 0) {
      this.escapeSeqRemaining--;
      return;
    }

    // Detect escape sequence start and skip the full sequence
    // CSI sequences: ESC [ ... <letter> (typically 2 more bytes, e.g. \x1b[A)
    // SS3 sequences: ESC O <letter> (2 more bytes)
    if (ch === "\x1b") {
      this.escapeSeqRemaining = 2;
      return;
    }

    // Ctrl+C — cancel command mode
    if (ch === "\x03") {
      this.write("^C\r\n");
      this.exitCommandMode();
      return;
    }

    // Backspace / Delete
    if (ch === "\x7f" || ch === "\b") {
      if (this.buffer.length > 0) {
        this.buffer = this.buffer.slice(0, -1);
        // Erase character on screen
        this.write("\b \b");
        // If we backspaced past everything, exit command mode
        if (this.buffer.length === 0) {
          this.exitCommandMode();
        }
      }
      return;
    }

    // Enter — execute command
    if (ch === "\r" || ch === "\n") {
      this.write("\r\n");
      this.executeCommand(sendToWs);
      return;
    }

    // Printable character — buffer and echo
    this.buffer += ch;
    this.write(ch);
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
          this.write(
            formatSystemMessage("Invalid command.", "error") + "\r\n",
          );
        }
      } else {
        const cmd = COMMAND_REGISTRY.get(parsed.name);
        if (!cmd) {
          if (!this.disposed) {
            this.write(
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
              this.write(result + "\r\n");
            }
          } catch (err: unknown) {
            const msg = err instanceof Error ? err.message : "Command failed";
            if (!this.disposed) {
              this.write(formatSystemMessage(msg, "error") + "\r\n");
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
  }

  /**
   * Cleanup — reset all internal state.
   */
  dispose(): void {
    this.disposed = true;
    this.buffer = "";
    this.commandMode = false;
    this.executing = false;
    this.escapeSeqRemaining = 0;
  }
}
