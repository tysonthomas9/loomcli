/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import {
  CLI_SETUP_REQUEST_EVENT,
  CLI_SETUP_REQUEST_KEY,
  clearPendingCliSetupRequest,
  getCliSetupInstructions,
  readPendingCliSetupRequest,
  requestCliSetup,
} from "../cliSetup";
import type { BackendInfo } from "@/utils/workspace";

const CODEX_BACKEND: BackendInfo = {
  name: "codex",
  displayName: "Codex",
  provider: "OpenAI",
  brandColor: "#10a37f",
  available: false,
  installed: false,
};

const CURSOR_BACKEND: BackendInfo = {
  name: "cursor",
  displayName: "Cursor",
  provider: "Anysphere",
  brandColor: "#00e5ff",
  available: false,
  installed: false,
};

describe("cliSetup", () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("stores and dispatches a CLI setup request", () => {
    const listener = vi.fn();
    window.addEventListener(CLI_SETUP_REQUEST_EVENT, listener);

    const request = requestCliSetup(CODEX_BACKEND, "install");

    expect(readPendingCliSetupRequest()).toEqual(request);
    expect(listener).toHaveBeenCalledTimes(1);
    expect((listener.mock.calls[0]?.[0] as CustomEvent).detail).toMatchObject({
      backendName: "codex",
      action: "install",
    });

    window.removeEventListener(CLI_SETUP_REQUEST_EVENT, listener);
  });

  it("returns install instructions with the Codex npm command", () => {
    const instructions = getCliSetupInstructions({
      id: "1",
      backendName: "codex",
      displayName: "Codex",
      provider: "OpenAI",
      brandColor: "#10a37f",
      action: "install",
    });

    expect(instructions.title).toBe("Install Codex");
    expect(instructions.command).toBe("npm install -g @openai/codex");
  });

  it("returns install instructions with the Cursor install script", () => {
    const instructions = getCliSetupInstructions({
      id: "1",
      backendName: "cursor",
      displayName: "Cursor",
      provider: "Anysphere",
      brandColor: "#00e5ff",
      action: "install",
    });

    expect(instructions.title).toBe("Install Cursor");
    expect(instructions.command).toBe(
      "curl https://cursor.com/install -fsS | bash",
    );
  });

  it("clears only the matching pending request id when one is provided", () => {
    const request = requestCliSetup(CURSOR_BACKEND, "login");

    clearPendingCliSetupRequest("other");
    expect(sessionStorage.getItem(CLI_SETUP_REQUEST_KEY)).not.toBeNull();

    clearPendingCliSetupRequest(request.id);
    expect(sessionStorage.getItem(CLI_SETUP_REQUEST_KEY)).toBeNull();
  });
});
