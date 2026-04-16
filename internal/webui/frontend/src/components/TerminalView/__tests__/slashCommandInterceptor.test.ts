/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SlashCommandInterceptor — terminal input stream interception.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import { SlashCommandInterceptor } from "../slashCommandInterceptor";

// Mock the slashCommands module
vi.mock("../slashCommands", () => {
  const formatSystemMessage = vi.fn(
    (text: string, type: string) => `[${type}] ${text}`,
  );
  const parseSlashCommand = vi.fn((line: string) => {
    const trimmed = line.trim();
    if (!trimmed.startsWith("/")) return null;
    const spaceIdx = trimmed.indexOf(" ");
    if (spaceIdx === -1) return { name: trimmed.slice(1), args: "" };
    return {
      name: trimmed.slice(1, spaceIdx),
      args: trimmed.slice(spaceIdx + 1).trim(),
    };
  });

  const helpExecute = vi.fn(async () => "[info] Available commands:");
  const statusExecute = vi.fn(async () => "[info] Project Status:");

  const COMMAND_REGISTRY = new Map([
    [
      "help",
      {
        name: "help",
        description: "Show available commands",
        usage: "/help [command]",
        execute: helpExecute,
      },
    ],
    [
      "status",
      {
        name: "status",
        description: "Show project status",
        usage: "/status [issue-id]",
        execute: statusExecute,
      },
    ],
  ]);

  return { formatSystemMessage, parseSlashCommand, COMMAND_REGISTRY };
});

import { COMMAND_REGISTRY, formatSystemMessage } from "../slashCommands";

const mockedFormatSystemMessage = vi.mocked(formatSystemMessage);

/**
 * Minimal fake terminal with a write spy.
 */
function createFakeTerminal() {
  return { write: vi.fn() };
}

describe("SlashCommandInterceptor", () => {
  let terminal: ReturnType<typeof createFakeTerminal>;
  let interceptor: SlashCommandInterceptor;
  let sendToWs: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.clearAllMocks();
    terminal = createFakeTerminal();
    interceptor = new SlashCommandInterceptor(
      (data) => terminal.write(data),
      "test-ws",
    );
    sendToWs = vi.fn();
  });

  // ============= Command mode entry =============

  describe("command mode entry", () => {
    it("enters command mode when first character is /", () => {
      interceptor.handleData("/", sendToWs);

      // The '/' is echoed to the terminal
      expect(terminal.write as any).toHaveBeenCalledWith("/");
      // Nothing sent to WebSocket
      expect(sendToWs).not.toHaveBeenCalled();
    });

    it("accumulates characters in buffer when first char is /", () => {
      // Type '/he' character by character
      interceptor.handleData("/", sendToWs);
      interceptor.handleData("h", sendToWs);
      interceptor.handleData("e", sendToWs);

      // Each character is echoed
      expect(terminal.write as any).toHaveBeenCalledWith("/");
      expect(terminal.write as any).toHaveBeenCalledWith("h");
      expect(terminal.write as any).toHaveBeenCalledWith("e");
      // Nothing sent to WebSocket
      expect(sendToWs).not.toHaveBeenCalled();
    });

    it("handles multi-character data starting with /", () => {
      interceptor.handleData("/help", sendToWs);

      // Each character should be echoed individually
      expect(terminal.write as any).toHaveBeenCalledWith("/");
      expect(terminal.write as any).toHaveBeenCalledWith("h");
      expect(terminal.write as any).toHaveBeenCalledWith("e");
      expect(terminal.write as any).toHaveBeenCalledWith("l");
      expect(terminal.write as any).toHaveBeenCalledWith("p");
      expect(sendToWs).not.toHaveBeenCalled();
    });
  });

  // ============= Pass-through for non-slash input =============

  describe("non-slash passthrough", () => {
    it("passes data through to sendToWs immediately when first char is not /", () => {
      interceptor.handleData("ls -la", sendToWs);

      expect(sendToWs).toHaveBeenCalledWith("ls -la");
      expect(terminal.write as any).not.toHaveBeenCalled();
    });

    it("passes single characters through when not in command mode", () => {
      interceptor.handleData("a", sendToWs);

      expect(sendToWs).toHaveBeenCalledWith("a");
    });

    it("passes enter key through when not in command mode", () => {
      interceptor.handleData("\r", sendToWs);

      expect(sendToWs).toHaveBeenCalledWith("\r");
    });
  });

  // ============= Backspace handling =============

  describe("backspace", () => {
    it("removes last character from buffer and writes \\b \\b to terminal", () => {
      interceptor.handleData("/he", sendToWs);
      (terminal.write as any).mockClear();

      // Send backspace (DEL = 0x7f)
      interceptor.handleData("\x7f", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("\b \b");
      expect(sendToWs).not.toHaveBeenCalled();
    });

    it("exits command mode when backspacing past / (empty buffer)", () => {
      // Type '/' then backspace it
      interceptor.handleData("/", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\x7f", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("\b \b");

      // Now subsequent input should go to sendToWs (back to normal mode)
      interceptor.handleData("x", sendToWs);
      expect(sendToWs).toHaveBeenCalledWith("x");
    });

    it("handles BS character (0x08) the same as DEL", () => {
      interceptor.handleData("/ab", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\b", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("\b \b");
    });

    it("does nothing when backspacing on empty buffer in command mode", () => {
      // This shouldn't happen in practice since empty buffer exits command mode,
      // but if somehow we're in command mode with empty buffer, backspace is ignored.
      // After '/':
      interceptor.handleData("/", sendToWs);
      // Buffer is '/'
      interceptor.handleData("\x7f", sendToWs);
      // Buffer is empty, command mode exited
      // Another backspace should pass through (we're in normal mode now)
      (terminal.write as any).mockClear();
      sendToWs.mockClear();

      interceptor.handleData("\x7f", sendToWs);
      // In normal mode, it passes through
      expect(sendToWs).toHaveBeenCalledWith("\x7f");
    });
  });

  // ============= Ctrl+C handling =============

  describe("Ctrl+C", () => {
    it("clears buffer and exits command mode", () => {
      interceptor.handleData("/status", sendToWs);
      (terminal.write as any).mockClear();

      // Send Ctrl+C
      interceptor.handleData("\x03", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("^C\r\n");

      // Subsequent input should pass through (exited command mode)
      interceptor.handleData("x", sendToWs);
      expect(sendToWs).toHaveBeenCalledWith("x");
    });

    it("writes ^C\\r\\n to the terminal", () => {
      interceptor.handleData("/h", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\x03", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("^C\r\n");
    });
  });

  // ============= Enter / command execution =============

  describe("enter triggers command execution", () => {
    it("executes a recognized command and writes the result", async () => {
      interceptor.handleData("/help", sendToWs);
      (terminal.write as any).mockClear();
      sendToWs.mockClear();

      // Press Enter
      interceptor.handleData("\r", sendToWs);

      // Wait for the async command execution
      await vi.waitFor(() => {
        expect(terminal.write as any).toHaveBeenCalledWith(
          expect.stringContaining("[info] Available commands:"),
        );
      });

      // After execution, a bare \r is sent to get a fresh shell prompt
      expect(sendToWs).toHaveBeenCalledWith("\r");
    });

    it("writes \\r\\n to terminal when Enter is pressed", async () => {
      interceptor.handleData("/help", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\r", sendToWs);

      // The first write should be the newline
      expect(terminal.write as any).toHaveBeenCalledWith("\r\n");

      await vi.waitFor(() => {
        expect(sendToWs).toHaveBeenCalled();
      });
    });

    it("shows error for unknown slash commands", async () => {
      mockedFormatSystemMessage.mockReturnValue("[error] Unknown command");

      interceptor.handleData("/foobar", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\r", sendToWs);

      await vi.waitFor(() => {
        expect(terminal.write as any).toHaveBeenCalledWith(
          expect.stringContaining("Unknown command"),
        );
      });
    });

    it("shows error message when command handler throws", async () => {
      const statusCmd = COMMAND_REGISTRY.get("status")!;
      (statusCmd.execute as any).mockRejectedValueOnce(
        new Error("API failure"),
      );
      mockedFormatSystemMessage.mockReturnValue("[error] API failure");

      interceptor.handleData("/status", sendToWs);
      (terminal.write as any).mockClear();

      interceptor.handleData("\r", sendToWs);

      await vi.waitFor(() => {
        expect(mockedFormatSystemMessage).toHaveBeenCalledWith(
          "API failure",
          "error",
        );
      });
    });
  });

  // ============= dispose =============

  describe("dispose", () => {
    it("resets all state so subsequent input passes through", () => {
      // Enter command mode
      interceptor.handleData("/stat", sendToWs);
      expect(sendToWs).not.toHaveBeenCalled();

      // Dispose
      interceptor.dispose();

      // After dispose, input should pass through since state is reset
      interceptor.handleData("x", sendToWs);
      expect(sendToWs).toHaveBeenCalledWith("x");
    });

    it("can be called multiple times without error", () => {
      interceptor.dispose();
      interceptor.dispose();
      // No error thrown
    });
  });

  // ============= Escape sequences =============

  describe("escape sequences in command mode", () => {
    it("silently ignores a full CSI escape sequence (ESC + 2 trailing bytes)", () => {
      interceptor.handleData("/he", sendToWs);
      (terminal.write as any).mockClear();

      // Send a full CSI escape sequence: ESC [ A (3 bytes total)
      interceptor.handleData("\x1b", sendToWs);
      interceptor.handleData("[", sendToWs);
      interceptor.handleData("A", sendToWs);

      // Nothing written to terminal, nothing sent to ws
      expect(terminal.write as any).not.toHaveBeenCalled();
      expect(sendToWs).not.toHaveBeenCalled();
    });

    it("ignores escape and its trailing bytes, then continues to accept normal input", async () => {
      interceptor.handleData("/hel", sendToWs);
      (terminal.write as any).mockClear();

      // Send a full CSI escape sequence: ESC [ A (arrow up, 3 bytes)
      // ESC sets escapeSeqRemaining=2, so '[' and 'A' are also consumed
      interceptor.handleData("\x1b", sendToWs);
      interceptor.handleData("[", sendToWs);
      interceptor.handleData("A", sendToWs);

      // Nothing should be written to terminal for escape bytes
      expect(terminal.write as any).not.toHaveBeenCalled();
      expect(sendToWs).not.toHaveBeenCalled();

      // Continue typing normally after the escape sequence
      interceptor.handleData("p", sendToWs);

      expect(terminal.write as any).toHaveBeenCalledWith("p");
      expect(sendToWs).not.toHaveBeenCalled();

      // Execute: the buffer should be /help
      interceptor.handleData("\r", sendToWs);

      await vi.waitFor(() => {
        expect(terminal.write as any).toHaveBeenCalledWith(
          expect.stringContaining("[info] Available commands:"),
        );
      });
    });

    it("ignores multiple complete escape sequences", () => {
      interceptor.handleData("/a", sendToWs);
      (terminal.write as any).mockClear();

      // Send two full CSI escape sequences (ESC + 2 trailing bytes each)
      // Sequence 1: ESC [ B (arrow down)
      interceptor.handleData("\x1b", sendToWs);
      interceptor.handleData("[", sendToWs);
      interceptor.handleData("B", sendToWs);
      // Sequence 2: ESC [ C (arrow right)
      interceptor.handleData("\x1b", sendToWs);
      interceptor.handleData("[", sendToWs);
      interceptor.handleData("C", sendToWs);

      expect(terminal.write as any).not.toHaveBeenCalled();
      expect(sendToWs).not.toHaveBeenCalled();
    });
  });

  // ============= Blocking during execution =============

  describe("input blocking during execution", () => {
    it("blocks input while a command is executing", async () => {
      let resolveExec: (value: string) => void;
      const statusCmd = COMMAND_REGISTRY.get("status")!;
      (statusCmd.execute as any).mockImplementationOnce(
        () =>
          new Promise<string>((resolve) => {
            resolveExec = resolve;
          }),
      );

      interceptor.handleData("/status", sendToWs);
      interceptor.handleData("\r", sendToWs);

      // While executing, new input is blocked
      sendToWs.mockClear();
      interceptor.handleData("x", sendToWs);
      expect(sendToWs).not.toHaveBeenCalled();

      // Resolve the command
      resolveExec!("[info] Done");

      await vi.waitFor(() => {
        // After execution completes, sendToWs should have been called with "\r"
        expect(sendToWs).toHaveBeenCalledWith("\r");
      });

      // Now input should work again
      sendToWs.mockClear();
      interceptor.handleData("y", sendToWs);
      expect(sendToWs).toHaveBeenCalledWith("y");
    });
  });
});
