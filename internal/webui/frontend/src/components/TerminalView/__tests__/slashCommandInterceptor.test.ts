/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";

import { createSlashCommandInterceptor } from "../slashCommandInterceptor";
import type { SlashCommandHandler } from "../slashCommandInterceptor";

describe("slashCommandInterceptor", () => {
  let handlers: Map<string, SlashCommandHandler>;
  let helpHandler: ReturnType<typeof vi.fn>;
  let statusHandler: ReturnType<typeof vi.fn>;
  let onUnknown: ReturnType<typeof vi.fn>;
  let onBufferChange: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    helpHandler = vi.fn();
    statusHandler = vi.fn();
    onUnknown = vi.fn();
    onBufferChange = vi.fn();

    handlers = new Map([
      [
        "help",
        { command: "help", description: "Show help", handler: helpHandler },
      ],
      [
        "status",
        {
          command: "status",
          description: "Show status",
          handler: statusHandler,
        },
      ],
    ]);
  });

  function createInterceptor() {
    return createSlashCommandInterceptor({
      handlers,
      onUnknownCommand: onUnknown,
      onBufferChange,
    });
  }

  describe("basic command handling", () => {
    it("detects slash command and executes handler on Enter", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/help\r");

      expect(helpHandler).toHaveBeenCalledTimes(1);
      expect(helpHandler).toHaveBeenCalledWith("");
    });

    it("passes command arguments to handler", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/status all\r");

      expect(statusHandler).toHaveBeenCalledWith("all");
    });

    it("calls onUnknownCommand for unrecognized commands", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/foobar\r");

      expect(onUnknown).toHaveBeenCalledWith("foobar");
    });

    it("passes non-command data through", () => {
      const interceptor = createInterceptor();

      const result = interceptor.handleData("hello world");

      expect(result).toBe("hello world");
    });

    it("updates buffer on each character", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/he");

      expect(onBufferChange).toHaveBeenCalledWith("/");
      expect(onBufferChange).toHaveBeenCalledWith("/h");
      expect(onBufferChange).toHaveBeenCalledWith("/he");
    });
  });

  describe("escape sequence handling — 3-byte ESC[A (arrow up)", () => {
    it("consumes 3-byte escape sequence without corrupting buffer", () => {
      const interceptor = createInterceptor();

      // Type /hel, then ESC[A (up arrow), then p
      interceptor.handleData("/hel");
      interceptor.handleData("\x1b[A");
      interceptor.handleData("p\r");

      expect(helpHandler).toHaveBeenCalledTimes(1);
      expect(interceptor.buffer).toBe("");
    });
  });

  describe("escape sequence handling — multi-byte CSI (Ctrl+Up)", () => {
    it("consumes ESC[1;5A (Ctrl+Up, 7 bytes) without corrupting buffer", () => {
      const interceptor = createInterceptor();

      // Type /hel, then ESC[1;5A (Ctrl+Up), then p
      interceptor.handleData("/hel");
      interceptor.handleData("\x1b[1;5A");
      interceptor.handleData("p\r");

      // Buffer should be /help, not /hel1;5p
      expect(helpHandler).toHaveBeenCalledTimes(1);
    });

    it("buffer is /help not /hel1;5p after Ctrl+Up", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel");
      interceptor.handleData("\x1b[1;5A");
      interceptor.handleData("p");

      expect(interceptor.buffer).toBe("/help");
    });
  });

  describe("escape sequence handling — 4-byte CSI (clear screen)", () => {
    it("consumes ESC[2J (4 bytes) without corrupting buffer", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/ab");
      interceptor.handleData("\x1b[2J");
      interceptor.handleData("c");

      expect(interceptor.buffer).toBe("/abc");
    });
  });

  describe("escape sequence handling — SS3 (F1 key)", () => {
    it("consumes ESC O P (SS3, 3 bytes) without corrupting buffer", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/ab");
      interceptor.handleData("\x1bOP");
      interceptor.handleData("c");

      expect(interceptor.buffer).toBe("/abc");
    });
  });

  describe("escape sequence handling — long SGR sequence", () => {
    it("consumes ESC[1;2;3m without corrupting buffer", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/x");
      interceptor.handleData("\x1b[1;2;3m");
      interceptor.handleData("y");

      expect(interceptor.buffer).toBe("/xy");
    });
  });

  describe("escape sequence handling — batched delivery", () => {
    it("handles entire escape sequence in a single handleData call", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel");
      // Deliver entire sequence as one string
      interceptor.handleData("\x1b[1;5A");
      interceptor.handleData("p");

      expect(interceptor.buffer).toBe("/help");
    });

    it("handles escape sequence split across handleData calls", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel");
      // Split the sequence across calls
      interceptor.handleData("\x1b");
      interceptor.handleData("[1;5A");
      interceptor.handleData("p");

      expect(interceptor.buffer).toBe("/help");
    });
  });

  describe("backspace and cancel", () => {
    it("backspace removes last character", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/helx\x7fp\r");

      expect(helpHandler).toHaveBeenCalledTimes(1);
    });

    it("backspace on / exits command mode", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/\x7f");

      expect(interceptor.isInCommandMode).toBe(false);
    });

    it("Ctrl+C cancels command mode", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel\x03");

      expect(interceptor.isInCommandMode).toBe(false);
      expect(interceptor.buffer).toBe("");
    });
  });

  describe("dispose", () => {
    it("resets all state on dispose", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel");
      expect(interceptor.isInCommandMode).toBe(true);

      interceptor.dispose();

      expect(interceptor.isInCommandMode).toBe(false);
      expect(interceptor.buffer).toBe("");
    });

    it("dispose during escape sequence resets escape state", () => {
      const interceptor = createInterceptor();

      interceptor.handleData("/hel\x1b");
      interceptor.dispose();

      // After dispose, normal input should work
      interceptor.handleData("/help\r");
      expect(helpHandler).toHaveBeenCalledTimes(1);
    });
  });
});
