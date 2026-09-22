/** @vitest-environment jsdom */

import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { FileEntry } from "@/api/workspace";
import { useScopedFileTreeCore, type DirLoader } from "../useScopedFileTree";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

describe("useScopedFileTreeCore", () => {
  it("aborts and ignores a stale loader generation", async () => {
    const oldRoot = deferred<FileEntry[]>();
    const newRoot = deferred<FileEntry[]>();
    let oldSignal: AbortSignal | undefined;
    const oldLoader: DirLoader = vi.fn((_path, options) => {
      oldSignal = options?.signal;
      return oldRoot.promise;
    });
    const newLoader: DirLoader = vi.fn(() => newRoot.promise);
    const hook = renderHook(
      ({ loader }) => useScopedFileTreeCore(loader, true, false),
      { initialProps: { loader: oldLoader } },
    );
    expect(oldSignal?.aborted).toBe(false);

    hook.rerender({ loader: newLoader });
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => {
      newRoot.resolve([
        { name: "new", is_dir: true, size: 0, mod_time: "now" },
      ]);
      await newRoot.promise;
    });
    await act(async () => {
      oldRoot.resolve([
        { name: "old", is_dir: true, size: 0, mod_time: "then" },
      ]);
      await oldRoot.promise;
    });

    expect(
      hook.result.current.treeData.get("")?.map((entry) => entry.name),
    ).toEqual(["new"]);
  });
});
