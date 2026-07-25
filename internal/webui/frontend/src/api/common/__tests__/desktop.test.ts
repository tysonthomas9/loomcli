/**
 * @vitest-environment jsdom
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import { isDesktopRuntime, pickDesktopFolder } from "../desktop";

describe("desktop runtime helpers", () => {
  afterEach(() => {
    Reflect.deleteProperty(window, "__TAURI__");
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  });

  it("reports browser mode when Tauri globals are absent", () => {
    expect(isDesktopRuntime()).toBe(false);
  });

  it("detects the public Tauri runtime global", () => {
    window.__TAURI__ = {
      core: {
        invoke: vi.fn(),
      },
    };

    expect(isDesktopRuntime()).toBe(true);
  });

  it("uses the internal Tauri invoke bridge for desktop folder selection", async () => {
    const invoke = vi.fn().mockResolvedValue("/tmp/workspace");
    window.__TAURI_INTERNALS__ = { invoke };

    await expect(pickDesktopFolder()).resolves.toBe("/tmp/workspace");
    expect(invoke).toHaveBeenCalledWith("pick_folder");
  });
});
