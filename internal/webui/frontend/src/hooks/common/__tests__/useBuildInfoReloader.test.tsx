/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { BuildInfo, ConnectionState } from "@/api/common";
import { useBuildInfoReloader } from "../useBuildInfoReloader";

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function createStorage(initial?: Record<string, string>) {
  const values = new Map(Object.entries(initial ?? {}));
  return {
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      values.set(key, value);
    }),
  };
}

function renderReloader(options: {
  connectionState?: ConnectionState;
  fetcher: (signal?: AbortSignal) => Promise<BuildInfo>;
  reload?: () => void;
  storage?: ReturnType<typeof createStorage>;
}) {
  const reload = options.reload ?? vi.fn();
  const storage = options.storage ?? createStorage();
  let connectionState = options.connectionState ?? "disconnected";
  const hook = renderHook(() =>
    useBuildInfoReloader({
      connectionState,
      fetcher: options.fetcher,
      reload,
      storage,
      intervalMs: 60_000,
    }),
  );
  return {
    ...hook,
    reload,
    storage,
    setConnectionState(next: ConnectionState) {
      connectionState = next;
      hook.rerender();
    },
  };
}

describe("useBuildInfoReloader", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("stores the initial frontend hash without reloading", async () => {
    const fetcher = vi.fn().mockResolvedValue({ frontend_hash: "hash-a" });
    const { reload } = renderReloader({ fetcher });

    await flushPromises();

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(reload).not.toHaveBeenCalled();
  });

  it("does not reload when reconnect sees the same hash", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ frontend_hash: "hash-a" })
      .mockResolvedValueOnce({ frontend_hash: "hash-a" });
    const { reload, setConnectionState } = renderReloader({ fetcher });
    await flushPromises();

    act(() => {
      setConnectionState("connected");
    });
    await flushPromises();

    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(reload).not.toHaveBeenCalled();
  });

  it("reloads once when reconnect sees a different hash", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ frontend_hash: "hash-a" })
      .mockResolvedValueOnce({ frontend_hash: "hash-b" })
      .mockResolvedValueOnce({ frontend_hash: "hash-b" });
    const { reload, storage, setConnectionState } = renderReloader({ fetcher });
    await flushPromises();

    act(() => {
      setConnectionState("connected");
    });
    await flushPromises();

    expect(reload).toHaveBeenCalledTimes(1);
    expect(storage.setItem).toHaveBeenCalledWith(
      "loom:build-info-reload:v1",
      "hash-b",
    );

    act(() => {
      setConnectionState("reconnecting");
    });
    act(() => {
      setConnectionState("connected");
    });
    await flushPromises();

    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("uses the reload guard to avoid loops for the same target hash", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ frontend_hash: "hash-a" })
      .mockResolvedValueOnce({ frontend_hash: "hash-b" });
    const storage = createStorage({ "loom:build-info-reload:v1": "hash-b" });
    const { reload, setConnectionState } = renderReloader({
      fetcher,
      storage,
    });
    await flushPromises();

    act(() => {
      setConnectionState("connected");
    });
    await flushPromises();

    expect(reload).not.toHaveBeenCalled();
  });

  it("checks periodically", async () => {
    const fetcher = vi.fn().mockResolvedValue({ frontend_hash: "hash-a" });
    renderReloader({ fetcher });
    await flushPromises();

    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    await flushPromises();

    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
