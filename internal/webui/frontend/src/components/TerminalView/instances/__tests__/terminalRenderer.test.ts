import { describe, expect, it } from "vitest";

import { terminalRendererForBackend } from "../terminalRenderer";

describe("terminalRendererForBackend", () => {
  it.each(["claude", " CLAUDE "])("routes %j to xterm", (backend) => {
    expect(terminalRendererForBackend(backend)).toBe("xterm");
  });

  it.each([
    undefined,
    "",
    "claude-code",
    "codex",
    "gemini",
    "cursor",
    "opencode",
    "shell",
    "agent",
    "unknown",
  ])("keeps %j on wterm", (backend) => {
    expect(terminalRendererForBackend(backend)).toBe("wterm");
  });
});
