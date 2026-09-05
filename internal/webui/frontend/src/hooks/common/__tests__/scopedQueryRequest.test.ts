import { describe, expect, it, vi } from "vitest";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";

function deferred<T>() {
  let resolve!: (data: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe("ScopedQueryRequest", () => {
  it("recovery supersedes an already running request and ordinary refresh joins it", async () => {
    const old = deferred<string>();
    const fresh = deferred<string>();
    const load = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(fresh.promise);
    const commit = vi.fn();
    const request = new ScopedQueryRequest({ load, commit });
    const before = request.run();
    const rejected = expect(before).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    await Promise.resolve();
    const recovery = request.run({
      fresh: true,
      signal: new AbortController().signal,
    });
    expect(request.run({ fresh: true })).toBe(recovery);
    fresh.resolve("current");
    await recovery;
    old.resolve("stale");
    await rejected;
    expect(commit).toHaveBeenCalledExactlyOnceWith("current");
  });
  it("rejects ignored-signal cancellation without committing late data", async () => {
    const data = deferred<string>();
    const commit = vi.fn();
    const request = new ScopedQueryRequest({
      load: () => data.promise,
      commit,
    });
    const controller = new AbortController();
    const result = request.run({ signal: controller.signal, fresh: true });
    const rejected = expect(result).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    controller.abort();
    await rejected;
    data.resolve("late");
    await Promise.resolve();
    expect(commit).not.toHaveBeenCalled();
  });
  it("propagates failure and permits a later successful retry", async () => {
    const commit = vi.fn();
    const onError = vi.fn();
    const load = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce("ok");
    const request = new ScopedQueryRequest({ load, commit, onError });
    await expect(request.run()).rejects.toThrow("offline");
    expect(commit).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledOnce();
    await request.run();
    expect(commit).toHaveBeenCalledWith("ok");
  });
  it("does not overwrite a newer request started by a loading callback", async () => {
    const old = deferred<string>();
    const next = deferred<string>();
    const load = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(next.promise);
    const commit = vi.fn();
    let nested: Promise<void> | undefined;
    let reenter = false;
    const request = new ScopedQueryRequest({
      load,
      commit,
      onLoading: (loading) => {
        if (!loading && reenter) {
          reenter = false;
          nested = request.run({ fresh: true });
        }
      },
    });
    const first = request.run();
    const firstRejected = expect(first).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    await Promise.resolve();
    reenter = true;
    await expect(request.run({ fresh: true })).rejects.toMatchObject({
      name: "AbortError",
    });
    next.resolve("nested");
    await nested;
    await firstRejected;
    expect(commit).toHaveBeenCalledExactlyOnceWith("nested");
  });
  it("allows ordinary partial data but validates strict recovery before commit", async () => {
    const commit = vi.fn();
    const request = new ScopedQueryRequest({
      load: async () => ({ partial: true }),
      commit,
      validateRecovery: (data) => {
        if (data.partial) throw new Error("incomplete");
      },
    });
    await request.run();
    expect(commit).toHaveBeenCalledTimes(1);
    await expect(
      request.run({ signal: new AbortController().signal, fresh: true }),
    ).rejects.toThrow("incomplete");
    expect(commit).toHaveBeenCalledTimes(1);
  });
});
