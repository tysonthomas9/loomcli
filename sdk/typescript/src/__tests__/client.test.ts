import { afterEach, describe, expect, it, vi } from "vitest";
import { FencedError, TaskRunClient, TaskRunError } from "../client.js";

const bootstrap = {
  serverUrl: "https://loom.example.com",
  workspace: "DEMO",
  taskId: "DEMO-1",
  sessionId: "sess-1",
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
  it("getTask GETs the issue, sends auth headers, unwraps data", async () => {
    const fetchFn = stubFetch((req) => {
      expect(req.method).toBe("GET");
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/issues/DEMO-1");
      expect(req.headers.get("authorization")).toBe("Bearer tok");
      // fencing rides the signed token claim, not a header.
      expect(req.headers.get("x-loom-fencing-token")).toBeNull();
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

  it("postArtifact POSTs the artifact to the session endpoint", async () => {
    stubFetch(async (req) => {
      expect(req.method).toBe("POST");
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/sessions/sess-1/artifacts");
      await expect(req.json()).resolves.toEqual({
        type: "patch",
        uri: "/tmp/x.patch",
        summary: "did X",
        files_changed: 3,
      });
      return json({ success: true, data: { artifact_id: "a1", type: "patch", uri: "/tmp/x.patch" } }, 201);
    });
    await TaskRunClient.fromBootstrap(bootstrap).postArtifact({
      type: "patch",
      uri: "/tmp/x.patch",
      summary: "did X",
      filesChanged: 3,
    });
  });

  it("recordUsage POSTs snake_case usage to the session endpoint", async () => {
    stubFetch(async (req) => {
      expect(new URL(req.url).pathname).toBe("/api/workspaces/DEMO/sessions/sess-1/usage");
      await expect(req.json()).resolves.toEqual({
        input_tokens: 100,
        output_tokens: 20,
        cache_read_tokens: 5,
        cache_write_tokens: 2,
      });
      return json({ success: true });
    });
    await TaskRunClient.fromBootstrap(bootstrap).recordUsage({
      inputTokens: 100,
      outputTokens: 20,
      cacheReadTokens: 5,
      cacheWriteTokens: 2,
    });
  });

  it("appendLog POSTs the log line; heartbeat POSTs to the heartbeat endpoint", async () => {
    const seen: string[] = [];
    stubFetch((req) => {
      seen.push(new URL(req.url).pathname);
      return json({ success: true }, 202);
    });
    const run = TaskRunClient.fromBootstrap(bootstrap);
    await run.appendLog({ stream: "stdout", text: "hi" });
    await run.heartbeat();
    expect(seen).toEqual([
      "/api/workspaces/DEMO/sessions/sess-1/logs",
      "/api/workspaces/DEMO/sessions/sess-1/heartbeat",
    ]);
  });

  it("heartbeat rotates the bearer onto the refreshed token", async () => {
    const auths: (string | null)[] = [];
    stubFetch((req) => {
      auths.push(req.headers.get("authorization"));
      if (new URL(req.url).pathname.endsWith("/heartbeat")) {
        return json({ success: true, data: { session_id: "sess-1", token: "tok2" } });
      }
      return json({ success: true }, 202);
    });
    const run = TaskRunClient.fromBootstrap(bootstrap);
    await run.appendLog({ stream: "stdout", text: "a" }); // uses initial token
    await run.heartbeat(); // server returns tok2 → client rotates
    await run.appendLog({ stream: "stdout", text: "b" }); // uses tok2
    expect(auths).toEqual(["Bearer tok", "Bearer tok", "Bearer tok2"]);
  });

  it("session writes require a sessionId in the bootstrap", async () => {
    const { sessionId, ...noSession } = bootstrap;
    void sessionId;
    await expect(TaskRunClient.fromBootstrap(noSession).heartbeat()).rejects.toBeInstanceOf(
      TaskRunError,
    );
  });

  it("maps a 409 on a write to FencedError", async () => {
    stubFetch(() => json({ error: "stale fencing token" }, 409));
    await expect(
      TaskRunClient.fromBootstrap(bootstrap).postArtifact({ type: "patch", uri: "/tmp/x" }),
    ).rejects.toBeInstanceOf(FencedError);
  });
});
