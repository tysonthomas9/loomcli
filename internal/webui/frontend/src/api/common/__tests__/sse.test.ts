/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../client";
import { fetchSseToken, getSSEUrl, WorkspaceSSEClient } from "../sse";
import type { MutationPayload, SseTokenResult } from "../sse";

const mockGet = vi.fn();
vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
  return {
    ...actual,
    get: (...args: unknown[]) => mockGet(...args),
  };
});

type StreamPlan =
  | { kind: "response"; status: number; contentType: string }
  | { kind: "network-error"; error: Error };

interface StreamRequest {
  url: string;
  headers: Headers;
  signal: AbortSignal | null;
  aborted: boolean;
  push: (frame: string) => void;
  close: () => void;
  fail: (error?: Error) => void;
}

const encoder = new TextEncoder();
let streamPlans: StreamPlan[] = [];
let streamRequests: StreamRequest[] = [];
let originalWindowFetch: typeof window.fetch;
let originalDocumentHidden: PropertyDescriptor | undefined;

function queueResponse(
  status = 200,
  contentType = "text/event-stream; charset=utf-8",
): void {
  streamPlans.push({ kind: "response", status, contentType });
}

function queueNetworkError(message = "network down"): void {
  streamPlans.push({ kind: "network-error", error: new Error(message) });
}

const mockStreamFetch = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const plan = streamPlans.shift() ?? {
      kind: "response",
      status: 200,
      contentType: "text/event-stream; charset=utf-8",
    };
    if (plan.kind === "network-error") throw plan.error;

    let controller: ReadableStreamDefaultController<Uint8Array>;
    let settled = false;
    const body = new ReadableStream<Uint8Array>({
      start(streamController) {
        controller = streamController;
      },
    });
    const signal = init?.signal ?? null;
    const request: StreamRequest = {
      url: String(input),
      headers: new Headers(init?.headers),
      signal,
      aborted: signal?.aborted ?? false,
      push(frame) {
        if (!settled) controller.enqueue(encoder.encode(frame));
      },
      close() {
        if (!settled) {
          settled = true;
          controller.close();
        }
      },
      fail(error = new Error("stream failed")) {
        if (!settled) {
          settled = true;
          controller.error(error);
        }
      },
    };
    signal?.addEventListener("abort", () => {
      request.aborted = true;
      request.fail(new DOMException("Aborted", "AbortError"));
    });
    streamRequests.push(request);

    return new Response(body, {
      status: plan.status,
      headers: { "content-type": plan.contentType },
    });
  },
);

async function flush(): Promise<void> {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

async function expectRequestCount(count: number): Promise<void> {
  await flush();
  expect(streamRequests).toHaveLength(count);
}

function pushConnected(request = streamRequests.at(-1)): void {
  request?.push('event: connected\ndata: {"clientId":1}\n\n');
}

function pushMutation(
  mutation: MutationPayload,
  id: string,
  request = streamRequests.at(-1),
): void {
  request?.push(
    `id: ${id}\nevent: mutation\ndata: ${JSON.stringify(mutation)}\n\n`,
  );
}

function pushResync(
  id: string,
  reason: "cap" | "error" | "expired" | "overflow",
  request = streamRequests.at(-1),
): void {
  request?.push(
    `id: ${id}\nevent: resync\ndata: ${JSON.stringify({ reason })}\n\n`,
  );
}

describe("WorkspaceSSEClient", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    streamPlans = [];
    streamRequests = [];
    mockGet.mockReset();
    mockGet.mockResolvedValue({ token: "test-token" });
    mockStreamFetch.mockClear();
    originalWindowFetch = window.fetch;
    originalDocumentHidden = Object.getOwnPropertyDescriptor(
      document,
      "hidden",
    );
    window.fetch = mockStreamFetch as typeof window.fetch;
  });

  afterEach(() => {
    window.fetch = originalWindowFetch;
    if (originalDocumentHidden) {
      Object.defineProperty(document, "hidden", originalDocumentHidden);
    } else {
      Reflect.deleteProperty(document, "hidden");
    }
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("resolves connect after starting the loop without waiting for open", async () => {
    let resolveToken!: (result: SseTokenResult) => void;
    const fetchToken = vi.fn(
      () =>
        new Promise<SseTokenResult>((resolve) => {
          resolveToken = resolve;
        }),
    );
    const client = new WorkspaceSSEClient("test-ws", { fetchToken });

    await expect(client.connect()).resolves.toBeUndefined();

    expect(client.getState()).toBe("connecting");
    expect(fetchToken).toHaveBeenCalledOnce();
    expect(streamRequests).toHaveLength(0);

    resolveToken({ kind: "token", token: "ready" });
    await expectRequestCount(1);
    expect(client.getState()).toBe("connected");
    client.disconnect();
  });

  it("connects, opens the stream, and reports the connected frame", async () => {
    const states = vi.fn();
    const onConnected = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onStateChange: states,
      onConnected,
    });

    await client.connect();
    expect(client.getState()).toBe("connecting");
    await expectRequestCount(1);
    expect(client.getState()).toBe("connected");
    expect(streamRequests[0].url).toContain("/api/workspaces/test-ws/events");

    pushConnected();
    await flush();

    expect(onConnected).toHaveBeenCalledOnce();
    expect(states.mock.calls.map(([state]) => state)).toEqual([
      "connecting",
      "connected",
    ]);
    client.disconnect();
  });

  it("delivers named mutation events and tracks opaque event ids", async () => {
    const onMutation = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { onMutation });
    const mutation: MutationPayload = {
      type: "status",
      entity_type: "agent",
      entity_id: "agent-alpha",
      action: "agent.status",
      timestamp: "2026-09-02T12:00:00Z",
    };

    await client.connect();
    await expectRequestCount(1);
    pushMutation(mutation, "opaque-cursor-1");
    await flush();

    expect(onMutation).toHaveBeenCalledWith(mutation);
    expect(client.getLastEventId()).toBe("opaque-cursor-1");
    client.disconnect();
  });

  it("advances its cursor and reports resync without delivering a mutation", async () => {
    const onMutation = vi.fn();
    const onResync = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onMutation,
      onResync,
    });

    await client.connect("c1.from", ["repo-a"]);
    await expectRequestCount(1);
    pushResync("c1.to", "overflow");
    await flush();

    expect(client.getLastEventId()).toBe("c1.to");
    expect(onResync).toHaveBeenCalledOnce();
    expect(onResync).toHaveBeenCalledWith({
      from: "c1.from",
      to: "c1.to",
      reason: "overflow",
    });
    expect(onMutation).not.toHaveBeenCalled();
    client.disconnect();
  });

  it.each([
    ["an unrecognized reason", JSON.stringify({ reason: "future-reason" })],
    ["non-JSON data", "{oops"],
  ])(
    "repairs with an error reason when a resync carries %s",
    async (_description, data) => {
      const onMutation = vi.fn();
      const onResync = vi.fn();
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const client = new WorkspaceSSEClient("test-ws", {
        onMutation,
        onResync,
      });

      await client.connect("c1.from");
      await expectRequestCount(1);
      streamRequests[0].push(`id: c1.to\nevent: resync\ndata: ${data}\n\n`);
      await flush();

      expect(client.getLastEventId()).toBe("c1.to");
      expect(onResync).toHaveBeenCalledOnce();
      expect(onResync).toHaveBeenCalledWith({
        from: "c1.from",
        to: "c1.to",
        reason: "error",
      });
      expect(onMutation).not.toHaveBeenCalled();
      expect(warn).toHaveBeenCalledWith(
        "[SSE] Received malformed resync event",
      );
      client.disconnect();
    },
  );

  it("keeps parsing messages when a mutation callback throws", async () => {
    const callbackError = new Error("listener failed");
    const onMutation = vi.fn(() => {
      throw callbackError;
    });
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const client = new WorkspaceSSEClient("test-ws", { onMutation });
    const mutation: MutationPayload = {
      type: "update",
      issue_id: "issue-1",
      timestamp: "2026-09-02T12:00:00Z",
    };

    await client.connect();
    await expectRequestCount(1);
    pushMutation(mutation, "cursor-1");
    pushMutation(mutation, "cursor-2");
    await flush();

    expect(onMutation).toHaveBeenCalledTimes(2);
    expect(client.getLastEventId()).toBe("cursor-2");
    expect(client.getState()).toBe("connected");
    expect(client.getReconnectAttempts()).toBe(0);
    expect(error).toHaveBeenCalledWith(
      "[SSE] onMutation callback threw:",
      callbackError,
    );
    client.disconnect();
  });

  it("delivers at most one connected callback per open stream", async () => {
    const onConnected = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { onConnected });

    await client.connect();
    await expectRequestCount(1);
    pushConnected(streamRequests[0]);
    pushConnected(streamRequests[0]);
    await flush();

    expect(onConnected).toHaveBeenCalledOnce();

    streamRequests[0].fail();
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    pushConnected(streamRequests[1]);
    await flush();

    expect(onConnected).toHaveBeenCalledTimes(2);
    client.disconnect();
  });

  it("stops on malformed mutation JSON and reconnects from the prior checkpoint", async () => {
    const onMutation = vi.fn();
    const onError = vi.fn();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const client = new WorkspaceSSEClient("test-ws", { onMutation, onError });

    await client.connect("valid-checkpoint");
    await expectRequestCount(1);
    streamRequests[0].push("id: bad-id\nevent: mutation\ndata: {oops\n\n");
    await flush();

    expect(onMutation).not.toHaveBeenCalled();
    expect(client.getLastEventId()).toBe("valid-checkpoint");
    expect(client.getState()).toBe("disconnected");
    expect(onError).toHaveBeenCalledWith("Malformed SSE mutation payload");
    expect(warn).toHaveBeenCalledWith(
      "[SSE] Received malformed mutation event",
    );
    await client.connect();
    await expectRequestCount(2);
    expect(streamRequests[1].headers.get("Last-Event-ID")).toBe(
      "valid-checkpoint",
    );
    client.disconnect();
  });

  it("recovers from a token 502 on the next backed-off attempt", async () => {
    mockGet
      .mockRejectedValueOnce(new ApiError(502, "Bad Gateway"))
      .mockResolvedValueOnce({ token: "fresh-token" });
    const onReconnect = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { onReconnect });

    await client.connect();
    await flush();

    expect(client.getState()).toBe("reconnecting");
    expect(client.getReconnectAttempts()).toBe(1);
    expect(streamRequests).toHaveLength(0);
    expect(onReconnect).toHaveBeenCalledWith(1);

    await vi.advanceTimersByTimeAsync(999);
    expect(streamRequests).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    await expectRequestCount(1);

    expect(streamRequests[0].url).toContain("token=fresh-token");
    expect(client.getState()).toBe("connected");
    expect(client.getReconnectAttempts()).toBe(0);
    expect(onReconnect).toHaveBeenLastCalledWith(0);
    client.disconnect();
  });

  it("opens a tokenless stream when the token endpoint returns 404", async () => {
    mockGet.mockRejectedValueOnce(new ApiError(404, "Not Found"));
    const onError = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { onError });

    await client.connect();
    await expectRequestCount(1);

    expect(new URL(streamRequests[0].url).searchParams.has("token")).toBe(
      false,
    );
    expect(client.getState()).toBe("connected");
    expect(onError).not.toHaveBeenCalled();
    client.disconnect();
  });

  it("opens a tokenless stream when fetchToken resolves disabled", async () => {
    const fetchToken = vi
      .fn<() => Promise<SseTokenResult>>()
      .mockResolvedValue({ kind: "disabled" });
    const onError = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { fetchToken, onError });

    await client.connect();
    await expectRequestCount(1);

    expect(new URL(streamRequests[0].url).searchParams.has("token")).toBe(
      false,
    );
    expect(client.getState()).toBe("connected");
    expect(onError).not.toHaveBeenCalled();
    client.disconnect();
  });

  it("opens a tokenless stream when fetchToken rejects with a 404", async () => {
    const fetchToken = vi
      .fn<() => Promise<SseTokenResult>>()
      .mockRejectedValue(new ApiError(404, "Not Found"));
    const onError = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", { fetchToken, onError });

    await client.connect();
    await expectRequestCount(1);

    expect(new URL(streamRequests[0].url).searchParams.has("token")).toBe(
      false,
    );
    expect(client.getState()).toBe("connected");
    expect(onError).not.toHaveBeenCalled();
    client.disconnect();
  });

  it("retries a failed open-mode stream with no token", async () => {
    const fetchToken = vi
      .fn<() => Promise<SseTokenResult>>()
      .mockResolvedValue({ kind: "disabled" });
    const client = new WorkspaceSSEClient("test-ws", {
      fetchToken,
      initialReconnectDelay: 100,
    });

    await client.connect();
    await expectRequestCount(1);
    expect(new URL(streamRequests[0].url).searchParams.has("token")).toBe(
      false,
    );
    streamRequests[0].fail();
    await flush();

    expect(client.getState()).toBe("reconnecting");
    await vi.advanceTimersByTimeAsync(99);
    expect(streamRequests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    await expectRequestCount(2);
    expect(new URL(streamRequests[1].url).searchParams.has("token")).toBe(
      false,
    );
    expect(fetchToken).toHaveBeenCalledTimes(2);
    client.disconnect();
  });

  it.each([401, 403])("treats token %s as fatal", async (status) => {
    const fetchToken = vi
      .fn<() => Promise<SseTokenResult>>()
      .mockRejectedValue(new ApiError(status, "denied"));
    const onError = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      fetchToken,
      onError,
    });

    await client.connect();
    await flush();

    expect(client.getState()).toBe("disconnected");
    expect(onError).toHaveBeenCalledWith(
      `SSE auth failed: API Error: ${status} denied`,
    );
    expect(onError).toHaveBeenCalledTimes(1);
    expect(streamRequests).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchToken).toHaveBeenCalledTimes(1);
  });

  it("retries a non-2xx stream response", async () => {
    queueResponse(502, "text/plain");
    queueResponse();
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect();
    await flush();
    expect(client.getState()).toBe("reconnecting");
    expect(client.getReconnectAttempts()).toBe(1);

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(client.getState()).toBe("connected");
    client.disconnect();
  });

  it("retries a 200 response whose content type is not SSE", async () => {
    queueResponse(200, "text/plain");
    queueResponse();
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect();
    await flush();
    expect(client.getState()).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(client.getState()).toBe("connected");
    client.disconnect();
  });

  it("retries a network failure", async () => {
    queueNetworkError();
    queueResponse();
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect();
    await flush();
    expect(client.getState()).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(1);
    expect(mockStreamFetch).toHaveBeenCalledTimes(2);
    expect(client.getState()).toBe("connected");
    client.disconnect();
  });

  it("retries an errored stream", async () => {
    const client = new WorkspaceSSEClient("test-ws");
    await client.connect();
    await expectRequestCount(1);

    streamRequests[0].fail();
    await flush();
    expect(client.getState()).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    client.disconnect();
  });

  it.each(["token", "503", "close"])(
    "preserves the saved checkpoint after %s failure before the first wire ID",
    async (failure) => {
      if (failure === "token") {
        mockGet.mockRejectedValueOnce(new Error("temporary token failure"));
      } else if (failure === "503") {
        queueResponse(503);
      }
      const client = new WorkspaceSSEClient("test-ws");
      await client.connect("saved-checkpoint");
      await flush();
      if (failure === "close") {
        await expectRequestCount(1);
        streamRequests[0].close();
        await flush();
      }

      await vi.advanceTimersByTimeAsync(1000);
      await expectRequestCount(failure === "token" ? 1 : 2);
      const retry = streamRequests.at(-1)!;
      expect(retry.headers.get("Last-Event-ID")).toBe("saved-checkpoint");
      expect(new URL(retry.url).searchParams.has("since")).toBe(false);
      client.disconnect();
    },
  );

  it("allows an empty first wire ID to clear the saved checkpoint", async () => {
    const client = new WorkspaceSSEClient("test-ws");
    await client.connect("saved-checkpoint");
    await expectRequestCount(1);
    streamRequests[0].push("id:\n\n");
    streamRequests[0].close();
    await flush();

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(streamRequests[1].headers.has("Last-Event-ID")).toBe(false);
    expect(new URL(streamRequests[1].url).searchParams.has("since")).toBe(
      false,
    );
    client.disconnect();
  });

  it.each(["retryNow", "rebind", "reconnect"])(
    "carries ID-only checkpoints and empty resets across %s",
    async (transition) => {
      const client = new WorkspaceSSEClient("test-ws");
      await client.connect("initial-cursor");
      await expectRequestCount(1);

      for (const cursor of ["checkpoint-only", ""]) {
        const active = streamRequests.at(-1)!;
        active.push(`id: ${cursor}\n\n`);
        await flush();
        expect(client.getLastEventId()).toBe(cursor || undefined);
        // A control frame and heartbeat lacking an ID do not reset the cursor.
        active.push('event: connected\ndata: {"clientId":1}\n\n: ping\n\n');
        await flush();
        expect(client.getLastEventId()).toBe(cursor || undefined);
        const expectedRequests = streamRequests.length + 1;
        if (transition === "retryNow") {
          active.close();
          await flush();
          client.retryNow();
        } else if (transition === "rebind") {
          client.updateSourceRepos(["repo-b"]);
        } else {
          client.disconnect();
          await client.connect();
        }
        await expectRequestCount(expectedRequests);
        const next = streamRequests.at(-1)!;
        expect(next.headers.get("Last-Event-ID")).toBe(cursor || null);
        expect(new URL(next.url).searchParams.get("since")).toBe(
          cursor || null,
        );
      }
      client.disconnect();
    },
  );

  it("lets fetch-event-source send Last-Event-ID and refreshes query auth", async () => {
    mockGet
      .mockResolvedValueOnce({ token: "token-one" })
      .mockResolvedValueOnce({ token: "token-two" });
    const client = new WorkspaceSSEClient("test-ws");
    const mutation: MutationPayload = {
      type: "update",
      issue_id: "issue-1",
      timestamp: "2026-09-02T12:00:00Z",
    };

    await client.connect("explicit-cursor");
    await expectRequestCount(1);
    expect(streamRequests[0].url).toContain("since=explicit-cursor");
    expect(streamRequests[0].url).toContain("token=token-one");
    pushMutation(mutation, "library-cursor", streamRequests[0]);
    await flush();
    streamRequests[0].close();
    await flush();

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(streamRequests[1].headers.get("Last-Event-ID")).toBe(
      "library-cursor",
    );
    expect(streamRequests[1].url).not.toContain("since=");
    expect(streamRequests[1].url).toContain("token=token-two");
    client.disconnect();
  });

  it("uses the newest id line as Last-Event-ID on the next attempt", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect("initial-cursor");
    await expectRequestCount(1);
    streamRequests[0].push("id: cursor-one\n\nid: cursor-two\n\n");
    streamRequests[0].close();
    await flush();

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(streamRequests[1].headers.get("Last-Event-ID")).toBe("cursor-two");
    expect(streamRequests[1].url).not.toContain("since=");
    client.disconnect();
  });

  it("clears Last-Event-ID when the server sends an empty id line", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect("initial-cursor");
    await expectRequestCount(1);
    streamRequests[0].push("id: cursor-one\n\nid:\n\n");
    streamRequests[0].close();
    await flush();

    await vi.advanceTimersByTimeAsync(1000);
    await expectRequestCount(2);
    expect(streamRequests[1].headers.has("Last-Event-ID")).toBe(false);
    expect(streamRequests[1].url).not.toContain("since=");
    client.disconnect();
  });

  it("overrides a server retry directive with the wrapper backoff", async () => {
    const client = new WorkspaceSSEClient("test-ws", {
      initialReconnectDelay: 1000,
    });

    await client.connect();
    await expectRequestCount(1);
    streamRequests[0].push("retry: 5000\n\n");
    streamRequests[0].close();
    await flush();

    await vi.advanceTimersByTimeAsync(999);
    expect(streamRequests).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    await expectRequestCount(2);
    client.disconnect();
  });

  it("resets exponential backoff after a successful open", async () => {
    const onReconnect = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onReconnect,
      initialReconnectDelay: 100,
      maxReconnectDelay: 1000,
    });

    await client.connect();
    await expectRequestCount(1);
    streamRequests[0].fail();
    await flush();
    await vi.advanceTimersByTimeAsync(100);
    await expectRequestCount(2);

    expect(client.getReconnectAttempts()).toBe(0);
    streamRequests[1].fail();
    await flush();
    await vi.advanceTimersByTimeAsync(99);
    expect(streamRequests).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    await expectRequestCount(3);
    expect(onReconnect.mock.calls.map(([attempt]) => attempt)).toEqual([
      1, 0, 1, 0,
    ]);
    client.disconnect();
  });

  it("continues retrying when onReconnect throws", async () => {
    queueResponse(502, "text/plain");
    queueResponse();
    const callbackError = new Error("reconnect listener failed");
    const onReconnect = vi.fn(() => {
      throw callbackError;
    });
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const client = new WorkspaceSSEClient("test-ws", {
      onReconnect,
      initialReconnectDelay: 25,
    });

    await client.connect();
    await flush();
    expect(client.getState()).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(25);
    await expectRequestCount(2);
    expect(client.getState()).toBe("connected");
    expect(error).toHaveBeenCalledWith(
      "[SSE] onReconnect callback threw:",
      callbackError,
    );
    client.disconnect();
  });

  it("retryNow inside onReconnect starts only one replacement attempt", async () => {
    queueResponse(502, "text/plain");
    queueResponse();
    const fetchToken = vi
      .fn<() => Promise<SseTokenResult>>()
      .mockResolvedValue({ kind: "token", token: "fresh" });
    const clientRef: { current?: WorkspaceSSEClient } = {};
    const onReconnect = vi.fn((attempt: number) => {
      if (attempt === 1) clientRef.current?.retryNow();
    });
    const client = new WorkspaceSSEClient("test-ws", {
      fetchToken,
      onReconnect,
      initialReconnectDelay: 5000,
    });
    clientRef.current = client;

    await client.connect();
    await flush();

    expect(fetchToken).toHaveBeenCalledTimes(2);
    expect(streamRequests).toHaveLength(2);
    expect(client.getState()).toBe("connected");
    expect(onReconnect.mock.calls.map(([attempt]) => attempt)).toEqual([1, 0]);

    await vi.advanceTimersByTimeAsync(5000);
    expect(fetchToken).toHaveBeenCalledTimes(2);
    expect(streamRequests).toHaveLength(2);
    client.disconnect();
  });

  it("retryNow aborts the pending library retry and reconnects immediately", async () => {
    queueResponse(502, "text/plain");
    queueResponse();
    const onReconnect = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onReconnect,
      initialReconnectDelay: 5000,
    });

    await client.connect();
    await flush();
    expect(client.getState()).toBe("reconnecting");

    client.retryNow();
    await expectRequestCount(2);
    expect(client.getState()).toBe("connected");
    expect(onReconnect).toHaveBeenCalledWith(0);

    await vi.advanceTimersByTimeAsync(5000);
    expect(streamRequests).toHaveLength(2);
    client.disconnect();
  });

  it("disconnect stops retries and aborts the active stream request", async () => {
    const client = new WorkspaceSSEClient("test-ws");
    await client.connect();
    await expectRequestCount(1);

    client.disconnect();

    expect(client.getState()).toBe("disconnected");
    expect(streamRequests[0].aborted).toBe(true);
    expect(streamRequests[0].signal?.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(streamRequests).toHaveLength(1);
  });

  it("disconnect during token exchange prevents a stream request", async () => {
    let resolveToken!: (result: SseTokenResult) => void;
    const fetchToken = vi.fn(
      () =>
        new Promise<SseTokenResult>((resolve) => {
          resolveToken = resolve;
        }),
    );
    const client = new WorkspaceSSEClient("test-ws", { fetchToken });

    await client.connect();
    client.disconnect();
    resolveToken({ kind: "token", token: "too-late" });
    await flush();

    expect(streamRequests).toHaveLength(0);
    expect(client.getState()).toBe("disconnected");
  });

  it("ignores stale callbacks after disconnect followed by connect", async () => {
    let resolveStaleResponse!: (response: Response) => void;
    let staleController!: ReadableStreamDefaultController<Uint8Array>;
    const staleBody = new ReadableStream<Uint8Array>({
      start(controller) {
        staleController = controller;
      },
    });
    mockStreamFetch.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveStaleResponse = resolve;
        }),
    );

    const onMutation = vi.fn();
    const onConnected = vi.fn();
    const onError = vi.fn();
    const onReconnect = vi.fn();
    const onStateChange = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onMutation,
      onConnected,
      onError,
      onReconnect,
      onStateChange,
    });
    const mutation: MutationPayload = {
      type: "update",
      issue_id: "current",
      timestamp: "2026-09-02T12:00:00Z",
    };

    await client.connect();
    await flush();
    client.disconnect();
    await client.connect();
    await expectRequestCount(1);
    pushConnected(streamRequests[0]);
    pushMutation(mutation, "current-cursor", streamRequests[0]);
    await flush();

    const callbackCounts = {
      connected: onConnected.mock.calls.length,
      error: onError.mock.calls.length,
      mutation: onMutation.mock.calls.length,
      reconnect: onReconnect.mock.calls.length,
      state: onStateChange.mock.calls.length,
    };

    resolveStaleResponse(
      new Response(staleBody, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      }),
    );
    await flush();
    staleController.enqueue(
      encoder.encode(
        'event: connected\ndata: {}\n\nid: stale-cursor\nevent: mutation\ndata: {"type":"update"}\n\n',
      ),
    );
    staleController.close();
    await flush();

    expect(onConnected).toHaveBeenCalledTimes(callbackCounts.connected);
    expect(onError).toHaveBeenCalledTimes(callbackCounts.error);
    expect(onMutation).toHaveBeenCalledTimes(callbackCounts.mutation);
    expect(onReconnect).toHaveBeenCalledTimes(callbackCounts.reconnect);
    expect(onStateChange).toHaveBeenCalledTimes(callbackCounts.state);
    expect(client.getLastEventId()).toBe("current-cursor");
    expect(client.getState()).toBe("connected");
    client.disconnect();
  });

  it("updates sourceRepos and reconnects from the retained cursor", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect(undefined, ["repo-a"]);
    await expectRequestCount(1);
    expect(streamRequests[0].url).toContain("source_repos=repo-a");
    pushMutation(
      {
        type: "update",
        issue_id: "issue-1",
        timestamp: "2025-01-23T12:00:00Z",
      },
      "cursor-1",
    );
    await flush();

    client.updateSourceRepos(["repo-b"]);
    await expectRequestCount(2);

    expect(streamRequests[0].aborted).toBe(true);
    expect(streamRequests[1].url).toContain("since=cursor-1");
    expect(streamRequests[1].url).toContain("source_repos=repo-b");
    expect(streamRequests[1].url).not.toContain("repo-a");
    client.disconnect();
  });

  it("clears sourceRepos when updateSourceRepos receives undefined", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect(undefined, ["repo-a"]);
    await expectRequestCount(1);

    client.updateSourceRepos(undefined);
    await expectRequestCount(2);

    expect(streamRequests[0].aborted).toBe(true);
    expect(streamRequests[1].url).not.toContain("source_repos");
    client.disconnect();
  });

  it("retains sourceRepos when a later connect omits the filter", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect(undefined, ["repo-a"]);
    await expectRequestCount(1);
    client.disconnect();
    await client.connect();
    await expectRequestCount(2);

    expect(streamRequests[1].url).toContain("source_repos=repo-a");
    client.disconnect();
  });

  it("clears sourceRepos when a later connect passes an empty array", async () => {
    const client = new WorkspaceSSEClient("test-ws");

    await client.connect(undefined, ["repo-a"]);
    await expectRequestCount(1);
    client.disconnect();
    await client.connect(undefined, []);
    await expectRequestCount(2);

    expect(streamRequests[1].url).not.toContain("source_repos");
    client.disconnect();
  });

  it("uses exponential backoff values and caps at the configured maximum", async () => {
    mockGet.mockRejectedValue(new ApiError(502, "Bad Gateway"));
    const onReconnect = vi.fn();
    const client = new WorkspaceSSEClient("test-ws", {
      onReconnect,
      initialReconnectDelay: 100,
      maxReconnectDelay: 250,
    });

    await client.connect();
    await flush();
    expect(mockGet).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(99);
    expect(mockGet).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await flush();
    expect(mockGet).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(199);
    expect(mockGet).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    await flush();
    expect(mockGet).toHaveBeenCalledTimes(3);

    await vi.advanceTimersByTimeAsync(249);
    expect(mockGet).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(1);
    await flush();
    expect(mockGet).toHaveBeenCalledTimes(4);
    expect(onReconnect.mock.calls.map(([attempt]) => attempt)).toEqual([
      1, 2, 3, 4,
    ]);
    client.disconnect();
  });

  it("emits the five-attempt notice while continuing to retry", async () => {
    mockGet.mockRejectedValue(new ApiError(502, "Bad Gateway"));
    const onError = vi.fn();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const client = new WorkspaceSSEClient("test-ws", {
      onError,
      initialReconnectDelay: 1,
      maxReconnectDelay: 1,
    });

    await client.connect();
    await flush();
    for (let attempt = 2; attempt <= 5; attempt++) {
      await vi.advanceTimersByTimeAsync(1);
      await flush();
    }

    expect(client.getReconnectAttempts()).toBe(5);
    expect(onError).not.toHaveBeenCalled();
    expect(warn).toHaveBeenCalledWith(
      "[SSE] Multiple connection failures, will continue retrying",
    );
    expect(warn).toHaveBeenCalledTimes(1);
    expect(client.getState()).toBe("reconnecting");
    expect(vi.getTimerCount()).toBe(1);

    await vi.advanceTimersByTimeAsync(1);
    await flush();
    expect(client.getReconnectAttempts()).toBe(6);
    expect(client.getState()).toBe("reconnecting");
    expect(onError).not.toHaveBeenCalled();
    expect(warn).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(1);
    client.disconnect();
  });

  it("keeps the stream open when document.hidden changes", async () => {
    const client = new WorkspaceSSEClient("test-ws");
    await client.connect();
    await expectRequestCount(1);

    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
    await flush();

    expect(streamRequests[0].aborted).toBe(false);
    expect(streamRequests).toHaveLength(1);
    client.disconnect();
  });

  it("destroy aborts and makes controls no-ops without changing the last state", async () => {
    const client = new WorkspaceSSEClient("test-ws");
    await client.connect();
    await expectRequestCount(1);
    expect(client.getState()).toBe("connected");

    client.destroy();
    client.disconnect();
    client.retryNow();
    await client.connect();

    expect(streamRequests[0].aborted).toBe(true);
    expect(streamRequests).toHaveLength(1);
    expect(client.getState()).toBe("connected");
  });
});

describe("fetchSseToken", () => {
  beforeEach(() => mockGet.mockReset());

  it("returns token and disabled responses unchanged", async () => {
    mockGet.mockResolvedValueOnce({ token: "opaque" });
    await expect(fetchSseToken("ws")).resolves.toEqual({
      kind: "token",
      token: "opaque",
    });

    mockGet.mockResolvedValueOnce({ disabled: true });
    await expect(fetchSseToken("ws")).resolves.toEqual({ kind: "disabled" });
  });

  it("maps a 404 response to disabled", async () => {
    mockGet.mockRejectedValueOnce(new ApiError(404, "Not Found"));

    await expect(fetchSseToken("ws")).resolves.toEqual({ kind: "disabled" });
  });

  it.each([401, 403, 502])(
    "returns the optional HTTP status for error result %s",
    async (status) => {
      mockGet.mockRejectedValue(new ApiError(status, "request failed"));

      const result = await fetchSseToken("ws");
      mockGet.mockReset();
      expect(result).toEqual({
        kind: "error",
        message: `API Error: ${status} request failed`,
        status,
      });
    },
  );
});

describe("getSSEUrl", () => {
  it("preserves since, source_repos, and query token contract", () => {
    const url = getSSEUrl("test-ws", "cursor-1", ["repo-a", "repo-b"], "tok");

    expect(url).toBe(
      `${window.location.origin}/api/workspaces/test-ws/events?since=cursor-1&source_repos=repo-a%2Crepo-b&token=tok`,
    );
  });

  it("omits optional query parameters", () => {
    expect(getSSEUrl("test-ws")).toBe(
      `${window.location.origin}/api/workspaces/test-ws/events`,
    );
    expect(getSSEUrl("test-ws", 0)).toContain("since=0");
  });
});
