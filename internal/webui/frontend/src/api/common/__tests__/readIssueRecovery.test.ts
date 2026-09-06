import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readIssueRecovery } from "../readIssueRecovery";
import { getAuthToken } from "../client";
import type { RecoveryHandle } from "../recoveryHandle";
vi.mock("../client", () => ({
  getAuthToken: vi.fn(() => "token"),
  wsUrl: (ws: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(ws)}${path}`,
}));
const now = Date.parse("2026-09-05T12:00:00Z");
const offer: RecoveryHandle = {
  handle: "A".repeat(43),
  source_identity: "s1.Zml4dHVyZQ",
  workspace: "WS",
  source_repos: [],
  expires_at: "2026-09-05T12:01:00Z",
  manifest: "fleet.issue-workspace.v5",
};
const document = JSON.stringify({
  manifest: offer.manifest,
  workspace: "WS",
  through: "c2.Zml4dHVyZQ",
  issues: [],
  total: 0,
  ready: [],
  blocked: [],
  deferred: [],
  dependencies: [],
  comments: [],
  history: null,
});
const headers = {
  "X-Loom-Recovery-Source": offer.source_identity,
  "Content-Type": "application/json",
  "X-Loom-Recovery-Handle": offer.handle,
};
const encoder = new TextEncoder();
function response(chunks: Uint8Array[], extra = {}) {
  return new Response(
    new ReadableStream({
      start(c) {
        for (const chunk of chunks) c.enqueue(chunk);
        c.close();
      },
    }),
    { headers, ...extra },
  );
}
function read(signal = new AbortController().signal) {
  return readIssueRecovery(offer, signal);
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  vi.mocked(getAuthToken).mockReturnValue("token");
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
describe("readIssueRecovery", () => {
  it("encodes selected scope without query injection and validates the exact echo", async () => {
    const id = "WS-1 &another=two";
    const selected = JSON.stringify({
      ...JSON.parse(document),
      history: { issue_id: id, present: false, events: [], has_older: false },
    });
    const fetcher = vi
      .fn()
      .mockImplementation(async () => response([encoder.encode(selected)]));
    vi.stubGlobal("fetch", fetcher);
    const result = await readIssueRecovery(
      offer,
      new AbortController().signal,
      id,
    );
    expect(result.history?.issue_id).toBe(id);
    expect(fetcher.mock.calls[0][0]).toBe(
      `/api/workspaces/WS/events/recovery/issues?${new URLSearchParams({ issue_id: id })}`,
    );
    await expect(
      readIssueRecovery(offer, new AbortController().signal, "WS-other"),
    ).rejects.toThrow();
  });
  it.each(["", "\u0085", "😀".repeat(257), "\uD800"])(
    "rejects invalid selected scope before network %s",
    async (id) => {
      const fetcher = vi.fn();
      vi.stubGlobal("fetch", fetcher);
      await expect(
        readIssueRecovery(offer, new AbortController().signal, id),
      ).rejects.toThrow();
      expect(fetcher).not.toHaveBeenCalled();
    },
  );
  it("rejects a selected-history document on the unselected HTTP read", async () => {
    const selected = JSON.stringify({
      ...JSON.parse(document),
      history: {
        issue_id: "WS-absent",
        present: false,
        events: [],
        has_older: false,
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(response([encoder.encode(selected)])),
    );
    await expect(read()).rejects.toThrow();
  });
  it("preserves document and sends authenticated bodyless POST", async () => {
    const raw = ` ${document}\n`;
    const bytes = encoder.encode(raw);
    const fetcher = vi
      .fn()
      .mockResolvedValue(response([bytes.slice(0, 11), bytes.slice(11)]));
    vi.stubGlobal("fetch", fetcher);
    expect((await read()).document).toBe(raw);
    expect(fetcher).toHaveBeenCalledWith(
      "/api/workspaces/WS/events/recovery/issues",
      expect.objectContaining({
        method: "POST",
        redirect: "error",
        cache: "no-store",
        headers: {
          Authorization: "Bearer token",
          "X-Loom-Recovery-Handle": offer.handle,
        },
      }),
    );
    expect(fetcher.mock.calls[0][1]).not.toHaveProperty("body");
  });
  it("decodes a split multibyte character without replacing it", async () => {
    const row = {
      id: "WS-1",
      workspace: "WS",
      title: "é",
      status: "open",
      type: "task",
      priority: 1,
      created_by: "a",
      created_at: "2026-09-05T12:00:00Z",
      updated_at: "2026-09-05T12:00:00Z",
      labels: [],
      metadata: {},
    };
    const raw = JSON.stringify({
      ...JSON.parse(document),
      issues: [row],
      total: 1,
    });
    const bytes = encoder.encode(raw);
    const split = bytes.indexOf(0xc3) + 1;
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          response([bytes.slice(0, split), bytes.slice(split)]),
        ),
    );
    expect((await read()).document).toBe(raw);
  });
  it.each([
    ["invalid UTF8", [new Uint8Array([0xff])]],
    ["oversize", [new Uint8Array(16 * 1024 * 1024 + 1)]],
    ["malformed JSON", [encoder.encode("{")]],
    ["incomplete UTF8", [new Uint8Array([0xc3])]],
    ["BOM", [encoder.encode(`\uFEFF${document}`)]],
  ])("rejects %s", async (_, chunks) => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(response(chunks as Uint8Array[])),
    );
    await expect(read()).rejects.toBeDefined();
  });
  it.each([
    { status: 201 },
    { status: 410 },
    { headers: { ...headers, "X-Loom-Recovery-Handle": "wrong" } },
    { headers: { "Content-Type": "application/json" } },
    { headers: { ...headers, "Content-Type": "text/plain" } },
  ])("rejects envelope before reading %#", async (extra) => {
    const cancel = vi.fn();
    const body = new ReadableStream({ cancel });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(body, { headers, ...extra })),
    );
    await expect(read()).rejects.toThrow("Invalid recovery response");
    expect(cancel).toHaveBeenCalledOnce();
  });
  it("rejects invalid offer and absent auth before fetch", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(
      readIssueRecovery(
        { ...offer, handle: "bad" },
        new AbortController().signal,
      ),
    ).rejects.toThrow();
    vi.mocked(getAuthToken).mockReturnValue(null);
    await expect(read()).rejects.toThrow("authentication");
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("rejects expired and pre-aborted requests without fetch", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    const c = new AbortController();
    c.abort();
    await expect(read(c.signal)).rejects.toBeDefined();
    vi.setSystemTime(Date.parse(offer.expires_at));
    await expect(read()).rejects.toThrow("offer");
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("cancels ignored fetch and disposes late response", async () => {
    let resolve!: (r: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((r) => {
            resolve = r;
          }),
      ),
    );
    const c = new AbortController();
    const result = read(c.signal);
    const rejected = expect(result).rejects.toBeDefined();
    c.abort();
    await rejected;
    const cancel = vi.fn();
    resolve(new Response(new ReadableStream({ cancel }), { headers }));
    await Promise.resolve();
    await Promise.resolve();
    expect(cancel).toHaveBeenCalledOnce();
  });
  it("cancels pending body read and releases reader even if cancel hangs", async () => {
    const cancel = vi.fn(() => new Promise<void>(() => {}));
    const body = new ReadableStream<Uint8Array>({ cancel });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(body, { headers })),
    );
    const c = new AbortController();
    const result = read(c.signal);
    const rejected = expect(result).rejects.toBeDefined();
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    c.abort();
    await rejected;
    expect(cancel).toHaveBeenCalledOnce();
    expect(body.locked).toBe(false);
  });
  it.each([15000, 500])("times out or expires after %i ms", async (delay) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const scoped = {
      ...offer,
      expires_at: new Date(now + (delay === 500 ? 500 : 60000)).toISOString(),
    };
    const rejected = expect(
      readIssueRecovery(scoped, new AbortController().signal),
    ).rejects.toBeDefined();
    await vi.advanceTimersByTimeAsync(delay);
    await rejected;
  });
  it("rejects a late successful body after expiry even before its timer runs", async () => {
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        vi.setSystemTime(Date.parse(offer.expires_at));
        controller.enqueue(encoder.encode(document));
        controller.close();
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(body, { headers })),
    );
    await expect(read()).rejects.toThrow("expired");
    expect(body.locked).toBe(false);
  });
  it("propagates rejected redirect or network fetch without fallback", async () => {
    const fetcher = vi
      .fn()
      .mockRejectedValue(new TypeError("redirect rejected"));
    vi.stubGlobal("fetch", fetcher);
    await expect(read()).rejects.toThrow("redirect rejected");
    expect(fetcher).toHaveBeenCalledOnce();
  });
  it("enforces the absolute 15s deadline before the timeout callback can run", async () => {
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        vi.setSystemTime(now + 15_001);
        controller.enqueue(encoder.encode(document));
        controller.close();
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(body, { headers })),
    );
    await expect(read()).rejects.toThrow("expired");
  });
});

it.each([undefined, "s1.b3RoZXI"])(
  "rejects missing or foreign source identity header %s",
  async (identity) => {
    const changed: Record<string, string> = { ...headers };
    if (identity === undefined) delete changed["X-Loom-Recovery-Source"];
    else changed["X-Loom-Recovery-Source"] = identity;
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          response([encoder.encode(document)], { headers: changed }),
        ),
    );
    await expect(read()).rejects.toThrow("Invalid recovery response");
  },
);
