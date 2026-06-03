import { afterEach, describe, expect, it, vi } from "vitest";
import { FencedError, NotImplementedError, TaskRunClient } from "../client.js";

const bootstrap = {
  serverUrl: "https://loom.example.com",
  workspace: "DEMO",
  taskId: "DEMO-1",
  token: "tok",
  fencingToken: "7",
  actor: "loom-dev",
};

function stubFetch(handler: (req: Request) => Response) {
  const fn = vi.fn(async (input: Request | string, init?: RequestInit) => {
    const req = input instanceof Request ? input : new Request(input, init);
    return handler(req);
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

afterEach(() => vi.unstubAllGlobals());

describe("TaskRunClient", () => {
  it("getTask GETs the issue, sends auth + fencing headers, unwraps data", async () => {
    const fetchFn = stubFetch((req) => {
      expect(req.method).toBe("GET");
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/issues/DEMO-1");
      expect(req.headers.get("authorization")).toBe("Bearer tok");
      expect(req.headers.get("x-loom-fencing-token")).toBe("7");
      expect(req.headers.get("x-actor")).toBe("loom-dev");
      return json({
        success: true,
        data: { id: "DEMO-1", title: "T", design: "do X", status: "open", issue_type: "task", labels: [] },
      });
    });
    const task = await TaskRunClient.fromBootstrap(bootstrap).getTask();
    expect(task.design).toBe("do X");
    expect(fetchFn).toHaveBeenCalledOnce();
  });

  it("comment POSTs the text", async () => {
    stubFetch(async (req) => {
      expect(req.method).toBe("POST");
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/issues/DEMO-1/comments");
      await expect(req.json()).resolves.toEqual({ text: "hi" });
      return json({ success: true }, 201);
    });
    await TaskRunClient.fromBootstrap(bootstrap).comment("hi");
  });

  it("complete POSTs to /close", async () => {
    stubFetch((req) => {
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/issues/DEMO-1/close");
      return json({ success: true });
    });
    await TaskRunClient.fromBootstrap(bootstrap).complete({ reason: "done" });
  });

  it("maps HTTP 409 to FencedError", async () => {
    stubFetch(() => json({ error: "stale fencing token" }, 409));
    await expect(TaskRunClient.fromBootstrap(bootstrap).comment("x")).rejects.toBeInstanceOf(
      FencedError,
    );
  });

  it("Phase C/D methods throw NotImplementedError", async () => {
    const run = TaskRunClient.fromBootstrap(bootstrap);
    await expect(run.heartbeat()).rejects.toBeInstanceOf(NotImplementedError);
    await expect(run.recordUsage({ inputTokens: 1, outputTokens: 2 })).rejects.toBeInstanceOf(
      NotImplementedError,
    );
    await expect(run.postArtifact({ type: "patch", uri: "/tmp/x.patch" })).rejects.toBeInstanceOf(
      NotImplementedError,
    );
  });
});
