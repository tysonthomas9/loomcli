/**
 * TerminalInstance component.
 * Single xterm.js terminal pane with WebSocket connection, WebGL rendering,
 * SearchAddon, FitAddon, and auto-reconnect. Designed to be mounted inside
 * a tabbed TerminalView container.
 */

import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import { Terminal } from "@xterm/xterm";
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  useCallback,
} from "react";

import { fetchScrollback } from "@/api/terminal";
import { stripAnsi } from "@/utils/stripAnsi";
import {
  startAutoReconnect,
  type ReconnectState,
} from "@/utils/reconnectBackoff";
import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import styles from "./TerminalInstance.module.css";
import { SlashCommandInterceptor } from "./slashCommandInterceptor";
import { connectWebSocket, encodeResize } from "./terminalConnection";
import { getTerminalTheme } from "./terminalTheme";
import "@xterm/xterm/css/xterm.css";

const SEARCH_DECORATIONS = {
  matchBackground: "#515C6A",
  matchBorder: "#515C6A",
  matchOverviewRuler: "#d186167e",
  activeMatchBackground: "#EE8B17",
  activeMatchBorder: "#EE8B17",
  activeMatchColorOverviewRuler: "#ee8b17ff",
};

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"
  | "crashed";

export interface SearchResultInfo {
  resultIndex: number;
  resultCount: number;
}

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
  onSearchRequest?: () => void;
  onContextMenu?: (event: ContextMenuEvent) => void;
  onReconnectStateChange?: (state: ReconnectOverlayState) => void;
  onOutput?: () => void;
  onSearchResultChange?: (result: SearchResultInfo | null) => void;
  onBackendCrash?: (reason: string) => void;
  onTerminalFocus?: (() => void) | undefined;
}

export interface TerminalInstanceHandle {
  search: (
    term: string,
    options?: {
      caseSensitive?: boolean;
      wholeWord?: boolean;
      regex?: boolean;
    },
  ) => boolean;
  findNext: () => boolean;
  findPrevious: () => boolean;
  clearSearch: () => void;
  reconnect: () => void;
  pasteText: (text: string) => void;
  getSelection: () => string;
  hasSelection: () => boolean;
  selectAll: () => void;
  focus: () => void;
}

export const TerminalInstance = forwardRef<
  TerminalInstanceHandle,
  TerminalInstanceProps
>(function TerminalInstance(
  {
    sessionName,
    isActive,
    fontFamily = 'Menlo, Monaco, "Courier New", monospace',
    fontSize = 14,
    onConnectionStateChange,
    onCopyNotify,
    onPasteRequest,
    onSearchRequest,
    onContextMenu,
    onReconnectStateChange,
    onOutput,
    onSearchResultChange,
    onBackendCrash,
    onTerminalFocus,
  },
  ref,
) {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const wsCleanupRef = useRef<(() => void) | null>(null);
  const reconnectCancelRef = useRef<(() => void) | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const reconnectAttemptRef = useRef(0);
  const interceptorRef = useRef<SlashCommandInterceptor | null>(null);

  // Scroll-tracking and new output pill
  const isAtBottomRef = useRef(true);
  const [showNewOutputPill, setShowNewOutputPill] = useState(false);
  const onOutputRef = useRef(onOutput);
  onOutputRef.current = onOutput;
  const onSearchResultChangeRef = useRef(onSearchResultChange);
  onSearchResultChangeRef.current = onSearchResultChange;
  const onBackendCrashRef = useRef(onBackendCrash);
  onBackendCrashRef.current = onBackendCrash;

  // Track current search term and options so findNext/findPrevious can reuse them
  const lastSearchTermRef = useRef("");
  const lastSearchOptsRef = useRef<Record<string, unknown>>({});

  const handleWsOutput = useCallback(() => {
    onOutputRef.current?.();
    if (!isAtBottomRef.current) {
      setShowNewOutputPill(true);
    }
  }, []);

  const handleScrollToBottom = useCallback(() => {
    terminalRef.current?.scrollToBottom();
    setShowNewOutputPill(false);
  }, []);

  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const hasConnectedRef = useRef(false);

  // Mirror connection state to parent via callback
  const connectionStateRef = useRef(connectionState);
  useEffect(() => {
    connectionStateRef.current = connectionState;
    if (connectionState === "connected") {
      hasConnectedRef.current = true;
    }
    onConnectionStateChange?.(connectionState, hasConnectedRef.current);
  }, [connectionState, onConnectionStateChange]);

  const onReconnectStateChangeRef = useRef(onReconnectStateChange);
  onReconnectStateChangeRef.current = onReconnectStateChange;

  const clearReconnectState = useCallback(() => {
    if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
    reconnectTimeoutRef.current = null;
    reconnectAttemptRef.current = 0;
    onReconnectStateChangeRef.current?.(null);
  }, []);

  // Reconnect handler — fetches scrollback before re-establishing WebSocket
  const handleReconnect = useCallback(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;
    if (!terminal || !fitAddon) return;

    clearReconnectState();
    reconnectCancelRef.current?.();
    reconnectCancelRef.current = null;
    wsCleanupRef.current?.();

    const doWsConnect = () => {
      const cleanupWs = connectWebSocket(
        sessionName,
        terminal,
        fitAddon,
        wsRef,
        setConnectionState,
        () => {
          reconnectCancelRef.current?.();
          reconnectCancelRef.current = null;
          clearReconnectState();
        },
        () => {
          if (reconnectCancelRef.current) return;
          const cancel = startAutoReconnect(
            () => {
              handleReconnect();
              return false;
            },
            (state: ReconnectState) => {
              reconnectAttemptRef.current = state.attempt;
              if (state.gaveUp) setConnectionState("error");
            },
          );
          reconnectCancelRef.current = cancel;
        },
        handleWsOutput,
        (reason: string) => onBackendCrashRef.current?.(reason),
        (data, sendToWs) => interceptorRef.current?.handleData(data, sendToWs),
      );
      wsCleanupRef.current = cleanupWs;
    };

    if (hasConnectedRef.current) {
      fetchScrollback(sessionName)
        .then(({ content }) => {
          if (content) {
            terminal.clear();
            terminal.write(content);
          }
        })
        .catch(() => {})
        .finally(() => doWsConnect());
    } else {
      doWsConnect();
    }
  }, [sessionName, clearReconnectState, handleWsOutput]);

  // Expose search methods and reconnect via imperative handle
  useImperativeHandle(
    ref,
    () => ({
      search(term, options) {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        lastSearchTermRef.current = term;
        const opts: Record<string, unknown> = {};
        if (options?.caseSensitive != null)
          opts.caseSensitive = options.caseSensitive;
        if (options?.wholeWord != null) opts.wholeWord = options.wholeWord;
        if (options?.regex != null) opts.regex = options.regex;
        lastSearchOptsRef.current = opts;
        if (!term) {
          addon.clearDecorations();
          onSearchResultChangeRef.current?.(null);
          return false;
        }
        return addon.findNext(term, {
          ...opts,
          decorations: SEARCH_DECORATIONS,
        });
      },
      findNext() {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        const term = lastSearchTermRef.current;
        if (!term) return false;
        return addon.findNext(term, {
          ...lastSearchOptsRef.current,
          decorations: SEARCH_DECORATIONS,
        });
      },
      findPrevious() {
        const addon = searchAddonRef.current;
        if (!addon) return false;
        const term = lastSearchTermRef.current;
        if (!term) return false;
        return addon.findPrevious(term, {
          ...lastSearchOptsRef.current,
          decorations: SEARCH_DECORATIONS,
        });
      },
      clearSearch() {
        searchAddonRef.current?.clearDecorations();
        lastSearchTermRef.current = "";
        lastSearchOptsRef.current = {};
        onSearchResultChangeRef.current?.(null);
      },
      reconnect() {
        reconnectCancelRef.current?.();
        reconnectCancelRef.current = null;
        wsCleanupRef.current?.();
        wsCleanupRef.current = null;
        handleReconnect();
      },
      pasteText(text: string) {
        terminalRef.current?.paste(text);
      },
      getSelection() {
        const sel = terminalRef.current?.getSelection() ?? "";
        return stripAnsi(sel);
      },
      hasSelection() {
        return terminalRef.current?.hasSelection() ?? false;
      },
      selectAll() {
        terminalRef.current?.selectAll();
      },
      focus() {
        terminalRef.current?.focus();
      },
    }),
    [handleReconnect],
  );

  // Stable refs for callbacks used inside terminal lifecycle effect
  const onCopyNotifyRef = useRef(onCopyNotify);
  onCopyNotifyRef.current = onCopyNotify;
  const onPasteRequestRef = useRef(onPasteRequest);
  onPasteRequestRef.current = onPasteRequest;
  const onSearchRequestRef = useRef(onSearchRequest);
  onSearchRequestRef.current = onSearchRequest;
  const onContextMenuRef = useRef(onContextMenu);
  onContextMenuRef.current = onContextMenu;
  const onTerminalFocusRef = useRef(onTerminalFocus);
  onTerminalFocusRef.current = onTerminalFocus;

  // Terminal lifecycle: create terminal and connect WebSocket
  useEffect(() => {
    const container = termRef.current;
    if (!container) return;

    let mounted = true;

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize,
      fontFamily,
      scrollback: 10000,
      smoothScrollDuration: 120,
      rightClickSelectsWord: true,
      theme: getTerminalTheme(),
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();
    const searchAddon = new SearchAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(webLinksAddon);
    terminal.loadAddon(searchAddon);

    // Try loading WebGL addon — fall back to canvas on failure
    let webglAddon: WebglAddon | null = null;
    try {
      webglAddon = new WebglAddon();
      webglAddon.onContextLoss(() => {
        console.warn(
          "TerminalInstance: WebGL context lost, falling back to canvas renderer",
        );
        webglAddon?.dispose();
        webglAddon = null;
      });
      terminal.loadAddon(webglAddon);
    } catch {
      // WebGL not available — canvas renderer is the default
      webglAddon = null;
    }

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;
    searchAddonRef.current = searchAddon;

    // Create slash command interceptor for this terminal instance
    const interceptor = new SlashCommandInterceptor(terminal);
    interceptorRef.current = interceptor;

    // Forward search result changes (N of M counter)
    const searchResultDisposable = searchAddon.onDidChangeResults(
      (e: { resultIndex: number; resultCount: number }) => {
        onSearchResultChangeRef.current?.(e);
      },
    );

    // Copy-on-select: strip ANSI codes and write clean text to clipboard
    let copyDebounce: ReturnType<typeof setTimeout> | undefined;
    let suppressCopyOnSelect = false;
    const selectionDisposable = terminal.onSelectionChange(() => {
      if (suppressCopyOnSelect) {
        suppressCopyOnSelect = false;
        return;
      }
      const text = terminal.getSelection();
      if (!text) return;
      const clean = stripAnsi(text);
      clearTimeout(copyDebounce);
      copyDebounce = setTimeout(() => {
        navigator.clipboard
          .writeText(clean)
          .then(() => onCopyNotifyRef.current?.())
          .catch(() => {});
      }, 100);
    });

    // Custom key handler for clipboard shortcuts and search
    terminal.attachCustomKeyEventHandler((e: KeyboardEvent) => {
      if (e.type !== "keydown") return true;

      // Ctrl+C — smart copy/interrupt
      if (
        e.ctrlKey &&
        !e.shiftKey &&
        !e.altKey &&
        !e.metaKey &&
        e.key === "c"
      ) {
        if (terminal.hasSelection()) {
          clearTimeout(copyDebounce); // cancel pending copy-on-select debounce
          const clean = stripAnsi(terminal.getSelection());
          navigator.clipboard
            .writeText(clean)
            .then(() => onCopyNotifyRef.current?.())
            .catch(() => {});
          terminal.clearSelection();
          return false; // suppress SIGINT
        }
        return true; // no selection — send SIGINT
      }

      // Ctrl+V — paste with multi-line check
      if (
        e.ctrlKey &&
        !e.shiftKey &&
        !e.altKey &&
        !e.metaKey &&
        e.key === "v"
      ) {
        e.preventDefault();
        onPasteRequestRef.current?.();
        return false;
      }

      // Ctrl+Shift+V — request paste from parent (legacy)
      if (e.ctrlKey && e.shiftKey && e.key === "V") {
        e.preventDefault();
        onPasteRequestRef.current?.();
        return false;
      }

      // Ctrl+Shift+F — request search from parent
      if (e.ctrlKey && e.shiftKey && e.key === "F") {
        e.preventDefault();
        onSearchRequestRef.current?.();
        return false;
      }

      // Cmd/Ctrl+1-9, Cmd/Ctrl+T, Cmd/Ctrl+W — suppress xterm processing
      // so the events bubble to the document handler in TerminalView
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.shiftKey &&
        !e.altKey &&
        (e.key === "t" || e.key === "w" || (e.key >= "1" && e.key <= "9"))
      ) {
        return false;
      }

      return true;
    });

    terminal.open(container);

    // Focus tracking for split view — fires when user clicks into terminal
    const textarea = terminal.textarea;
    const focusHandler = () => onTerminalFocusRef.current?.();
    textarea?.addEventListener("focus", focusHandler);

    // Right-click context menu — suppress copy-on-select for word selection
    const handleContextMenu = (e: MouseEvent) => {
      e.preventDefault();
      suppressCopyOnSelect = true;
      onContextMenuRef.current?.({
        x: e.clientX,
        y: e.clientY,
        hasSelection: terminal.hasSelection(),
      });
    };
    container.addEventListener("contextmenu", handleContextMenu);

    // Capture-phase paste listener for macOS Cmd+V
    const handleBrowserPaste = (e: ClipboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      onPasteRequestRef.current?.();
    };
    container.addEventListener("paste", handleBrowserPaste, { capture: true });

    // Track scroll position to show "New output" pill when scrolled up
    const scrollDisposable = terminal.onScroll(() => {
      const buffer = terminal.buffer.active;
      const atBottom = buffer.viewportY >= buffer.baseY - 1;
      isAtBottomRef.current = atBottom;
      if (atBottom) {
        setShowNewOutputPill(false);
      }
    });

    // Initial fit
    fitAddon.fit();

    const RECONNECT_TIMEOUT = 30_000;

    function doConnect(withScrollback = false): void {
      wsCleanupRef.current?.();
      const startWs = () => {
        const cleanup = connectWebSocket(
          sessionName,
          terminal,
          fitAddon,
          wsRef,
          setConnectionState,
          () => {
            reconnectCancelRef.current?.();
            reconnectCancelRef.current = null;
            clearReconnectState();
          },
          () => {
            if (!mounted || reconnectCancelRef.current) return;
            onReconnectStateChangeRef.current?.("reconnecting");
            if (!reconnectTimeoutRef.current) {
              reconnectTimeoutRef.current = setTimeout(() => {
                reconnectTimeoutRef.current = null;
                reconnectCancelRef.current?.();
                reconnectCancelRef.current = null;
                onReconnectStateChangeRef.current?.("expired");
                setConnectionState("error");
              }, RECONNECT_TIMEOUT);
            }
            const cancel = startAutoReconnect(
              () => {
                if (!mounted) return true;
                doConnect(true);
                return false;
              },
              (state: ReconnectState) => {
                reconnectAttemptRef.current = state.attempt;
                if (state.gaveUp) {
                  clearReconnectState();
                  onReconnectStateChangeRef.current?.("expired");
                  setConnectionState("error");
                }
              },
            );
            reconnectCancelRef.current = cancel;
          },
          handleWsOutput,
          (reason: string) => onBackendCrashRef.current?.(reason),
          (data, sendToWs) =>
            interceptorRef.current?.handleData(data, sendToWs),
        );
        wsCleanupRef.current = cleanup;
      };
      if (withScrollback && hasConnectedRef.current) {
        fetchScrollback(sessionName)
          .then(({ content }) => {
            if (mounted && content) {
              terminal.clear();
              terminal.write(content);
            }
          })
          .catch(() => {})
          .finally(() => {
            if (mounted) startWs();
          });
      } else {
        startWs();
      }
    }

    doConnect();

    // ResizeObserver with debounce
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (fitAddonRef.current && terminalRef.current) {
          fitAddonRef.current.fit();
          const currentWs = wsRef.current;
          if (currentWs && currentWs.readyState === WebSocket.OPEN) {
            currentWs.send(
              encodeResize(terminalRef.current.cols, terminalRef.current.rows),
            );
          }
        }
      }, 100);
    });
    observer.observe(container);

    return () => {
      mounted = false;

      clearTimeout(resizeTimer);
      clearTimeout(copyDebounce);
      observer.disconnect();
      container.removeEventListener("contextmenu", handleContextMenu);
      container.removeEventListener("paste", handleBrowserPaste, {
        capture: true,
      });
      textarea?.removeEventListener("focus", focusHandler);
      selectionDisposable.dispose();
      scrollDisposable.dispose();
      searchResultDisposable.dispose();

      reconnectCancelRef.current?.();
      reconnectCancelRef.current = null;

      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = null;
      }

      wsCleanupRef.current?.();
      wsCleanupRef.current = null;

      interceptorRef.current?.dispose();
      interceptorRef.current = null;

      webglAddon?.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;

      setConnectionState("disconnected");
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fontSize/fontFamily handled by separate sync effect
  }, [sessionName]);

  // Re-fit when tab becomes active (xterm can't measure when hidden)
  useEffect(() => {
    if (!isActive) return;
    const frame = requestAnimationFrame(() => {
      if (fitAddonRef.current) {
        fitAddonRef.current.fit();
        const currentWs = wsRef.current;
        if (
          currentWs &&
          currentWs.readyState === WebSocket.OPEN &&
          terminalRef.current
        ) {
          currentWs.send(
            encodeResize(terminalRef.current.cols, terminalRef.current.rows),
          );
        }
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [isActive]);

  // Dynamic font changes without terminal recreation
  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) return;
    terminal.options.fontSize = fontSize;
    terminal.options.fontFamily = fontFamily;
    fitAddonRef.current?.fit();
  }, [fontSize, fontFamily]);

  // Sync terminal theme when data-theme attribute changes
  useEffect(() => {
    const t = terminalRef.current;
    if (!t) return;
    const obs = new MutationObserver(() => {
      t.options.theme = getTerminalTheme();
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => obs.disconnect();
  }, [sessionName]);

  return (
    <div className={styles.wrapper}>
      <div
        className={styles.container}
        ref={termRef}
        role="application"
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
