/**
 * @vitest-environment jsdom
 */

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  getWorkflowRunEvents,
  workflowRunEventStreamUrl,
  type WorkflowRunEvent,
  type WorkflowRunStreamCompletion,
  type WorkflowRunStreamError,
} from "@/api/workflows";

import { useWorkflowRunEvents } from "../useWorkflowRunEvents";

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "WS" }),
}));

vi.mock("@/api/workflows", () => ({
  getWorkflowRunEvents: vi.fn(),
  workflowRunEventStreamUrl: vi.fn(
    (
      workspaceId: string,
      runId: string,
      options?: { untilTerminal?: boolean },
    ) =>
      options?.untilTerminal
        ? `/stream/${workspaceId}/${runId}?until=terminal`
        : `/stream/${workspaceId}/${runId}`,
  ),
}));

const mockGetWorkflowRunEvents = vi.mocked(getWorkflowRunEvents);
const mockWorkflowRunEventStreamUrl = vi.mocked(workflowRunEventStreamUrl);

type EventListener = (event: MessageEvent<string>) => void;

class MockEventSource {
  static instances: MockEventSource[] = [];

  readonly url: string | URL;
  readonly listeners = new Map<string, EventListener[]>();
  closed = false;
  onerror: ((event: Event) => void) | null = null;

  constructor(url: string | URL) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener): void {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, data: unknown): void {
    const event = { data: JSON.stringify(data) } as MessageEvent<string>;
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }

  emitRaw(type: string, data: string): void {
    const event = { data } as MessageEvent<string>;
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }

  emitError(): void {
    this.onerror?.(new Event("error"));
  }

  static reset(): void {
    MockEventSource.instances = [];
  }

  static get last(): MockEventSource {
    const instance = MockEventSource.instances.at(-1);
    if (!instance) throw new Error("no EventSource instance");
    return instance;
  }
}

const initialEvent: WorkflowRunEvent = {
  workspace_key: "WS",
  event_id: "evt-1",
  workflow_run_id: "wrun-1",
  event_index: 1,
  type: "workflow_started",
  created_at: "2026-01-01T00:00:00Z",
};

describe("useWorkflowRunEvents", () => {
  const originalEventSource = globalThis.EventSource;

  beforeEach(() => {
    vi.clearAllMocks();
    MockEventSource.reset();
    globalThis.EventSource = MockEventSource as unknown as typeof EventSource;
    mockGetWorkflowRunEvents.mockResolvedValue([initialEvent]);
  });

  afterEach(() => {
    globalThis.EventSource = originalEventSource;
  });

  it("requests terminal streams and exposes completion envelopes", async () => {
    const { result } = renderHook(() => useWorkflowRunEvents("wrun-1", true));

    await waitFor(() => expect(result.current.events).toEqual([initialEvent]));
    expect(mockWorkflowRunEventStreamUrl).toHaveBeenCalledWith("WS", "wrun-1", {
      untilTerminal: true,
    });
    expect(String(MockEventSource.last.url)).toContain("until=terminal");

    const nextEvent: WorkflowRunEvent = {
      ...initialEvent,
      event_id: "evt-2",
      event_index: 2,
      type: "workflow_completed",
    };
    act(() => {
      MockEventSource.last.emit("workflow_event", nextEvent);
    });
    await waitFor(() =>
      expect(result.current.events.map((event) => event.event_id)).toEqual([
        "evt-1",
        "evt-2",
      ]),
    );

    const completion: WorkflowRunStreamCompletion = {
      run_ids: ["wrun-1"],
      runs: [
        {
          run_id: "wrun-1",
          workflow_name: "epic-runner",
          status: "completed",
          finished_at: "2026-01-01T00:01:00Z",
        },
      ],
    };
    act(() => {
      MockEventSource.last.emit("workflow_run_stream_complete", completion);
    });

    await waitFor(() =>
      expect(result.current.streamCompletion).toEqual(completion),
    );
    expect(result.current.isStreamComplete).toBe(true);
    expect(MockEventSource.last.closed).toBe(true);

    act(() => {
      MockEventSource.last.emitError();
    });
    expect(mockGetWorkflowRunEvents).toHaveBeenCalledTimes(1);
  });

  it("does not open EventSource streams for non-live runs", async () => {
    const { result } = renderHook(() => useWorkflowRunEvents("wrun-1", false));

    await waitFor(() => expect(result.current.events).toEqual([initialEvent]));
    expect(MockEventSource.instances).toHaveLength(0);
    expect(result.current.streamCompletion).toBeNull();
    expect(result.current.isStreamComplete).toBe(false);
  });

  it("reports structured stream error envelopes and closes the stream", async () => {
    const { result } = renderHook(() => useWorkflowRunEvents("wrun-1", true));

    await waitFor(() => expect(result.current.events).toEqual([initialEvent]));

    const streamError: WorkflowRunStreamError = {
      run_ids: ["wrun-1"],
      message: "run disappeared",
      terminal: true,
    };
    act(() => {
      MockEventSource.last.emit("workflow_run_stream_error", streamError);
    });

    await waitFor(() =>
      expect(result.current.error?.message).toBe("run disappeared"),
    );
    expect(result.current.isStreamComplete).toBe(false);
    expect(MockEventSource.last.closed).toBe(true);

    act(() => {
      MockEventSource.last.emitError();
    });
    expect(mockGetWorkflowRunEvents).toHaveBeenCalledTimes(1);
  });

  it("reports malformed completion envelopes as errors", async () => {
    const { result } = renderHook(() => useWorkflowRunEvents("wrun-1", true));

    await waitFor(() => expect(result.current.events).toEqual([initialEvent]));

    act(() => {
      MockEventSource.last.emitRaw("workflow_run_stream_complete", "{");
    });

    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));
    expect(result.current.streamCompletion).toBeNull();
    expect(MockEventSource.last.closed).toBe(false);
  });
});
