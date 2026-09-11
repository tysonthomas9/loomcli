import { describe, expect, it } from "vitest";
import { SSEProbeParser } from "../../../../tests/e2e/integration/sse-browser-probe";

describe("passive CDP SSE framing", () => {
  it("preserves fragmented UTF8 and split CRLF, emitting buffered frames before later bytes", () => {
    const frames: unknown[] = [];
    const parser = new SSEProbeParser((frame) => frames.push(frame));
    const bytes = new TextEncoder().encode(
      'id: c2.first\r\nevent: mutation\r\ndata: {"title":"😀"}\r\n\r\nid:\ndata: one\ndata: two\n\n',
    );
    for (const byte of bytes) parser.push(Uint8Array.of(byte));
    expect(frames).toEqual([
      { id: "c2.first", event: "mutation", data: '{"title":"😀"}' },
      { id: "", event: "message", data: "one\ntwo" },
    ]);
  });
  it("does not count incomplete records or comments as delivered checkpoints", () => {
    const frames: unknown[] = [];
    const parser = new SSEProbeParser((frame) => frames.push(frame));
    parser.push(
      new TextEncoder().encode(
        ": heartbeat\n\nid: accepted\n\nid: partial\nevent: mutation\ndata: {}\n",
      ),
    );
    expect(frames).toEqual([{ id: "accepted", event: "message" }]);
    parser.push(new TextEncoder().encode("\n"));
    expect(frames).toHaveLength(2);
  });
  it("rejects invalid UTF8 and bounds unframed data", () => {
    const parser = new SSEProbeParser(() => {});
    expect(() => parser.push(Uint8Array.of(0xff))).toThrow();
    const bounded = new SSEProbeParser(() => {});
    expect(() => bounded.push(new Uint8Array(8 * 1024 * 1024 + 1))).toThrow(
      /budget/,
    );
  });
});
