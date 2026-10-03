/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { listHarnessModels, updateAgent } from "..";

let calls: { url: string; method: string; body?: unknown }[];

beforeEach(() => {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit = {}) => {
      calls.push({
        url,
        method: init.method ?? "GET",
        body: init.body ? JSON.parse(String(init.body)) : undefined,
      });
      const res = url.includes("/harnesses/")
        ? {
            harness: "opencode",
            providers: [{ id: "openai", name: "OpenAI", models: [] }],
          }
        : { agent_id: "a1" };
      return new Response(JSON.stringify(res), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("model catalog and effort", () => {
  it("lists a harness's models from its catalog route", async () => {
    const cat = await listHarnessModels("ws", "opencode");
    expect(calls[0].url).toContain(
      "/api/workspaces/ws/v1/harnesses/opencode/models",
    );
    expect(cat.providers[0].name).toBe("OpenAI");
  });

  it("PATCHes effort and options with the request id", async () => {
    await updateAgent(
      "ws",
      "a1",
      { effort: "high", options: [{ id: "fast", value: true }] },
      "r1",
    );
    expect(calls[0].method).toBe("PATCH");
    expect(calls[0].body).toEqual({
      effort: "high",
      options: [{ id: "fast", value: true }],
    });
  });
});
