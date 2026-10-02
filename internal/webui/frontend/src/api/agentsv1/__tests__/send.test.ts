/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { sendMessage } from "..";

let bodies: unknown[];

beforeEach(() => {
  bodies = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body));
      bodies.push(body);
      const res = body.delivery
        ? { message_id: "", state: "", replaced: false, interrupted: true }
        : { message_id: "m1", state: "waiting", replaced: false };
      return new Response(JSON.stringify(res), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sendMessage delivery", () => {
  it("sends delivery only when set, and returns interrupted", async () => {
    const queued = await sendMessage("ws1", "a1", "hello", "r1");
    const stop = await sendMessage("ws1", "a1", "", "r2", "interrupt");
    await sendMessage("ws1", "a1", "instead", "r3", "interrupt");
    expect(bodies).toEqual([
      { text: "hello" },
      { text: "", delivery: "interrupt" },
      { text: "instead", delivery: "interrupt" },
    ]);
    expect(queued.interrupted).toBeUndefined();
    expect(stop.interrupted).toBe(true);
  });
});
