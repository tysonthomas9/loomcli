/**
 * @vitest-environment jsdom
 */

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const xtermMocks = vi.hoisted(() => {
  type Disposable = { dispose: ReturnType<typeof vi.fn> };

  class MockFitAddon {
    fit = vi.fn();
  }

  class MockTerminal {
    static instances: MockTerminal[] = [];

    cols = 80;
    rows = 24;
    options: Record<string, unknown>;
    textarea: HTMLTextAreaElement | undefined;
    write = vi.fn();
    focus = vi.fn();
    scrollToBottom = vi.fn();
    dispose = vi.fn();
    loadAddon = vi.fn();
    open = vi.fn((host: HTMLElement) => {
      this.textarea = document.createElement("textarea");
      host.appendChild(this.textarea);
    });
    dataListener: ((data: string) => void) | undefined;
    binaryListener: ((data: string) => void) | undefined;
    resizeListener:
      | ((size: { cols: number; rows: number }) => void)
      | undefined;
    dataDisposable: Disposable = { dispose: vi.fn() };
    binaryDisposable: Disposable = { dispose: vi.fn() };
    resizeDisposable: Disposable = { dispose: vi.fn() };

    constructor(options: Record<string, unknown>) {
      this.options = options;
      MockTerminal.instances.push(this);
    }

    onData(listener: (data: string) => void): Disposable {
      this.dataListener = listener;
      return this.dataDisposable;
    }

    onBinary(listener: (data: string) => void): Disposable {
      this.binaryListener = listener;
      return this.binaryDisposable;
    }

    onResize(
      listener: (size: { cols: number; rows: number }) => void,
    ): Disposable {
      this.resizeListener = listener;
      return this.resizeDisposable;
    }
  }

  return { MockFitAddon, MockTerminal };
});

vi.mock("@xterm/addon-fit", () => ({ FitAddon: xtermMocks.MockFitAddon }));
vi.mock("@xterm/xterm", () => ({ Terminal: xtermMocks.MockTerminal }));

import { XTermRenderer, type XTermRendererHandle } from "../XTermRenderer";

describe("XTermRenderer", () => {
  afterEach(() => {
    xtermMocks.MockTerminal.instances.length = 0;
    vi.restoreAllMocks();
  });

  it("bridges text, binary mouse data, resize, and its imperative handle", async () => {
    const onReady = vi.fn();
    const onDispose = vi.fn();
    const onData = vi.fn();
    const onBinary = vi.fn();
    const onResize = vi.fn();

    const view = render(
      <XTermRenderer
        onReady={onReady}
        onDispose={onDispose}
        onData={onData}
        onBinary={onBinary}
        onResize={onResize}
      />,
    );

    await waitFor(() => expect(onReady).toHaveBeenCalledOnce());
    const terminal = xtermMocks.MockTerminal.instances[0];
    expect(terminal).toBeDefined();
    expect(terminal?.options.scrollback).toBe(10_000);

    const handle = onReady.mock.calls[0]?.[0] as XTermRendererHandle;
    act(() => terminal?.dataListener?.("hello"));
    expect(onData).toHaveBeenCalledWith("hello");

    act(() => terminal?.binaryListener?.("\x00\xffA"));
    expect(onBinary).toHaveBeenCalledOnce();
    expect(Array.from(onBinary.mock.calls[0]?.[0] as Uint8Array)).toEqual([
      0, 255, 65,
    ]);

    act(() => terminal?.resizeListener?.({ cols: 120, rows: 40 }));
    expect(onResize).toHaveBeenCalledWith(120, 40);

    handle.write("output");
    handle.focus();
    handle.scrollToBottom();
    expect(terminal?.write).toHaveBeenCalledWith("output");
    expect(terminal?.focus).toHaveBeenCalledOnce();
    expect(terminal?.scrollToBottom).toHaveBeenCalledOnce();

    const host = view.getByTestId("xterm-terminal");
    Object.defineProperties(host, {
      clientWidth: { configurable: true, value: 800 },
      clientHeight: { configurable: true, value: 480 },
    });
    if (terminal) {
      terminal.cols = 132;
      terminal.rows = 42;
    }
    expect(handle.fit()).toEqual({ cols: 132, rows: 42 });
    const fitAddon = terminal?.loadAddon.mock.calls[0]?.[0] as
      | InstanceType<typeof xtermMocks.MockFitAddon>
      | undefined;
    expect(fitAddon?.fit).toHaveBeenCalledOnce();

    view.unmount();
    expect(onDispose).toHaveBeenCalledWith(handle);
    expect(terminal?.dataDisposable.dispose).toHaveBeenCalledOnce();
    expect(terminal?.binaryDisposable.dispose).toHaveBeenCalledOnce();
    expect(terminal?.resizeDisposable.dispose).toHaveBeenCalledOnce();
    expect(terminal?.dispose).toHaveBeenCalledOnce();
  });
});
