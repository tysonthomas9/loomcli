// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  getAgentLogArchive,
  getAgentTerminalInfo,
  getAgentTerminalToken,
  getAgentTerminalWsUrl,
} from "@/api";

import { useAgentTerminalLogs } from "../useAgentTerminalLogs";

vi.mock("@/api", () => ({
  getAgentLogArchive: vi.fn(),
  getAgentTerminalInfo: vi.fn(),
  getAgentTerminalToken: vi.fn(),
  getAgentTerminalWsUrl: vi.fn(),
}));
vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});
vi.mock("@/utils/reconnectBackoff", () => ({
  calculateBackoffDelay: vi.fn(() => 1),
  DEFAULT_RECONNECT_CONFIG: {
    baseDelay: 1,
    maxDelay: 1,
    maxAttempts: 2,
    jitterFactor: 0,
  },
}));

const mockGetAgentLogArchive = vi.mocked(getAgentLogArchive);
const mockGetAgentTerminalInfo = vi.mocked(getAgentTerminalInfo);
const mockGetAgentTerminalToken = vi.mocked(getAgentTerminalToken);
const mockGetAgentTerminalWsUrl = vi.mocked(getAgentTerminalWsUrl);

class MockWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: MockWebSocket[] = [];

  readonly url: string;
  readyState = MockWebSocket.CONNECTING;
  binaryType = "";
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  send = vi.fn();

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  close = vi.fn((_code?: number, _reason?: string) => {
    if (this.readyState === MockWebSocket.CLOSED) return;
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.({} as CloseEvent);
  });

  triggerOpen(): void {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  triggerMessage(data: string | ArrayBuffer): void {
    this.onmessage?.({ data } as MessageEvent);
  }

  triggerClose(): void {
    this.close();
  }
}

function createDeferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function decodeChunks(chunks: Array<{ chunk: Uint8Array }>): string {
  const decoder = new TextDecoder();
  return chunks.map((entry) => decoder.decode(entry.chunk)).join("");
}

async function flushAsyncWork(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useAgentTerminalLogs", () => {
  let originalWebSocket: typeof globalThis.WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket;
    MockWebSocket.instances = [];

    vi.useFakeTimers();
    vi.clearAllMocks();

    mockGetAgentTerminalWsUrl.mockImplementation(
      (agentName: string, token: string) =>
        `ws://localhost/${agentName}?token=${token}`,
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    globalThis.WebSocket = originalWebSocket;
  });

  it("falls back to archive snapshot after tmux retry exhaustion while preserving existing output", async () => {
    mockGetAgentTerminalInfo
      .mockResolvedValueOnce("tmux")
      .mockResolvedValueOnce("archive");
    mockGetAgentTerminalToken.mockResolvedValue("token-123");
    mockGetAgentLogArchive.mockResolvedValue({
      lines: ["archive line"],
      lineCount: 1,
      startLine: 1,
    });

    const { result } = renderHook(() =>
      useAgentTerminalLogs({
        agentName: "ember",
        enabled: true,
      }),
    );

    await flushAsyncWork();
    expect(MockWebSocket.instances).toHaveLength(1);

    const initialSocket = MockWebSocket.instances[0];
    expect(initialSocket).toBeDefined();
    act(() => {
      initialSocket?.triggerOpen();
      initialSocket?.triggerMessage("live output\n");
    });

    expect(decodeChunks(result.current.chunks)).toContain("live output");

    act(() => {
      initialSocket?.triggerClose();
    });

    expect(result.current.state).toBe("reconnecting");
    expect(decodeChunks(result.current.chunks)).toContain("live output");

    // maxAttempts is mocked to 2 for deterministic coverage.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(MockWebSocket.instances).toHaveLength(2);
    act(() => {
      MockWebSocket.instances[1]?.triggerClose();
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(MockWebSocket.instances).toHaveLength(3);
    act(() => {
      MockWebSocket.instances[2]?.triggerClose();
    });
    await flushAsyncWork();

    expect(result.current.mode).toBe("archive");
    expect(result.current.state).toBe("connected");

    expect(mockGetAgentTerminalInfo).toHaveBeenCalledTimes(2);
    expect(mockGetAgentLogArchive).toHaveBeenCalledWith(
      "test-ws-id",
      "ember",
      500,
    );
    expect(decodeChunks(result.current.chunks)).toContain("archive line");
  });

  it("auto-returns to tmux mode from archive snapshot when a live session appears", async () => {
    mockGetAgentTerminalInfo
      .mockResolvedValueOnce("archive")
      .mockResolvedValue("tmux");
    mockGetAgentLogArchive.mockResolvedValue({
      lines: ["snapshot"],
      lineCount: 1,
      startLine: 1,
    });
    mockGetAgentTerminalToken.mockResolvedValue("token-abc");

    const { result } = renderHook(() =>
      useAgentTerminalLogs({
        agentName: "ember",
        enabled: true,
      }),
    );

    await flushAsyncWork();
    expect(result.current.mode).toBe("archive");
    expect(result.current.state).toBe("connected");
    expect(MockWebSocket.instances).toHaveLength(0);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    await flushAsyncWork();

    expect(MockWebSocket.instances).toHaveLength(1);
    expect(result.current.mode).toBe("tmux");

    act(() => {
      MockWebSocket.instances[0]?.triggerOpen();
    });
    expect(result.current.state).toBe("connected");
    expect(result.current.mode).toBe("tmux");
  });

  it("dedupes concurrent archive top-load requests", async () => {
    mockGetAgentTerminalInfo.mockResolvedValue("archive");
    const deferredLoad = createDeferred<{
      lines: string[];
      lineCount: number;
      startLine: number;
    }>();

    mockGetAgentLogArchive
      .mockResolvedValueOnce({
        lines: ["line-200", "line-201"],
        lineCount: 2,
        startLine: 200,
      })
      .mockImplementationOnce(() => deferredLoad.promise)
      .mockResolvedValueOnce({
        lines: ["line-196", "line-197"],
        lineCount: 2,
        startLine: 196,
      });

    const { result } = renderHook(() =>
      useAgentTerminalLogs({
        agentName: "ember",
        enabled: true,
      }),
    );

    await flushAsyncWork();
    await flushAsyncWork();
    expect(result.current.mode).toBe("archive");
    expect(result.current.hasMoreLines).toBe(true);

    act(() => {
      void result.current.loadOlderLogs();
      void result.current.loadOlderLogs();
    });
    await flushAsyncWork();

    expect(mockGetAgentLogArchive).toHaveBeenCalledTimes(2);
    expect(mockGetAgentLogArchive).toHaveBeenNthCalledWith(
      2,
      "test-ws-id",
      "ember",
      500,
      200,
    );
    expect(result.current.isLoadingMore).toBe(true);

    await act(async () => {
      deferredLoad.resolve({
        lines: ["line-198", "line-199"],
        lineCount: 2,
        startLine: 198,
      });
      await Promise.resolve();
    });

    expect(result.current.isLoadingMore).toBe(false);

    act(() => {
      void result.current.loadOlderLogs();
    });
    await flushAsyncWork();

    expect(mockGetAgentLogArchive).toHaveBeenCalledTimes(3);
    expect(mockGetAgentLogArchive).toHaveBeenNthCalledWith(
      3,
      "test-ws-id",
      "ember",
      500,
      198,
    );
  });
});
