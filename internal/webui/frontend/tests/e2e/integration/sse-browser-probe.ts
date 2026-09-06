import type { Page } from "@playwright/test";
import type { Protocol } from "../../../node_modules/playwright-core/types/protocol";
const MAX_BYTES = 8 * 1024 * 1024;

/** Incremental passive SSE parser: partial records never count as delivered. */
export class SSEProbeParser {
  private decoder = new TextDecoder("utf-8", { fatal: true });
  private line = "";
  private skipLF = false;
  private fields: { id?: string; event?: string; data: string[] } = {
    data: [],
  };
  private bytes = 0;
  constructor(
    private emit: (frame: {
      id?: string;
      event: string;
      data?: string;
    }) => void,
  ) {}
  push(bytes: Uint8Array) {
    this.bytes += bytes.length;
    if (this.bytes > MAX_BYTES)
      throw new Error("SSE probe byte budget exceeded");
    for (const char of this.decoder.decode(bytes, { stream: true })) {
      if (this.skipLF) {
        this.skipLF = false;
        if (char === "\n") continue;
      }
      if (char !== "\r" && char !== "\n") {
        this.line += char;
        continue;
      }
      const line = this.line;
      this.line = "";
      this.skipLF = char === "\r";
      if (!line) {
        const f = this.fields;
        this.fields = { data: [] };
        if (f.id !== undefined || f.event !== undefined || f.data.length)
          this.emit({
            ...(f.id !== undefined ? { id: f.id } : {}),
            event: f.event || "message",
            ...(f.data.length ? { data: f.data.join("\n") } : {}),
          });
        continue;
      }
      if (line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const name = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (name === "data") this.fields.data.push(value);
      if (name === "event") this.fields.event = value;
      if (name === "id" && !value.includes("\0")) this.fields.id = value;
    }
  }
}
export interface ProbeRequest {
  sequence: number;
  requestId: string;
  path: string;
  method: string;
  since: string | null;
  lastEventId: string | null;
  status?: number;
  type?: string;
  streamAttached?: boolean;
}
export interface ProbeFrame {
  sequence: number;
  requestId: string;
  id?: string;
  event: string;
  issueId?: string;
  entityId?: string;
  action?: string;
  workspaceId?: string;
  data?: unknown;
}

/** Observes the current application's stream, without interception or extra requests. */
export async function createSSEBrowserProbe(page: Page, workspace: string) {
  const session = await page.context().newCDPSession(page);
  const requests: ProbeRequest[] = [],
    frames: ProbeFrame[] = [];
  const responses: { sequence: number; requestId: string; path: string }[] = [];
  const completions: { sequence: number; requestId: string; path: string }[] =
    [];
  const failures: { sequence: number; requestId: string; canceled: boolean }[] =
    [];
  const errors: string[] = [],
    owned = new Set<string>();
  const tracked = new Map<string, ProbeRequest>();
  const streams = new Map<
    string,
    {
      parser: SSEProbeParser;
      queue: string[];
      ready: boolean;
      queuedBytes: number;
    }
  >();
  const attachments = new Set<Promise<void>>();
  let sequence = 0,
    disposed = false;
  const base = `/api/workspaces/${encodeURIComponent(workspace)}`;
  function record<T>(array: T[], value: T) {
    if (++sequence > 10_000)
      throw new Error("SSE probe record budget exceeded");
    array.push(value);
  }
  function fail(error: unknown) {
    if (!disposed && errors.length < 20)
      errors.push(
        error instanceof Error ? error.message : "SSE observation failed",
      );
  }
  function safe<T>(callback: (event: T) => void) {
    return (event: T) => {
      if (!disposed && !errors.length) {
        try {
          callback(event);
        } catch (error) {
          fail(error);
        }
      }
    };
  }
  session.on(
    "Network.requestWillBeSent",
    safe((e: Protocol.Network.requestWillBeSentPayload) => {
      const url = new URL(e.request.url);
      if (
        ![`${base}/events`, `${base}/events/token`, `${base}/issues`].includes(
          url.pathname,
        )
      )
        return;
      const resume = Object.entries(e.request.headers).find(
        ([key]) => key.toLowerCase() === "last-event-id",
      )?.[1];
      const request: ProbeRequest = {
        sequence: sequence + 1,
        requestId: e.requestId,
        path: url.pathname,
        method: e.request.method,
        since: url.searchParams.get("since"),
        lastEventId: resume === undefined ? null : String(resume),
      };
      record(requests, request);
      tracked.set(e.requestId, request);
    }),
  );
  session.on(
    "Network.responseReceived",
    safe((e: Protocol.Network.responseReceivedPayload) => {
      const request = tracked.get(e.requestId);
      if (!request) return;
      if (request.path === `${base}/issues` && request.method === "GET") {
        record(responses, {
          sequence: sequence + 1,
          requestId: e.requestId,
          path: request.path,
        });
      }
      request.status = e.response.status;
      request.type = e.type;
      if (request.path !== `${base}/events` || request.status !== 200) return;
      if (!e.response.mimeType.toLowerCase().startsWith("text/event-stream"))
        throw new Error("SSE probe received non-stream content type");
      const state = {
        parser: new SSEProbeParser((frame) => {
          const result: ProbeFrame = {
            sequence: sequence + 1,
            requestId: e.requestId,
            event: frame.event,
            ...(frame.id !== undefined ? { id: frame.id } : {}),
          };
          if (frame.data !== undefined && frame.event === "mutation") {
            let data: unknown;
            try {
              data = JSON.parse(frame.data);
            } catch {
              throw new Error("SSE probe received malformed mutation JSON");
            }
            if (data && typeof data === "object") {
              const row = data as Record<string, unknown>;
              for (const [key, name] of [
                ["issue_id", "issueId"],
                ["entity_id", "entityId"],
                ["action", "action"],
                ["workspace_id", "workspaceId"],
              ] as const)
                if (typeof row[key] === "string") result[name] = row[key];
              if (result.issueId && owned.has(result.issueId))
                result.data = data;
            }
          }
          record(frames, result);
        }),
        queue: [] as string[],
        ready: false,
        queuedBytes: 0,
      };
      streams.set(e.requestId, state);
      const attachment = (async () => {
        try {
          const result: Protocol.Network.streamResourceContentReturnValue =
            await session.send("Network.streamResourceContent", {
              requestId: e.requestId,
            });
          if (disposed) return;
          state.parser.push(Buffer.from(result.bufferedData, "base64"));
          for (const chunk of state.queue)
            state.parser.push(Buffer.from(chunk, "base64"));
          state.queue = [];
          state.ready = true;
          request.streamAttached = true;
        } catch {
          fail(new Error("SSE probe could not attach/decode CDP stream"));
        }
      })();
      attachments.add(attachment);
      void attachment.finally(() => attachments.delete(attachment));
    }),
  );
  session.on(
    "Network.dataReceived",
    safe((e: Protocol.Network.dataReceivedPayload) => {
      const state = streams.get(e.requestId);
      if (!state || e.data === undefined) return;
      if (state.ready) state.parser.push(Buffer.from(e.data, "base64"));
      else {
        state.queuedBytes += e.data.length;
        if (state.queuedBytes > MAX_BYTES * 2)
          throw new Error("SSE probe pending byte budget exceeded");
        state.queue.push(e.data);
      }
    }),
  );
  session.on(
    "Network.loadingFinished",
    safe((e: Protocol.Network.loadingFinishedPayload) => {
      const request = tracked.get(e.requestId);
      if (request?.path === `${base}/issues` && request.method === "GET")
        record(completions, {
          sequence: sequence + 1,
          requestId: e.requestId,
          path: request.path,
        });
    }),
  );
  session.on(
    "Network.loadingFailed",
    safe((e: Protocol.Network.loadingFailedPayload) => {
      if (tracked.has(e.requestId))
        record(failures, {
          sequence: sequence + 1,
          requestId: e.requestId,
          canceled: e.canceled ?? false,
        });
    }),
  );
  try {
    await session.send("Network.enable");
  } catch {
    await session.detach();
    throw new Error("SSE probe requires Chromium CDP Network observation");
  }
  return {
    requests,
    frames,
    responses,
    completions,
    failures,
    errors,
    ownIssue(id: string) {
      owned.add(id);
    },
    assertHealthy() {
      if (errors.length) throw new Error(errors.join("; "));
    },
    snapshot() {
      return JSON.parse(
        JSON.stringify({
          requests,
          frames,
          responses,
          completions,
          failures,
          errors,
        }),
      );
    },
    async dispose() {
      disposed = true;
      await session.detach();
      await Promise.allSettled(attachments);
    },
  };
}

export type SSEBrowserProbe = Awaited<ReturnType<typeof createSSEBrowserProbe>>;
