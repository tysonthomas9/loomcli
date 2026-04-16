/**
 * TerminalInstance component.
 * Single wterm terminal pane with WebSocket connection, backoff reconnect,
 * scrollback replay, and copy/paste UX. Mounted inside a tabbed TerminalView.
 */

import { Terminal, useTerminal } from "@wterm/react";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import { fetchScrollback } from "@/api/terminal";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import {
  startAutoReconnect,
  type ReconnectConfig,
  type ReconnectState,
} from "@/utils/reconnectBackoff";
import { stripAnsi } from "@/utils/stripAnsi";

import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import { SlashCommandInterceptor } from "./slashCommandInterceptor";
import {
  connectWebSocket,
  encodeResize,
  type TerminalSink,
} from "./terminalConnection";
import styles from "./TerminalInstance.module.css";

/** Stricter backoff for initial connection failures (e.g. 501 when session doesn't exist). */
const INITIAL_CONNECT_CONFIG: ReconnectConfig = {
  maxAttempts: 3,
  baseDelay: 3000,
  maxDelay: 15000,
  jitterFactor: 0.5,
};

const RECONNECT_TIMEOUT_MS = 30_000;

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"
  | "crashed";

export interface ContextMenuEvent {
  x: number;
  y: number;
  hasSelection: boolean;
}

export interface TerminalInstanceProps {
  sessionName: string;
  isActive: boolean;
  fontFamily?: string;
  fontSize?: number;
  onConnectionStateChange?: (
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  onCopyNotify?: () => void;
  onPasteRequest?: () => void;
  onContextMenu?: (event: ContextMenuEvent) => void;
  onReconnectStateChange?: (state: ReconnectOverlayState) => void;
  onOutput?: () => void;
  onBackendCrash?: (reason: string) => void;
  onTerminalFocus?: (() => void) | undefined;
  /** When set, connects to agent terminal WebSocket instead of regular terminal. */
  agentName?: string | undefined;
}

export interface TerminalInstanceHandle {
  /** Gracefully disconnect the WebSocket and cancel reconnect. Returns when WS is closed. */
  disconnect: () => Promise<void>;
  reconnect: () => void;
  pasteText: (text: string) => void;
  getSelection: () => string;
  hasSelection: () => boolean;
  selectAll: () => void;
  focus: () => void;
}

function selectionInside(node: Node | null, container: HTMLElement | null) {
  if (!container || !node) return false;
  return container.contains(node);
}

function wrapperSelectionText(container: HTMLElement | null): string {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed) return "";
  if (!selectionInside(sel.anchorNode, container)) return "";
  return sel.toString();
}

function writeToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard
      .writeText(text)
      .then(() => true)
      .catch(() => execCommandCopy(text));
  }
  return Promise.resolve(execCommandCopy(text));
}

function execCommandCopy(text: string): boolean {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  try {
    ta.select();
    return document.execCommand("copy");
  } finally {
    document.body.removeChild(ta);
  }
}

export const TerminalInstance = forwardRef<
  TerminalInstanceHandle,
  TerminalInstanceProps
>(function TerminalInstance(
  {
    sessionName,
    isActive,
    fontFamily,
    fontSize,
    onConnectionStateChange,
    onCopyNotify,
    onPasteRequest,
    onContextMenu,
    onReconnectStateChange,
    onOutput,
    onBackendCrash,
    onTerminalFocus,
    agentName,
  },
  ref,
) {
  const { workspaceId } = useWorkspaceContext();
  const { ref: wtermRef, write, focus: wtermFocus } = useTerminal();

  const wrapperRef = useRef<HTMLDivElement>(null);
  const writeRef = useRef(write);
  writeRef.current = write;

  const wsRef = useRef<WebSocket | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const reconnectAttemptRef = useRef(0);
  const reconnectResolveRef = useRef<((ok: boolean) => void) | null>(null);
  const beingKilledRef = useRef(false);
  const hasConnectedRef = useRef(false);
  const isAtBottomRef = useRef(true);
  const sizeRef = useRef({ cols: 80, rows: 24 });
  const interceptorRef = useRef<SlashCommandInterceptor | null>(null);
  const suppressCopyOnSelectRef = useRef(false);
  const copyDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [mountKey, setMountKey] = useState(0);
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const [showNewOutputPill, setShowNewOutputPill] = useState(false);

  // Stable callback refs
  const onCopyNotifyRef = useRef(onCopyNotify);
  onCopyNotifyRef.current = onCopyNotify;
  const onPasteRequestRef = useRef(onPasteRequest);
  onPasteRequestRef.current = onPasteRequest;
  const onContextMenuRef = useRef(onContextMenu);
  onContextMenuRef.current = onContextMenu;
  const onOutputRef = useRef(onOutput);
  onOutputRef.current = onOutput;
  const onBackendCrashRef = useRef(onBackendCrash);
  onBackendCrashRef.current = onBackendCrash;
  const onTerminalFocusRef = useRef(onTerminalFocus);
  onTerminalFocusRef.current = onTerminalFocus;
  const onReconnectStateChangeRef = useRef(onReconnectStateChange);
  onReconnectStateChangeRef.current = onReconnectStateChange;
  const onConnectionStateChangeRef = useRef(onConnectionStateChange);
  onConnectionStateChangeRef.current = onConnectionStateChange;

  // Mirror connection state to parent
  useEffect(() => {
    if (connectionState === "connected") {
      hasConnectedRef.current = true;
    }
    onConnectionStateChangeRef.current?.(connectionState, hasConnectedRef.current);
  }, [connectionState]);

  const clearReconnectState = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    reconnectAttemptRef.current = 0;
    onReconnectStateChangeRef.current?.(null);
  }, []);

  const handleWsOutput = useCallback(() => {
    onOutputRef.current?.();
    if (!isAtBottomRef.current) {
      setShowNewOutputPill(true);
    }
  }, []);

  // Connect the WebSocket. Caller has already ensured the wterm instance is
  // mounted and (optionally) has replayed scrollback.
  const startWs = useCallback(() => {
    wsCleanupRef.current?.();
    const sink: TerminalSink = {
      write: (data) => writeRef.current(data),
      getSize: () => sizeRef.current,
    };
    const cleanup = connectWebSocket(
      workspaceId,
      sessionName,
      sink,
      wsRef,
      setConnectionState,
      () => {
        reconnectResolveRef.current?.(true);
        reconnectResolveRef.current = null;
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        clearReconnectState();
      },
      () => {
        reconnectResolveRef.current?.(false);
        reconnectResolveRef.current = null;
        if (beingKilledRef.current) return;
        if (reconnectCancelRef.current) return;
        onReconnectStateChangeRef.current?.("reconnecting");
        // Wall-clock timeout only for mid-session disconnects; initial
        // connect uses its own tight backoff window.
        if (hasConnectedRef.current && !reconnectTimeoutRef.current) {
          reconnectTimeoutRef.current = setTimeout(() => {
            reconnectTimeoutRef.current = null;
            reconnectCancelRef.current?.();
            reconnectCancelRef.current = null;
            onReconnectStateChangeRef.current?.("expired");
            setConnectionState("error");
          }, RECONNECT_TIMEOUT_MS);
        }
        const reconnectConfig = hasConnectedRef.current
          ? undefined
          : INITIAL_CONNECT_CONFIG;
        const cancel = startAutoReconnect(
          () =>
            new Promise<boolean>((resolve) => {
              reconnectResolveRef.current = resolve;
              // Force wterm remount — handleReady will fetch scrollback + restart WS.
              setMountKey((k) => k + 1);
            }),
          (state: ReconnectState) => {
            reconnectAttemptRef.current = state.attempt;
            if (state.gaveUp) {
              clearReconnectState();
              onReconnectStateChangeRef.current?.("expired");
              setConnectionState("error");
            }
          },
          reconnectConfig,
        );
        reconnectCancelRef.current = cancel;
      },
      handleWsOutput,
      (reason) => onBackendCrashRef.current?.(reason),
      agentName,
      () => {
        beingKilledRef.current = true;
      },
    );
    wsCleanupRef.current = cleanup;
  }, [
    workspaceId,
    sessionName,
    agentName,
    clearReconnectState,
    handleWsOutput,
  ]);

  // wterm is mounted (or remounted). Replay scrollback if reconnecting,
  // then start the WS.
  const handleReady = useCallback(() => {
    interceptorRef.current?.dispose();
    interceptorRef.current = new SlashCommandInterceptor(
      (data) => writeRef.current(data),
      workspaceId,
    );

    // Refocus if user was previously inside the terminal. Remount on
    // reconnect discards focus by default.
    const wasFocused =
      wrapperRef.current !== null &&
      wrapperRef.current.contains(document.activeElement);
    if (wasFocused) wtermFocus();

    if (hasConnectedRef.current) {
      // Reconnect path: fetch and replay scrollback into the fresh grid.
      fetchScrollback(workspaceId, sessionName)
        .then(({ content }) => {
          if (content) writeRef.current(content);
        })
        .catch(() => {})
        .finally(() => startWs());
    } else {
      startWs();
    }
  }, [workspaceId, sessionName, startWs, wtermFocus]);

  // True when the underlying program is driving the alt-screen (tmux,
  // Claude Code, vim, less, htop…). Used to gate loom's terminal-UI
  // extensions (slash-command interceptor, mouse-wheel forwarder) so they
  // step aside while a TUI is active. Piercing wterm's internals is
  // isolated here — if @wterm/react restructures `instance.bridge`, only
  // this helper breaks.
  const isInAltScreen = useCallback(
    () => wtermRef.current?.instance?.bridge?.usingAltScreen() ?? false,
    [wtermRef],
  );

  const handleData = useCallback(
    (data: string) => {
      const ws = wsRef.current;
      const sendToWs = (d: string) => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(d);
      };
      // Loom's slash-command interceptor only runs at a normal-mode shell
      // prompt. When a TUI is driving the alt-screen, pass keystrokes
      // through unmodified so the app's own slash commands work (Claude's
      // /status, /clear, /compact, less's /search, vim's /-search, etc.).
      const interceptor = interceptorRef.current;
      if (interceptor && !isInAltScreen()) {
        interceptor.handleData(data, sendToWs);
      } else {
        sendToWs(data);
      }
    },
    [isInAltScreen],
  );

  const handleResize = useCallback((cols: number, rows: number) => {
    sizeRef.current = { cols, rows };
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encodeResize(cols, rows));
    }
  }, []);

  const handleScrollToBottom = useCallback(() => {
    const el = wrapperRef.current?.querySelector(".wterm") as HTMLElement | null;
    if (el) el.scrollTop = el.scrollHeight;
    setShowNewOutputPill(false);
  }, []);

  // Imperative handle
  useImperativeHandle(
    ref,
    () => ({
      disconnect() {
        beingKilledRef.current = true;
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        const ws = wsRef.current;
        if (
          ws &&
          (ws.readyState === WebSocket.OPEN ||
            ws.readyState === WebSocket.CONNECTING)
        ) {
          return new Promise<void>((resolve) => {
            const timeout = setTimeout(resolve, 2000);
            ws.addEventListener(
              "close",
              () => {
                clearTimeout(timeout);
                resolve();
              },
              { once: true },
            );
            wsCleanupRef.current?.();
            wsCleanupRef.current = null;
          });
        }
        wsCleanupRef.current?.();
        wsCleanupRef.current = null;
        return Promise.resolve();
      },
      reconnect() {
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        wsCleanupRef.current?.();
        wsCleanupRef.current = null;
        setMountKey((k) => k + 1);
      },
      pasteText(text: string) {
        const ws = wsRef.current;
        if (ws?.readyState === WebSocket.OPEN) ws.send(text);
      },
      getSelection() {
        return stripAnsi(wrapperSelectionText(wrapperRef.current));
      },
      hasSelection() {
        return wrapperSelectionText(wrapperRef.current).length > 0;
      },
      selectAll() {
        const el = wrapperRef.current;
        if (!el) return;
        const range = document.createRange();
        range.selectNodeContents(el);
        const sel = window.getSelection();
        sel?.removeAllRanges();
        sel?.addRange(range);
      },
      focus() {
        wtermFocus();
      },
    }),
    [wtermFocus],
  );

  // Copy-on-select — scoped to the wrapper via selection containment check.
  useEffect(() => {
    const handler = () => {
      if (suppressCopyOnSelectRef.current) {
        suppressCopyOnSelectRef.current = false;
        return;
      }
      const text = wrapperSelectionText(wrapperRef.current);
      if (!text) return;
      const clean = stripAnsi(text);
      if (copyDebounceRef.current) clearTimeout(copyDebounceRef.current);
      copyDebounceRef.current = setTimeout(() => {
        writeToClipboard(clean).then((ok) => {
          if (ok) onCopyNotifyRef.current?.();
        });
      }, 100);
    };
    document.addEventListener("selectionchange", handler);
    return () => {
      document.removeEventListener("selectionchange", handler);
      if (copyDebounceRef.current) clearTimeout(copyDebounceRef.current);
    };
  }, []);

  // Wrapper DOM event bindings: keydown (paste + tab shortcuts), focusin,
  // contextmenu, paste, scroll-tracking.
  useEffect(() => {
    const el = wrapperRef.current;
    if (!el) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey;
      // Ctrl/Cmd+V — paste with multi-line confirm
      if (mod && !e.shiftKey && !e.altKey && e.key === "v") {
        if (window.isSecureContext) {
          e.preventDefault();
          e.stopPropagation();
          onPasteRequestRef.current?.();
        }
        return;
      }
      // Ctrl/Cmd+Shift+V — legacy paste request
      if (mod && e.shiftKey && e.key === "V") {
        e.preventDefault();
        e.stopPropagation();
        onPasteRequestRef.current?.();
        return;
      }
      // Cmd/Ctrl+T, Cmd/Ctrl+W, Cmd/Ctrl+1-9 — suppress wterm handling so the
      // document-level handler in TerminalView sees them for tab management.
      if (
        mod &&
        !e.shiftKey &&
        !e.altKey &&
        (e.key === "t" ||
          e.key === "w" ||
          (e.key >= "1" && e.key <= "9"))
      ) {
        e.stopPropagation();
      }
    };

    const handleContextMenu = (e: MouseEvent) => {
      e.preventDefault();
      suppressCopyOnSelectRef.current = true;
      onContextMenuRef.current?.({
        x: e.clientX,
        y: e.clientY,
        hasSelection: wrapperSelectionText(el).length > 0,
      });
    };

    const handleBrowserPaste = (e: ClipboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      onPasteRequestRef.current?.();
    };

    const handleFocusIn = () => {
      onTerminalFocusRef.current?.();
    };

    // Wheel: forward as SGR mouse escape only when the app is in alt-screen
    // mode (tmux, vim, less). In normal shells, let the native DOM scroll
    // wterm's own scrollback. loom's backend sets `set -g mouse on` on every
    // tmux session so copy-mode engages on wheel events automatically.
    const handleWheel = (e: WheelEvent) => {
      if (!isInAltScreen()) return;

      const ws = wsRef.current;
      if (ws?.readyState !== WebSocket.OPEN) return;

      const wtermEl = el.querySelector(".wterm") as HTMLElement | null;
      if (!wtermEl) return;
      const rect = wtermEl.getBoundingClientRect();
      const { cols, rows } = sizeRef.current;
      if (!cols || !rows || !rect.width || !rect.height) return;

      // SGR mouse protocol — 64/65 wheel up/down, 66/67 wheel left/right.
      let btn: number | null = null;
      if (Math.abs(e.deltaY) >= Math.abs(e.deltaX)) {
        if (e.deltaY < 0) btn = 64;
        else if (e.deltaY > 0) btn = 65;
      } else {
        if (e.deltaX < 0) btn = 66;
        else if (e.deltaX > 0) btn = 67;
      }
      if (btn === null) return;

      const col = Math.max(
        1,
        Math.min(cols, Math.floor((e.clientX - rect.left) / (rect.width / cols)) + 1),
      );
      const row = Math.max(
        1,
        Math.min(rows, Math.floor((e.clientY - rect.top) / (rect.height / rows)) + 1),
      );

      e.preventDefault();
      ws.send(`\x1b[<${btn};${col};${row}M`);
    };

    el.addEventListener("keydown", handleKeyDown, { capture: true });
    el.addEventListener("contextmenu", handleContextMenu);
    el.addEventListener("paste", handleBrowserPaste, { capture: true });
    el.addEventListener("focusin", handleFocusIn);
    el.addEventListener("wheel", handleWheel, { capture: true, passive: false });

    return () => {
      el.removeEventListener("keydown", handleKeyDown, { capture: true });
      el.removeEventListener("contextmenu", handleContextMenu);
      el.removeEventListener("paste", handleBrowserPaste, { capture: true });
      el.removeEventListener("focusin", handleFocusIn);
      el.removeEventListener("wheel", handleWheel, { capture: true });
    };
  }, [isInAltScreen]);

  // Scroll tracking for "New output" pill — re-bound after each remount.
  useEffect(() => {
    const el = wrapperRef.current?.querySelector(".wterm") as HTMLElement | null;
    if (!el) return;
    const handleScroll = () => {
      const atBottom =
        el.scrollHeight - el.scrollTop - el.clientHeight < 10;
      isAtBottomRef.current = atBottom;
      if (atBottom) setShowNewOutputPill(false);
    };
    el.addEventListener("scroll", handleScroll);
    return () => el.removeEventListener("scroll", handleScroll);
  }, [mountKey]);

  // Sign-out: close WebSocket so a fresh login can reconnect cleanly.
  useEffect(() => {
    const handleSignOut = () => {
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
    };
    window.addEventListener("auth-sign-out", handleSignOut);
    return () => window.removeEventListener("auth-sign-out", handleSignOut);
  }, []);

  // Tab activate — resend current size so tmux notices if we were hidden.
  useEffect(() => {
    if (!isActive) return;
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(encodeResize(sizeRef.current.cols, sizeRef.current.rows));
    }
  }, [isActive]);

  // Cleanup on unmount or sessionName change.
  useEffect(() => {
    return () => {
      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;
      // Settle any pending reconnect promise so startAutoReconnect's .then
      // chain doesn't strand waiting on a dead fiber.
      reconnectResolveRef.current?.(false);
      reconnectResolveRef.current = null;
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = null;
      }
      wsCleanupRef.current?.();
      wsCleanupRef.current = null;
      interceptorRef.current?.dispose();
      interceptorRef.current = null;
    };
  }, [sessionName]);

  return (
    <div ref={wrapperRef} className={styles.wrapper}>
      <Terminal
        key={mountKey}
        ref={wtermRef as React.RefObject<React.ComponentRef<typeof Terminal>>}
        cols={80}
        rows={24}
        autoResize
        cursorBlink
        onData={handleData}
        onResize={handleResize}
        onReady={handleReady}
        className={styles.container}
        style={{
          ...(fontSize ? { fontSize: `${fontSize}px` } : {}),
          ...(fontFamily ? { fontFamily } : {}),
        }}
        data-testid="terminal-instance"
      />
      {showNewOutputPill && (
        <button
          className={styles.newOutputPill}
          onClick={handleScrollToBottom}
          type="button"
          aria-label="Scroll to new output"
        >
          New output ↓
        </button>
      )}
    </div>
  );
});
