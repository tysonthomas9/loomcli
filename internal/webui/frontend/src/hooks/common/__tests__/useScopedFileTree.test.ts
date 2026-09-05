/** @vitest-environment jsdom */

import { createElement, type ReactNode } from "react";
import {
  QueryRecoveryCoordinator,
  QueryRecoveryContext,
} from "../queryRecovery";
import { act, renderHook, waitFor } from "@testing-library/react";
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
  function recoveryWrapper(coordinator: QueryRecoveryCoordinator) {
    return function Wrapper({ children }: { children: ReactNode }) {
      return createElement(
        QueryRecoveryContext.Provider,
        { value: coordinator },
        children,
      );
    };
  }
  const entry = (name: string, is_dir = false): FileEntry => ({
    name,
    is_dir,
    size: 0,
    mod_time: "now",
  });

  it("rejects any expanded directory failure without partial commit or losing selection", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const loader = vi.fn<DirLoader>(async (path) =>
      path === "" ? [entry("a", true)] : [entry("before")],
    );
    const hook = renderHook(() => useScopedFileTreeCore(loader, true, true), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() =>
      expect(hook.result.current.treeData.has("")).toBe(true),
    );
    await act(async () => hook.result.current.toggle("a"));
    act(() => hook.result.current.selectFile("a/file"));
    const before = hook.result.current.treeData;
    loader.mockImplementation(async (path) => {
      if (path === "a") throw new Error("directory unavailable");
      return [entry("a", true), entry("after")];
    });
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow(
        "directory unavailable",
      );
    });
    expect(hook.result.current.treeData).toBe(before);
    expect(hook.result.current.selectedPath).toBe("a/file");
    expect(hook.result.current.expanded).toEqual(new Set(["", "a"]));
  });

  it("fences an old ordinary directory response after recovery commits", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const pending = deferred<FileEntry[]>();
    const loader = vi.fn<DirLoader>(async () => [entry("a", true)]);
    const hook = renderHook(() => useScopedFileTreeCore(loader, true, true), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() =>
      expect(hook.result.current.treeData.has("")).toBe(true),
    );
    loader.mockReturnValueOnce(pending.promise);
    let ordinary!: Promise<void>;
    act(() => {
      ordinary = hook.result.current.toggle("a");
    });
    loader.mockImplementation(async (path) =>
      path === "" ? [entry("a", true)] : [entry("fresh")],
    );
    await act(async () => coordinator.refresh());
    await act(async () => {
      pending.resolve([entry("stale")]);
      await ordinary;
    });
    expect(hook.result.current.treeData.get("a")).toEqual([entry("fresh")]);
  });

  it("includes expansion changes during a recovery before acknowledging", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const root = deferred<FileEntry[]>();
    const directory = deferred<FileEntry[]>();
    const loader = vi.fn<DirLoader>(async () => []);
    const hook = renderHook(() => useScopedFileTreeCore(loader, true, true), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() =>
      expect(hook.result.current.treeData.has("")).toBe(true),
    );
    const before = hook.result.current.treeData;
    loader.mockImplementation(async (path) =>
      path === "new-directory"
        ? directory.promise
        : [entry("new-directory", true)],
    );
    loader.mockReturnValueOnce(root.promise);
    let recovery!: Promise<void>;
    act(() => {
      recovery = coordinator.refresh();
    });
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(2));
    let expanded!: Promise<void>;
    act(() => {
      expanded = hook.result.current.toggle("new-directory");
    });
    await act(async () => {
      root.resolve([entry("intermediate-root")]);
    });
    await waitFor(() =>
      expect(loader.mock.calls.some(([path]) => path === "new-directory")).toBe(
        true,
      ),
    );
    expect(hook.result.current.treeData).toBe(before);
    await act(async () => {
      directory.resolve([]);
      await recovery;
      await expanded;
    });
    expect(hook.result.current.treeData.has("new-directory")).toBe(true);
    expect(hook.result.current.expanded.has("new-directory")).toBe(true);
  });

  it("aborts recovery on loader changes and ignores late results from the old scope", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const old = deferred<FileEntry[]>();
    const loader = vi.fn<DirLoader>(async () => []);
    const next = vi.fn<DirLoader>(async () => [entry("new-scope")]);
    const hook = renderHook(
      ({ load }) => useScopedFileTreeCore(load, true, true),
      { wrapper: recoveryWrapper(coordinator), initialProps: { load: loader } },
    );
    await waitFor(() =>
      expect(hook.result.current.treeData.has("")).toBe(true),
    );
    loader.mockReturnValueOnce(old.promise);
    let recovery!: Promise<void>;
    act(() => {
      recovery = coordinator.refresh();
    });
    await waitFor(() => expect(loader).toHaveBeenCalledTimes(2));
    const signal = loader.mock.calls[1]?.[1]?.signal;
    hook.rerender({ load: next });
    await act(async () => recovery);
    expect(signal?.aborted).toBe(true);
    await act(async () => old.resolve([entry("old-scope")]));
    expect(hook.result.current.treeData.get("")).toEqual([entry("new-scope")]);
  });
  it("recovers an initial root read and fences its ignored-abort late result", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const oldRoot = deferred<FileEntry[]>();
    const loader = vi.fn<DirLoader>(async () => [entry("fresh")]);
    loader.mockReturnValueOnce(oldRoot.promise);
    const hook = renderHook(() => useScopedFileTreeCore(loader, true, true), {
      wrapper: recoveryWrapper(coordinator),
    });
    await act(async () => coordinator.refresh());
    expect(hook.result.current.isLoading).toBe(false);
    expect(hook.result.current.expanded.has("")).toBe(true);
    await act(async () => oldRoot.resolve([entry("stale")]));
    expect(hook.result.current.treeData.get("")).toEqual([entry("fresh")]);
  });

  it("prunes a deleted expanded subtree only after its fresh parent proves absence", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const loader = vi.fn<DirLoader>(async (path) =>
      path === "" ? [entry("a", true)] : [entry("old")],
    );
    const hook = renderHook(() => useScopedFileTreeCore(loader, true, true), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() =>
      expect(hook.result.current.treeData.has("")).toBe(true),
    );
    await act(async () => hook.result.current.toggle("a"));
    loader.mockClear();
    loader.mockImplementation(async (path) => {
      if (path) throw new Error("404 deleted directory");
      return [];
    });
    await act(async () => coordinator.refresh());
    expect(loader.mock.calls.every(([path]) => path === "")).toBe(true);
    expect(hook.result.current.treeData.has("a")).toBe(false);
    expect(hook.result.current.expanded.has("a")).toBe(false);
  });
});
