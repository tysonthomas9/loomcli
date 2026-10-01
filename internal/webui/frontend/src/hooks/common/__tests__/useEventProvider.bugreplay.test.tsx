/**
 * @vitest-environment jsdom
 */
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import React from "react";

import { WorkspaceSSEClient } from "@/api/common/sse";

import { EventProvider } from "../useEventProvider";

vi.mock("@/api/common/client", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/api/common/client")>();
  return {
    ...mod,
    get: vi.fn().mockRejectedValue(new mod.ApiError(404, "Not Found")),
  };
});

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "ws-replay" }),
  };
});

class ReplayEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: ReplayEventSource[] = [];

  readyState = ReplayEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Array<(event: MessageEvent) => void>>();

  constructor(readonly url: string) {
    ReplayEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  removeEventListener(): void {}

  open(): void {
    this.readyState = ReplayEventSource.OPEN;
    this.onopen?.();
    for (const listener of this.listeners.get("connected") ?? []) {
      listener({ data: "{}" } as MessageEvent);
    }
  }

  close(): void {
    this.readyState = ReplayEventSource.CLOSED;
  }
}

function activeStream(): ReplayEventSource | undefined {
  return ReplayEventSource.instances.findLast(
    (source) => source.readyState !== ReplayEventSource.CLOSED,
  );
}

let originalEventSource: typeof EventSource;

beforeEach(() => {
  originalEventSource = globalThis.EventSource;
  globalThis.EventSource = ReplayEventSource as unknown as typeof EventSource;
  ReplayEventSource.instances = [];
});

afterEach(() => {
  globalThis.EventSource = originalEventSource;
});

it("#614 applies a repo filter that arrives after the first render", async () => {
  const { rerender } = render(
    <EventProvider sourceRepos={undefined}>
      <div />
    </EventProvider>,
  );
  await act(async () => {});
  expect(ReplayEventSource.instances).toHaveLength(1);
  expect(activeStream()?.url).not.toContain("source_repos=");

  rerender(
    <EventProvider sourceRepos={["repo-a"]}>
      <div />
    </EventProvider>,
  );
  await act(async () => {});

  expect(
    activeStream()?.url,
    "first undefined -> repo filter change was swallowed; the tab stays unscoped",
  ).toContain("source_repos=repo-a");
});

it("#614 applies a filter passed to connect() while already connected", async () => {
  const client = new WorkspaceSSEClient("ws-replay", {
    fetchToken: async () => ({ kind: "token", token: "ok" }),
  });
  await client.connect();
  activeStream()!.open();
  expect(client.getState()).toBe("connected");

  await client.connect(undefined, ["repo-a"]);

  expect(
    activeStream()?.url,
    "connected client saved the new filter without applying it",
  ).toContain("source_repos=repo-a");
  client.destroy();
});
