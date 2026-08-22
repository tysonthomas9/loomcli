/**
 * @vitest-environment jsdom
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  CLIENT_FRAME_VECTORS,
  SERVER_FRAME_VECTORS,
} from "./terminalProtocolVectors";

const shared = vi.hoisted(() => {
  let resolveToken: ((value: { token: string }) => void) | null = null;
  let rejectToken: ((error: Error) => void) | null = null;
  const getMock = vi.fn(
    () =>
      new Promise<{ token: string }>((resolve, reject) => {
        resolveToken = resolve;
        rejectToken = reject;
      }),
  );
  return {
    getMock,
    get resolveToken() {
      return resolveToken;
    },
    get rejectToken() {
      return rejectToken;
    },
  };
});

vi.mock("@/hooks/api", () => ({
  get: shared.getMock,
  getWsBaseUrl: () => "ws://localhost",
  wsUrl: (workspaceId: string, path: string) =>
    "/api/workspaces/" + encodeURIComponent(workspaceId) + path,
}));

function bufferFromHex(hex: string): ArrayBuffer {
  const pairs = hex.match(/../g) ?? [];
  return Uint8Array.from(pairs, (pair) => Number.parseInt(pair, 16)).buffer;
}

function toHex(value: unknown): string {
  if (!(value instanceof ArrayBuffer)) throw new Error("expected ArrayBuffer");
  return Array.from(new Uint8Array(value), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}

const initialStateForConnection =
  SERVER_FRAME_VECTORS.initialState.slice(0, 40) +
  "0000000000000008" +
  SERVER_FRAME_VECTORS.initialState.slice(56);

class MockWebSocket {
  static CONNECTING = 0 as const;
  static OPEN = 1 as const;
  static CLOSING = 2 as const;
  static CLOSED = 3 as const;
  static instances: MockWebSocket[] = [];

  readonly url: string;
  readonly requestedProtocols: string[];
  protocol = "loom-terminal.v1";
  readyState: number = MockWebSocket.CONNECTING;
  binaryType = "blob";
  close = vi.fn(() => {
    this.readyState = MockWebSocket.CLOSED;
  });
  send = vi.fn();
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    this.requestedProtocols =
      typeof protocols === "string" ? [protocols] : (protocols ?? []);
    MockWebSocket.instances.push(this);
  }

  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  simulateMessage(hex: string): void {
    this.onmessage?.(new MessageEvent("message", { data: bufferFromHex(hex) }));
  }

  simulateTextMessage(data: string): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  simulateClose(code = 1000, reason = ""): void {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.(new CloseEvent("close", { code, reason }));
  }

  simulateError(): void {
    this.onerror?.(new Event("error"));
  }
}

function makeCallbacks() {
  const order: string[] = [];
  return {
    order,
    callbacks: {
      write: vi.fn(() => order.push("write")),
      reset: vi.fn(async () => {
        order.push("reset");
      }),
      setConnectionState: vi.fn(),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onOutput: vi.fn(),
      onBackendCrash: vi.fn(),
      onSessionKilled: vi.fn(),
      onInitialState: vi.fn(() => order.push("initial")),
      onCanonicalResize: vi.fn(() => order.push("resize")),
      onNotice: vi.fn(() => order.push("notice")),
    },
  };
}

async function flushMessages(delay = 0): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, delay));
}

describe("connectWebSocket", () => {
  let connectWebSocket: typeof import("../terminalConnection").connectWebSocket;
  const originalWebSocket = globalThis.WebSocket;

  beforeEach(async () => {
    vi.resetModules();
    MockWebSocket.instances = [];
    shared.getMock.mockClear();
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket;
    ({ connectWebSocket } = await import("../terminalConnection"));
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
  });

  async function connect(
    callbacks = makeCallbacks(),
    initialSize?: { cols: number; rows: number },
  ) {
    const socketIndex = MockWebSocket.instances.length;
    const wsRef = { current: null as WebSocket | null };
    const handle = connectWebSocket(
      "ws1",
      "session1",
      wsRef,
      callbacks.callbacks,
      initialSize,
    );
    shared.resolveToken?.({ token: "tok" });
    await vi.waitFor(() =>
      expect(MockWebSocket.instances).toHaveLength(socketIndex + 1),
    );
    const ws = MockWebSocket.instances[socketIndex];
    if (!ws) throw new Error("WebSocket was not created");
    return { ...callbacks, handle, ws, wsRef };
  }

  it("requests and verifies the loom-terminal.v1 subprotocol", async () => {
    const { callbacks, ws } = await connect();
    expect(ws.requestedProtocols).toEqual(["loom-terminal.v1"]);
    expect(ws.binaryType).toBe("arraybuffer");

    ws.simulateOpen();
    expect(callbacks.setConnectionState).toHaveBeenLastCalledWith("connecting");
    expect(callbacks.onConnected).not.toHaveBeenCalled();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();
    expect(callbacks.setConnectionState).toHaveBeenLastCalledWith("connected");
    expect(callbacks.onConnected).toHaveBeenCalledOnce();
  });

  it("rejects a socket that did not negotiate the subprotocol", async () => {
    const { callbacks, ws } = await connect();
    ws.protocol = "";
    ws.simulateOpen();

    expect(ws.close).toHaveBeenCalledWith(
      1002,
      "terminal subprotocol was not negotiated",
    );
    expect(callbacks.setConnectionState).toHaveBeenCalledWith("error");
    expect(callbacks.onConnected).not.toHaveBeenCalled();
  });

  it("applies initial metadata, reset, then snapshot bytes in order", async () => {
    const { callbacks, order, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();

    expect(callbacks.onInitialState).toHaveBeenCalledWith({
      cols: 80,
      rows: 24,
      retainedLines: 42,
    });
    expect(order).toEqual(["initial", "reset", "write"]);
    expect(callbacks.write).toHaveBeenCalledWith(
      new Uint8Array([0x1b, 0x5b, 0x33, 0x31, 0x6d, 0x68, 0x69]),
    );
  });

  it("holds frames received while reset drains until after the snapshot", async () => {
    const { callbacks, ws } = await connect();
    let releaseReset!: () => void;
    callbacks.reset = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          releaseReset = resolve;
        }),
    );
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    ws.simulateMessage(SERVER_FRAME_VECTORS.output);
    await flushMessages();

    expect(callbacks.write).not.toHaveBeenCalled();
    releaseReset();
    await flushMessages(25);

    expect(callbacks.write).toHaveBeenNthCalledWith(
      1,
      new Uint8Array([0x1b, 0x5b, 0x33, 0x31, 0x6d, 0x68, 0x69]),
    );
    expect(callbacks.write).toHaveBeenNthCalledWith(
      2,
      new Uint8Array([0x68, 0x69, 0x0a]),
    );
  });

  it("requires initial_state to be the first binary frame", async () => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(SERVER_FRAME_VECTORS.output);
    ws.simulateMessage(
      SERVER_FRAME_VECTORS.output.replace(
        "0000000000000009",
        "000000000000000a",
      ),
    );
    await flushMessages();

    expect(ws.close).toHaveBeenCalledWith(
      1002,
      "first terminal frame must be initial_state",
    );
    expect(callbacks.setConnectionState).toHaveBeenCalledWith("error");
  });

  it("rejects an unsupported initial-state encoding", async () => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    const unsupported = initialStateForConnection.replace(
      "787465726d2d76742f31",
      "756e6b6e6f776e2d7674",
    );
    ws.simulateMessage(unsupported);
    await flushMessages();

    expect(ws.close).toHaveBeenCalledWith(
      1002,
      "unsupported terminal state encoding",
    );
    expect(callbacks.setConnectionState).toHaveBeenCalledWith("error");
  });

  it("rejects text and malformed binary frames", async () => {
    const first = await connect();
    first.ws.simulateOpen();
    first.ws.simulateTextMessage("raw output");
    await flushMessages();
    expect(first.ws.close).toHaveBeenCalledWith(
      1002,
      "terminal frames must be binary",
    );

    first.handle.dispose();
    const second = await connect();
    second.ws.simulateOpen();
    second.ws.simulateMessage("0001");
    await flushMessages();
    expect(second.ws.close).toHaveBeenCalledWith(
      1002,
      expect.stringContaining("header"),
    );
  });

  it("routes output, canonical resize, and notice frames", async () => {
    const { callbacks, order, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();
    callbacks.write.mockClear();
    callbacks.onOutput.mockClear();
    order.length = 0;

    ws.simulateMessage(SERVER_FRAME_VECTORS.output);
    ws.simulateMessage(
      SERVER_FRAME_VECTORS.output.replace(
        "0000000000000009",
        "000000000000000a",
      ),
    );
    ws.simulateMessage(
      SERVER_FRAME_VECTORS.resize.replace(
        "000000000000000a",
        "000000000000000b",
      ),
    );
    ws.simulateMessage(
      SERVER_FRAME_VECTORS.notice.replace(
        "000000000000000b",
        "000000000000000c",
      ),
    );
    await flushMessages(25);

    expect(callbacks.write).toHaveBeenCalledOnce();
    expect(callbacks.write).toHaveBeenCalledWith(
      new Uint8Array([0x68, 0x69, 0x0a, 0x68, 0x69, 0x0a]),
    );
    expect(callbacks.onOutput).toHaveBeenCalledOnce();
    expect(callbacks.onCanonicalResize).toHaveBeenCalledWith(120, 40);
    expect(callbacks.onNotice).toHaveBeenCalledWith({
      code: "input_dropped",
      message: "Input dropped",
    });
    expect(order).toEqual(["write", "resize", "notice"]);
  });

  it("uses a close frame reason when the WebSocket reason is empty", async () => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    ws.simulateMessage(
      SERVER_FRAME_VECTORS.close.replace(
        "000000000000000c",
        "0000000000000009",
      ),
    );
    await flushMessages();
    ws.simulateClose(4001);

    expect(callbacks.onBackendCrash).toHaveBeenCalledWith("exited");
  });

  it("closes and requests an immediate reconnect on generation mismatch", async () => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();
    const changedGeneration = SERVER_FRAME_VECTORS.output.replace(
      "000102030405060708090a0b0c0d0e0f",
      "ff0102030405060708090a0b0c0d0e0f",
    );
    ws.simulateMessage(changedGeneration);
    await flushMessages();

    expect(ws.close).toHaveBeenCalledWith(1002, "terminal generation changed");
    expect(callbacks.setConnectionState).toHaveBeenCalledWith("disconnected");
    expect(callbacks.onDisconnected).toHaveBeenCalledWith("immediate");
  });

  it("encodes input, resize_request, and focus with the pinned generation", async () => {
    const { handle, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();

    handle.sendInput("ls\n");
    handle.sendResizeRequest(120, 40);
    handle.sendFocus();

    expect(ws.send.mock.calls.map(([frame]) => toHex(frame))).toEqual([
      CLIENT_FRAME_VECTORS.input,
      CLIENT_FRAME_VECTORS.resizeRequest,
      CLIENT_FRAME_VECTORS.focus,
    ]);
  });

  it("holds focus and resize requests until initial_state pins generation", async () => {
    const { handle, ws } = await connect();
    ws.simulateOpen();
    handle.sendFocus();
    handle.sendResizeRequest(120, 40);
    expect(ws.send).not.toHaveBeenCalled();

    ws.simulateMessage(initialStateForConnection);
    await flushMessages();
    expect(ws.send.mock.calls.map(([frame]) => toHex(frame))).toEqual([
      CLIENT_FRAME_VECTORS.resizeRequest,
      CLIENT_FRAME_VECTORS.focus,
    ]);
  });

  it("queues input sent before initial_state and flushes it after the snapshot", async () => {
    const { handle, ws } = await connect();
    ws.simulateOpen();
    handle.sendInput("typed before snapshot\n");
    expect(ws.send).not.toHaveBeenCalled();

    ws.simulateMessage(initialStateForConnection);
    await flushMessages();

    expect(ws.send).toHaveBeenCalledOnce();
    expect(toHex(ws.send.mock.calls[0]?.[0])).toContain(
      "7479706564206265666f726520736e617073686f74",
    );
  });

  it("treats a sequence gap as an immediate resnapshot reconnect", async () => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    ws.simulateMessage(initialStateForConnection);
    await flushMessages();
    const gap = SERVER_FRAME_VECTORS.output.replace(
      "0000000000000009",
      "000000000000000b",
    );
    ws.simulateMessage(gap);
    await flushMessages();

    expect(ws.close).toHaveBeenCalledWith(
      1002,
      "terminal sequence gap or duplicate",
    );
    expect(callbacks.onDisconnected).toHaveBeenCalledWith("immediate");
  });

  it.each([
    [4003, "immediate"],
    [4004, "backoff"],
    [1001, "backoff"],
    [1008, "backoff"],
  ] as const)("classifies close %i as %s reconnect", async (code, policy) => {
    const { callbacks, ws } = await connect();
    ws.simulateOpen();
    ws.simulateClose(code, "retry");

    expect(callbacks.setConnectionState).toHaveBeenCalledWith("disconnected");
    expect(callbacks.onDisconnected).toHaveBeenCalledWith(policy);
  });

  it.each([
    [4002, "session killed"],
    [1000, ""],
  ] as const)(
    "keeps close %i as a terminal session end",
    async (code, reason) => {
      const { callbacks, ws } = await connect();
      ws.simulateOpen();
      ws.simulateClose(code, reason);

      expect(callbacks.setConnectionState).toHaveBeenCalledWith(
        "session_ended",
      );
      expect(callbacks.onSessionKilled).toHaveBeenCalledOnce();
      expect(callbacks.onDisconnected).not.toHaveBeenCalled();
    },
  );

  it("keeps backend exit and workspace unavailable behavior", async () => {
    const crash = await connect();
    crash.ws.simulateOpen();
    crash.ws.simulateClose(4001, "backend process exited");
    expect(crash.callbacks.setConnectionState).toHaveBeenCalledWith("crashed");
    expect(crash.callbacks.onBackendCrash).toHaveBeenCalledWith(
      "backend process exited",
    );

    crash.handle.dispose();
    const unavailable = await connect();
    unavailable.ws.simulateOpen();
    unavailable.ws.simulateClose(1001, "workspace unavailable");
    expect(unavailable.callbacks.setConnectionState).toHaveBeenCalledWith(
      "error",
    );
    expect(unavailable.callbacks.onSessionKilled).toHaveBeenCalledOnce();
    expect(unavailable.callbacks.onDisconnected).not.toHaveBeenCalled();
  });

  it("includes initial size and token in the workspace terminal URL", async () => {
    const { ws } = await connect(makeCallbacks(), { cols: 132, rows: 40 });
    expect(ws.url).toContain("session=session1");
    expect(ws.url).toContain("cols=132");
    expect(ws.url).toContain("rows=40");
    expect(ws.url).toContain("token=tok");
  });

  it("backs off when token fetching fails", async () => {
    const callbacks = makeCallbacks();
    const wsRef = { current: null as WebSocket | null };
    connectWebSocket("ws1", "session1", wsRef, callbacks.callbacks);
    shared.rejectToken?.(new Error("network error"));
    await flushMessages();
    expect(MockWebSocket.instances).toHaveLength(0);
    expect(callbacks.callbacks.setConnectionState).toHaveBeenCalledWith(
      "disconnected",
    );
    expect(callbacks.callbacks.onDisconnected).toHaveBeenCalledWith("backoff");
  });

  it("disposes idempotently and ignores late token resolution", async () => {
    const callbacks = makeCallbacks();
    const wsRef = { current: null as WebSocket | null };
    const handle = connectWebSocket(
      "ws1",
      "session1",
      wsRef,
      callbacks.callbacks,
    );
    handle.dispose();
    handle.dispose();
    shared.resolveToken?.({ token: "tok" });
    await flushMessages();

    expect(MockWebSocket.instances).toHaveLength(0);
    expect(wsRef.current).toBeNull();
  });

  it("closes and clears the socket on browser error", async () => {
    const { callbacks, ws, wsRef } = await connect();
    ws.simulateOpen();
    ws.simulateError();

    expect(ws.close).toHaveBeenCalledOnce();
    expect(wsRef.current).toBeNull();
    expect(callbacks.onDisconnected).toHaveBeenCalledWith("backoff");
  });
});
