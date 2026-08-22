import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef } from "react";

import {
  DEFAULT_FONT_FAMILY,
  DEFAULT_FONT_SIZE,
  TERMINAL_FONT_CHANGE_EVENT,
  TERMINAL_FONT_FAMILY_VAR,
  TERMINAL_FONT_SIZE_VAR,
  type TerminalFontChangeDetail,
} from "@/hooks/terminal/useTerminalFont";

export interface XTermRendererHandle {
  write: (data: string | Uint8Array) => void;
  reset: () => Promise<void>;
  setSize: (cols: number, rows: number) => void;
  focus: () => void;
  fit: () => { cols: number; rows: number } | null;
  scrollToBottom: () => void;
}

export const TERMINAL_SCROLLBACK_LINES = 10_000;

interface XTermRendererProps {
  className?: string | undefined;
  onReady: (handle: XTermRendererHandle) => void;
  onDispose: (handle: XTermRendererHandle) => void;
  onData: (data: string) => void;
  onBinary: (data: Uint8Array) => void;
  onResize: (cols: number, rows: number) => void;
  onFocus?: (() => void) | undefined;
}

function cssValue(name: string, fallback: string): string {
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
  return value || fallback;
}

function readTheme() {
  return {
    background: cssValue("--terminal-bg", "#0d0d0d"),
    foreground: cssValue("--terminal-fg", "#d4d4d4"),
    cursor: cssValue("--terminal-cursor", "#d4d4d4"),
    selectionBackground: cssValue(
      "--terminal-selection",
      "rgba(255, 255, 255, 0.15)",
    ),
  };
}

function readFont(): TerminalFontChangeDetail {
  const root = getComputedStyle(document.documentElement);
  const fontFamily =
    root.getPropertyValue(TERMINAL_FONT_FAMILY_VAR).trim() ||
    DEFAULT_FONT_FAMILY;
  const parsedSize = Number.parseFloat(
    root.getPropertyValue(TERMINAL_FONT_SIZE_VAR),
  );
  return {
    fontFamily,
    fontSize: Number.isFinite(parsedSize) ? parsedSize : DEFAULT_FONT_SIZE,
  };
}

function binaryStringToBytes(data: string): Uint8Array {
  const bytes = new Uint8Array(data.length);
  for (let index = 0; index < data.length; index += 1) {
    bytes[index] = data.charCodeAt(index) & 0xff;
  }
  return bytes;
}

/**
 * Thin xterm.js renderer shared by interactive terminal tabs.
 * PTY ownership and reconnect behavior stay in TerminalInstance.
 */
export function XTermRenderer({
  className,
  onReady,
  onDispose,
  onData,
  onBinary,
  onResize,
  onFocus,
}: XTermRendererProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const onDataRef = useRef(onData);
  onDataRef.current = onData;
  const onBinaryRef = useRef(onBinary);
  onBinaryRef.current = onBinary;
  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  const onFocusRef = useRef(onFocus);
  onFocusRef.current = onFocus;
  const onReadyRef = useRef(onReady);
  onReadyRef.current = onReady;
  const onDisposeRef = useRef(onDispose);
  onDisposeRef.current = onDispose;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const font = readFont();
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: font.fontFamily,
      fontSize: font.fontSize,
      scrollback: TERMINAL_SCROLLBACK_LINES,
      theme: readTheme(),
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(host);

    const fit = (): { cols: number; rows: number } | null => {
      if (host.clientWidth <= 0 || host.clientHeight <= 0) return null;

      // FitAddon may reflow wrapped rows. Anchor the top visible buffer line
      // with an xterm marker so a user reading older output stays on that
      // content instead of jumping to the prompt. Markers move with buffer
      // trims/reflow; the distance fallback covers alternate-buffer modes,
      // where xterm does not provide markers.
      const before = terminal.buffer.active;
      const wasAtBottom = before.viewportY >= before.baseY;
      const distanceFromBottom = Math.max(0, before.baseY - before.viewportY);
      const marker = wasAtBottom
        ? undefined
        : terminal.registerMarker(
            before.viewportY - (before.baseY + before.cursorY),
          );
      try {
        fitAddon.fit();
      } catch {
        marker?.dispose();
        return null;
      }

      const after = terminal.buffer.active;
      if (wasAtBottom) {
        terminal.scrollToBottom();
      } else if (marker && !marker.isDisposed) {
        terminal.scrollToLine(Math.min(marker.line, after.baseY));
      } else {
        terminal.scrollToLine(Math.max(0, after.baseY - distanceFromBottom));
      }
      marker?.dispose();
      return { cols: terminal.cols, rows: terminal.rows };
    };

    let applyingCanonicalResize = false;
    const handle: XTermRendererHandle = {
      write: (data) => terminal.write(data),
      reset: () =>
        new Promise<void>((resolve) => {
          terminal.write("", () => {
            terminal.reset();
            resolve();
          });
        }),
      setSize: (cols, rows) => {
        if (terminal.cols === cols && terminal.rows === rows) return;
        applyingCanonicalResize = true;
        try {
          terminal.resize(cols, rows);
        } finally {
          applyingCanonicalResize = false;
        }
      },
      focus: () => terminal.focus(),
      fit,
      scrollToBottom: () => terminal.scrollToBottom(),
    };

    const dataDisposable = terminal.onData((data) => onDataRef.current(data));
    const binaryDisposable = terminal.onBinary((data) =>
      onBinaryRef.current(binaryStringToBytes(data)),
    );
    const resizeDisposable = terminal.onResize(({ cols, rows }) => {
      if (!applyingCanonicalResize) onResizeRef.current(cols, rows);
    });

    const textarea = terminal.textarea;
    const handleFocus = () => onFocusRef.current?.();
    textarea?.addEventListener("focus", handleFocus);

    let resizeTimer: ReturnType<typeof setTimeout> | null = null;
    const resizeObserver =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            if (resizeTimer) clearTimeout(resizeTimer);
            resizeTimer = setTimeout(fit, 100);
          });
    resizeObserver?.observe(host);

    const handleFontChange = (event: Event) => {
      const detail = (event as CustomEvent<TerminalFontChangeDetail>).detail;
      if (!detail) return;
      terminal.options.fontFamily = detail.fontFamily;
      terminal.options.fontSize = detail.fontSize;
      fit();
    };
    window.addEventListener(TERMINAL_FONT_CHANGE_EVENT, handleFontChange);

    const themeObserver = new MutationObserver(() => {
      terminal.options.theme = readTheme();
    });
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "style"],
    });

    fit();
    onReadyRef.current(handle);

    return () => {
      if (resizeTimer) clearTimeout(resizeTimer);
      resizeObserver?.disconnect();
      themeObserver.disconnect();
      window.removeEventListener(TERMINAL_FONT_CHANGE_EVENT, handleFontChange);
      textarea?.removeEventListener("focus", handleFocus);
      dataDisposable.dispose();
      binaryDisposable.dispose();
      resizeDisposable.dispose();
      onDisposeRef.current(handle);
      terminal.dispose();
    };
  }, []);

  return (
    <div
      ref={hostRef}
      className={className}
      data-testid="xterm-terminal"
      data-terminal-input
      role="application"
    />
  );
}
