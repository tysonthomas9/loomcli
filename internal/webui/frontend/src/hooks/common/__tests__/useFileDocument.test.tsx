/** @vitest-environment jsdom */

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { FileMutationData, FileReadData } from "@/api/workspace";
import {
  FileDocumentRegistry,
  type FileDocumentOperations,
  type FileDocumentRef,
} from "@/stores/fileDocumentRegistry";
import {
  FileDocumentRegistryProvider,
  useFileDocument,
} from "../useFileDocument";

const workspaceRef: FileDocumentRef = {
  workspaceId: "ws-1",
  scope: "workspace",
  path: "src/main.ts",
};

function file(
  content: string,
  version: string,
  path = workspaceRef.path,
): FileReadData {
  return {
    path,
    content,
    size: content.length,
    binary: false,
    version,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function setup(operations?: Partial<FileDocumentOperations>) {
  const defaults: FileDocumentOperations = {
    read: vi.fn().mockResolvedValue(file("server", "v1")),
    write: vi.fn().mockResolvedValue({ success: true, version: "v2" }),
  };
  const registry = new FileDocumentRegistry(
    { ...defaults, ...operations },
    undefined,
  );
  const wrapper = ({ children }: { children: ReactNode }) => (
    <FileDocumentRegistryProvider registry={registry}>
      {children}
    </FileDocumentRegistryProvider>
  );
  return { registry, wrapper };
}

describe("useFileDocument", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("shares edits between two views and retains a dirty draft after close", async () => {
    const { registry, wrapper } = setup();
    const first = renderHook(
      () => useFileDocument("ws-1", { scope: "workspace" }, "src/main.ts"),
      { wrapper },
    );
    const second = renderHook(
      () => useFileDocument("ws-1", { scope: "workspace" }, "src/main.ts"),
      { wrapper },
    );

    await act(() => first.result.current.refresh());
    act(() => first.result.current.edit("shared draft"));
    expect(second.result.current.content).toBe("shared draft");
    expect(second.result.current.dirty).toBe(true);

    first.unmount();
    second.unmount();
    const reopened = renderHook(
      () => useFileDocument("ws-1", { scope: "workspace" }, "src/main.ts"),
      { wrapper },
    );
    expect(reopened.result.current.content).toBe("shared draft");
    expect(reopened.result.current.dirty).toBe(true);

    act(() => reopened.result.current.discard());
    expect(reopened.result.current.content).toBe("server");
    expect(reopened.result.current.dirty).toBe(false);
    reopened.unmount();
    registry.dispose();
  });

  it("isolates drafts by workspace and checkout ref", () => {
    const { registry, wrapper } = setup();
    const { result } = renderHook(
      () => ({
        workspace: useFileDocument("ws-1", { scope: "workspace" }, "same.txt"),
        otherWorkspace: useFileDocument(
          "ws-2",
          { scope: "workspace" },
          "same.txt",
        ),
        agent: useFileDocument(
          "ws-1",
          { scope: "agent", target: "atlas", repo: "repo-a" },
          "same.txt",
        ),
      }),
      { wrapper },
    );

    act(() => result.current.workspace.edit("only here"));
    expect(result.current.workspace.content).toBe("only here");
    expect(result.current.otherWorkspace.content).toBe("");
    expect(result.current.agent.content).toBe("");
    registry.dispose();
  });

  it("ignores a stale A read after switching rapidly to B", async () => {
    const a = deferred<FileReadData>();
    const b = deferred<FileReadData>();
    const read = vi.fn((ref: FileDocumentRef) =>
      ref.path === "a.ts" ? a.promise : b.promise,
    );
    const { registry, wrapper } = setup({ read });
    const scope = { scope: "workspace" as const };
    const hook = renderHook(
      ({ path }) => useFileDocument("ws-1", scope, path),
      { initialProps: { path: "a.ts" }, wrapper },
    );

    let aRefresh!: Promise<void>;
    act(() => {
      aRefresh = hook.result.current.refresh();
    });
    hook.rerender({ path: "b.ts" });
    let bRefresh!: Promise<void>;
    act(() => {
      bRefresh = hook.result.current.refresh();
    });
    b.resolve(file("B", "b1", "b.ts"));
    await act(() => bRefresh);
    a.resolve(file("A", "a1", "a.ts"));
    await act(() => aRefresh);

    expect(hook.result.current.ref.path).toBe("b.ts");
    expect(hook.result.current.content).toBe("B");
    registry.dispose();
  });

  it("aborts an in-flight read when the final view unmounts", () => {
    let signal: AbortSignal | undefined;
    const read = vi.fn((_ref: FileDocumentRef, nextSignal: AbortSignal) => {
      signal = nextSignal;
      return new Promise<FileReadData>(() => undefined);
    });
    const { registry, wrapper } = setup({ read });
    const hook = renderHook(
      () => useFileDocument("ws-1", { scope: "workspace" }, "slow.ts"),
      { wrapper },
    );
    act(() => void hook.result.current.refresh());
    expect(signal?.aborted).toBe(false);
    hook.unmount();
    expect(signal?.aborted).toBe(true);
    registry.dispose();
  });

  it("lets an explicit save finish after the final view unmounts", async () => {
    const pendingSave = deferred<FileMutationData>();
    let saveSignal: AbortSignal | undefined;
    const write = vi.fn(
      (_ref: FileDocumentRef, _content: string, signal: AbortSignal) => {
        saveSignal = signal;
        return pendingSave.promise;
      },
    );
    const { registry, wrapper } = setup({ write });
    const hook = renderHook(
      () => useFileDocument("ws-1", { scope: "workspace" }, "save.ts"),
      { wrapper },
    );
    await act(() => hook.result.current.refresh());
    act(() => hook.result.current.edit("saved after close"));
    let saving!: Promise<FileMutationData | null>;
    act(() => {
      saving = hook.result.current.save();
    });
    hook.unmount();
    expect(saveSignal?.aborted).toBe(false);
    pendingSave.resolve({ success: true, version: "v2" });
    await saving;
    expect(
      registry.get({
        workspaceId: "ws-1",
        scope: "workspace",
        path: "save.ts",
      }),
    ).toMatchObject({ dirty: false, baseVersion: "v2" });
    registry.dispose();
  });

  it("keeps mounted hooks subscribed across reset and retarget", async () => {
    const { registry, wrapper } = setup();
    const scope = { scope: "workspace" as const };
    const hook = renderHook(
      ({ path }) => useFileDocument("ws-1", scope, path),
      { initialProps: { path: "src/mounted.ts" }, wrapper },
    );
    await act(() => hook.result.current.refresh());
    act(() => hook.result.current.edit("before reset"));

    act(() => registry.resetPathPrefix("ws-1", scope, "src/mounted.ts"));
    expect(hook.result.current).toMatchObject({ content: "", dirty: false });
    act(() => hook.result.current.edit("after reset"));
    expect(hook.result.current).toMatchObject({
      content: "after reset",
      dirty: true,
    });

    act(() =>
      registry.retargetPathPrefix(
        "ws-1",
        scope,
        "src/mounted.ts",
        "lib/mounted.ts",
      ),
    );
    expect(hook.result.current).toMatchObject({ content: "", dirty: false });
    hook.rerender({ path: "lib/mounted.ts" });
    expect(hook.result.current).toMatchObject({
      content: "after reset",
      dirty: true,
    });
    act(() => hook.result.current.edit("after retarget"));
    expect(hook.result.current.content).toBe("after retarget");
    registry.dispose();
  });
});

describe("FileDocumentRegistry coordination", () => {
  it("installs exactly one beforeunload guard while any draft is dirty", () => {
    const listeners = new Set<(event: BeforeUnloadEvent) => void>();
    const target = {
      addEventListener: vi.fn(
        (_type: "beforeunload", listener: (event: BeforeUnloadEvent) => void) =>
          listeners.add(listener),
      ),
      removeEventListener: vi.fn(
        (_type: "beforeunload", listener: (event: BeforeUnloadEvent) => void) =>
          listeners.delete(listener),
      ),
    };
    const operations: FileDocumentOperations = {
      read: vi.fn(),
      write: vi.fn(),
    };
    const registry = new FileDocumentRegistry(operations, target);

    registry.edit(workspaceRef, "one");
    registry.edit(workspaceRef, "two");
    registry.edit({ ...workspaceRef, path: "other.ts" }, "three");
    expect(target.addEventListener).toHaveBeenCalledTimes(1);
    expect(listeners.size).toBe(1);

    registry.discard(workspaceRef);
    expect(target.removeEventListener).not.toHaveBeenCalled();
    registry.discard({ ...workspaceRef, path: "other.ts" });
    expect(target.removeEventListener).toHaveBeenCalledTimes(1);
    expect(listeners.size).toBe(0);
    registry.dispose();
  });

  it("refreshes clean content and records only real dirty external conflicts", async () => {
    const responses = [
      file("base", "v1"),
      file("external", "v2"),
      file("external again", "v3"),
      file("same base", "v3"),
    ];
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockImplementation(() => Promise.resolve(responses.shift()!)),
      write: vi.fn(),
    };
    const registry = new FileDocumentRegistry(operations, undefined);

    await registry.refresh(workspaceRef);
    expect(registry.get(workspaceRef).content).toBe("base");
    await registry.refresh(workspaceRef);
    expect(registry.get(workspaceRef).content).toBe("external");
    expect(registry.get(workspaceRef).baseVersion).toBe("v2");

    registry.edit(workspaceRef, "local");
    await registry.refresh(workspaceRef);
    expect(registry.get(workspaceRef).content).toBe("local");
    expect(registry.get(workspaceRef).externalConflict).toMatchObject({
      content: "external again",
      version: "v3",
    });
    registry.useExternal(workspaceRef);
    registry.edit(workspaceRef, "local after v3");
    await registry.refresh(workspaceRef);
    expect(registry.get(workspaceRef).externalConflict).toBeNull();
    expect(registry.get(workspaceRef).content).toBe("local after v3");
    registry.dispose();
  });

  it("orders refresh generations even when an aborted request still resolves", async () => {
    const first = deferred<FileReadData>();
    const second = deferred<FileReadData>();
    const signals: AbortSignal[] = [];
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockImplementationOnce((_ref, signal) => {
          signals.push(signal);
          return first.promise;
        })
        .mockImplementationOnce((_ref, signal) => {
          signals.push(signal);
          return second.promise;
        }),
      write: vi.fn(),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    const firstRefresh = registry.refresh(workspaceRef);
    const secondRefresh = registry.refresh(workspaceRef);
    expect(signals[0]?.aborted).toBe(true);
    second.resolve(file("new", "v2"));
    await secondRefresh;
    first.resolve(file("old", "v1"));
    await firstRefresh;
    expect(registry.get(workspaceRef).content).toBe("new");
    expect(registry.get(workspaceRef).baseVersion).toBe("v2");
    registry.dispose();
  });

  it("keeps a newer edit dirty when a save response arrives", async () => {
    const pendingSave = deferred<FileMutationData>();
    const operations: FileDocumentOperations = {
      read: vi.fn().mockResolvedValue(file("base", "v1")),
      write: vi.fn().mockReturnValue(pendingSave.promise),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "saved snapshot");
    const saving = registry.save(workspaceRef);
    registry.edit(workspaceRef, "newer draft");
    pendingSave.resolve({ success: true, version: "v2" });
    await saving;

    expect(registry.get(workspaceRef)).toMatchObject({
      content: "newer draft",
      baseContent: "saved snapshot",
      baseVersion: "v2",
      dirty: true,
      isSaving: false,
    });
    registry.dispose();
  });

  it("does not let a focus-style refresh abort an in-flight save", async () => {
    const pendingSave = deferred<FileMutationData>();
    const saveSignals: AbortSignal[] = [];
    const operations: FileDocumentOperations = {
      read: vi.fn().mockResolvedValue(file("base", "v1")),
      write: vi.fn().mockImplementation((_ref, _content, signal) => {
        saveSignals.push(signal);
        return pendingSave.promise;
      }),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "saving");
    const saving = registry.save(workspaceRef);
    await registry.refresh(workspaceRef);
    expect(saveSignals[0]?.aborted).toBe(false);
    expect(operations.read).toHaveBeenCalledTimes(1);
    pendingSave.resolve({ success: true, version: "v2" });
    await saving;
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "saving",
      baseVersion: "v2",
      dirty: false,
    });
    registry.dispose();
  });

  it("preserves an external conflict when edits continue during save", async () => {
    const pendingSave = deferred<FileMutationData>();
    const responses = [file("base", "v1"), file("external", "v2")];
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockImplementation(() => Promise.resolve(responses.shift()!)),
      write: vi.fn().mockReturnValue(pendingSave.promise),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "first local draft");
    await registry.refresh(workspaceRef);
    expect(registry.get(workspaceRef).externalConflict?.version).toBe("v2");

    const saving = registry.save(workspaceRef);
    registry.edit(workspaceRef, "newer local draft");
    pendingSave.resolve({ success: true, version: "v3" });
    await saving;
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "newer local draft",
      dirty: true,
      externalConflict: { content: "external", version: "v2" },
    });
    registry.dispose();
  });

  it("retargets and explicitly resets retained drafts after path mutations", async () => {
    const operations: FileDocumentOperations = {
      read: vi.fn().mockResolvedValue(file("base", "v1")),
      write: vi.fn(),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    const nested = { ...workspaceRef, path: "src/nested/file.ts" };
    await registry.refresh(nested);
    registry.edit(nested, "retained draft");

    registry.retargetPathPrefix("ws-1", { scope: "workspace" }, "src", "lib");
    const moved = { ...nested, path: "lib/nested/file.ts" };
    expect(registry.get(moved)).toMatchObject({
      content: "retained draft",
      dirty: true,
    });
    registry.resetPathPrefix("ws-1", { scope: "workspace" }, "lib");
    expect(registry.get(moved)).toMatchObject({ content: "", dirty: false });
    registry.dispose();
  });
});
