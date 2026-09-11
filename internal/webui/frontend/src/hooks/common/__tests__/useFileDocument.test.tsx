/** @vitest-environment jsdom */

import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "../queryRecovery";

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { FileMutationData, FileReadData } from "@/api/workspace";
import { ApiError } from "@/types/common";
import { checkoutExplorerRef, skillsExplorerRef } from "@/utils/explorerRefs";
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
  ref: checkoutExplorerRef({ scope: "workspace" }),
  path: "src/main.ts",
};

const unconditional = () => false;

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
    conditionalSave: unconditional,
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
      () =>
        useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "src/main.ts",
        ),
      { wrapper },
    );
    const second = renderHook(
      () =>
        useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "src/main.ts",
        ),
      { wrapper },
    );

    await act(() => first.result.current.refresh());
    act(() => first.result.current.edit("shared draft"));
    expect(second.result.current.content).toBe("shared draft");
    expect(second.result.current.dirty).toBe(true);

    first.unmount();
    second.unmount();
    const reopened = renderHook(
      () =>
        useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "src/main.ts",
        ),
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
        workspace: useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "same.txt",
        ),
        otherWorkspace: useFileDocument(
          "ws-2",
          checkoutExplorerRef({ scope: "workspace" }),
          "same.txt",
        ),
        agent: useFileDocument(
          "ws-1",
          checkoutExplorerRef({
            scope: "agent",
            target: "atlas",
            repo: "repo-a",
          }),
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
    const scope = checkoutExplorerRef({ scope: "workspace" });
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
      () =>
        useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "slow.ts",
        ),
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
      () =>
        useFileDocument(
          "ws-1",
          checkoutExplorerRef({ scope: "workspace" }),
          "save.ts",
        ),
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
        ref: checkoutExplorerRef({ scope: "workspace" }),
        path: "save.ts",
      }),
    ).toMatchObject({ dirty: false, baseVersion: "v2" });
    registry.dispose();
  });

  it("keeps mounted hooks subscribed across reset and retarget", async () => {
    const { registry, wrapper } = setup();
    const scope = checkoutExplorerRef({ scope: "workspace" });
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
  it("keeps checkout saves unconditional and byte-compatible", async () => {
    const operations: FileDocumentOperations = {
      read: vi.fn().mockResolvedValue(file("base", "v1")),
      write: vi.fn().mockResolvedValue({ success: true, version: "v2" }),
      conditionalSave: unconditional,
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "checkout draft");
    await registry.save(workspaceRef);
    expect(operations.write).toHaveBeenCalledWith(
      workspaceRef,
      "checkout draft",
      expect.any(AbortSignal),
    );
    registry.dispose();
  });

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

  it("refreshes the external version before a conditional overwrite", async () => {
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockResolvedValueOnce(file("base", "v1"))
        .mockResolvedValueOnce(file("latest external", "v2")),
      write: vi.fn().mockResolvedValue({ success: true, version: "v3" }),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "local draft");

    await registry.overwriteExternal(workspaceRef);

    expect(operations.write).toHaveBeenCalledWith(
      workspaceRef,
      "local draft",
      expect.any(AbortSignal),
      "v2",
    );
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "local draft",
      baseVersion: "v3",
      dirty: false,
      externalConflict: null,
    });
    registry.dispose();
  });

  it("keeps a newer edit dirty against the content just overwritten", async () => {
    const pendingWrite = deferred<FileMutationData>();
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockResolvedValueOnce(file("base", "v1"))
        .mockResolvedValueOnce(file("external", "v2")),
      write: vi.fn().mockReturnValue(pendingWrite.promise),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "overwrite snapshot");
    const overwriting = registry.overwriteExternal(workspaceRef);
    await vi.waitFor(() => expect(operations.write).toHaveBeenCalled());
    registry.edit(workspaceRef, "newer local draft");
    pendingWrite.resolve({ success: true, version: "v3" });
    await overwriting;

    expect(registry.get(workspaceRef)).toMatchObject({
      content: "newer local draft",
      baseContent: "overwrite snapshot",
      baseVersion: "v3",
      dirty: true,
      externalConflict: { content: "overwrite snapshot", version: "v3" },
    });
    registry.dispose();
  });

  it("preserves the draft and refreshes conflict state after overwrite races", async () => {
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockResolvedValueOnce(file("base", "v1"))
        .mockResolvedValueOnce(file("first external", "v2"))
        .mockResolvedValueOnce(file("second external", "v3")),
      write: vi.fn().mockRejectedValue(
        new ApiError(412, "Precondition Failed", {
          error: "file changed",
        }),
      ),
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "local draft");

    await registry.overwriteExternal(workspaceRef);

    expect(operations.write).toHaveBeenCalledWith(
      workspaceRef,
      "local draft",
      expect.any(AbortSignal),
      "v2",
    );
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "local draft",
      dirty: true,
      error: "file changed",
      externalConflict: { content: "second external", version: "v3" },
    });
    registry.dispose();
  });

  it("sends the skills base revision and turns a 412 into merge state", async () => {
    const ref: FileDocumentRef = {
      workspaceId: "ws-1",
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/SKILL.md",
    };
    const operations: FileDocumentOperations = {
      read: vi
        .fn()
        .mockResolvedValueOnce(file("server body", "body-v1", ref.path))
        .mockResolvedValueOnce(file("external body", "body-v2", ref.path)),
      write: vi.fn().mockRejectedValue(
        new ApiError(412, "Precondition Failed", {
          code: "precondition_failed",
          error: "skill document changed",
          revision: "body-v2",
        }),
      ),
      conditionalSave: () => true,
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(ref);
    registry.edit(ref, "local body");

    await registry.save(ref);

    expect(operations.write).toHaveBeenCalledWith(
      ref,
      "local body",
      expect.any(AbortSignal),
      "body-v1",
    );
    expect(registry.get(ref)).toMatchObject({
      content: "local body",
      dirty: true,
      error: "skill document changed",
      externalConflict: {
        content: "external body",
        version: "body-v2",
      },
    });
    registry.dispose();
  });

  it("keeps a 409 ownership error plain with no overwrite conflict", async () => {
    const ref: FileDocumentRef = {
      workspaceId: "ws-1",
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/SKILL.md",
    };
    const operations: FileDocumentOperations = {
      read: vi.fn().mockResolvedValue(file("server", "v1", ref.path)),
      write: vi.fn().mockRejectedValue(
        new ApiError(409, "Conflict", {
          code: "skill_provenance_conflict",
          error: "skill is owned by loom skill sync",
        }),
      ),
      conditionalSave: () => true,
    };
    const registry = new FileDocumentRegistry(operations, undefined);
    await registry.refresh(ref);
    registry.edit(ref, "local");

    await registry.save(ref);

    expect(registry.get(ref)).toMatchObject({
      dirty: true,
      error: "skill is owned by loom skill sync",
      externalConflict: null,
    });
    expect(operations.read).toHaveBeenCalledTimes(1);
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

    registry.retargetPathPrefix(
      "ws-1",
      checkoutExplorerRef({ scope: "workspace" }),
      "src",
      "lib",
    );
    const moved = { ...nested, path: "lib/nested/file.ts" };
    expect(registry.get(moved)).toMatchObject({
      content: "retained draft",
      dirty: true,
    });
    registry.resetPathPrefix(
      "ws-1",
      checkoutExplorerRef({ scope: "workspace" }),
      "lib",
    );
    expect(registry.get(moved)).toMatchObject({ content: "", dirty: false });
    registry.dispose();
  });
});

describe("document recovery", () => {
  it("preserves dirty draft and base while recording external content", async () => {
    const read = vi
      .fn()
      .mockResolvedValueOnce(file("base", "v1"))
      .mockResolvedValueOnce(file("external", "v2"));
    const { registry } = setup({ read });
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "draft");
    const before = registry.get(workspaceRef);
    await registry.refreshForRecovery(
      workspaceRef,
      new AbortController().signal,
    );
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "draft",
      baseContent: "base",
      baseVersion: "v1",
      draftRevision: before.draftRevision,
      dirty: true,
      externalConflict: { content: "external", version: "v2" },
    });
    registry.dispose();
  });
  it("preserves edits during recovery and ordinary refresh joins", async () => {
    const pending = deferred<FileReadData>();
    const read = vi
      .fn()
      .mockResolvedValueOnce(file("base", "v1"))
      .mockReturnValueOnce(pending.promise);
    const { registry } = setup({ read });
    await registry.refresh(workspaceRef);
    const recovery = registry.refreshForRecovery(
      workspaceRef,
      new AbortController().signal,
    );
    await Promise.resolve();
    registry.edit(workspaceRef, "typed");
    const ordinary = registry.refresh(workspaceRef);
    pending.resolve(file("external", "v2"));
    await Promise.all([ordinary, recovery]);
    expect(read).toHaveBeenCalledTimes(2);
    expect(registry.get(workspaceRef)).toMatchObject({
      content: "typed",
      baseVersion: "v1",
      dirty: true,
      externalConflict: { version: "v2" },
    });
    registry.dispose();
  });
  it("rejects recovery during save without canceling write", async () => {
    const pending = deferred<FileMutationData>();
    const write = vi.fn().mockReturnValue(pending.promise);
    const { registry } = setup({ write });
    await registry.refresh(workspaceRef);
    registry.edit(workspaceRef, "draft");
    const saving = registry.save(workspaceRef);
    await expect(
      registry.refreshForRecovery(workspaceRef, new AbortController().signal),
    ).rejects.toThrow("save in progress");
    expect(write.mock.calls[0][2].aborted).toBe(false);
    pending.resolve({ success: true, version: "v2" });
    await saving;
    expect(registry.get(workspaceRef)).toMatchObject({
      baseVersion: "v2",
      dirty: false,
    });
    registry.dispose();
  });
  it("rejects failure and abort even when transport ignores signal", async () => {
    const pending = deferred<FileReadData>();
    const read = vi
      .fn()
      .mockRejectedValueOnce(new Error("unavailable"))
      .mockReturnValueOnce(pending.promise);
    const { registry } = setup({ read });
    await expect(
      registry.refreshForRecovery(workspaceRef, new AbortController().signal),
    ).rejects.toThrow("unavailable");
    const controller = new AbortController();
    const recovery = registry.refreshForRecovery(
      workspaceRef,
      controller.signal,
    );
    const rejected = expect(recovery).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    controller.abort();
    await rejected;
    pending.resolve(file("late", "v9"));
    await Promise.resolve();
    expect(registry.get(workspaceRef).baseVersion).toBeNull();
    registry.dispose();
  });
  it("fences reset and reused document keys", async () => {
    const old = deferred<FileReadData>();
    const read = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValue(file("new", "v2"));
    const { registry } = setup({ read });
    const recovery = registry.refreshForRecovery(
      workspaceRef,
      new AbortController().signal,
    );
    const rejected = expect(recovery).rejects.toMatchObject({
      name: "AbortError",
    });
    await Promise.resolve();
    registry.reset(workspaceRef);
    await registry.refreshForRecovery(
      workspaceRef,
      new AbortController().signal,
    );
    await rejected;
    old.resolve(file("old", "v1"));
    await Promise.resolve();
    expect(registry.get(workspaceRef).content).toBe("new");
    registry.dispose();
  });
  it("supersedes pre-recovery reads and rejects synchronous invalidation at commit", async () => {
    const old = deferred<FileReadData>();
    const read = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValue(file("fresh", "v2"));
    const { registry } = setup({ read });
    const ordinary = registry.refresh(workspaceRef);
    await registry.refreshForRecovery(
      workspaceRef,
      new AbortController().signal,
    );
    old.resolve(file("stale", "v1"));
    await ordinary;
    expect(registry.get(workspaceRef).content).toBe("fresh");
    let reset = false;
    const unsubscribe = registry.subscribe(workspaceRef, () => {
      if (!reset && !registry.get(workspaceRef).isLoading) {
        reset = true;
        registry.reset(workspaceRef);
      }
    });
    await expect(
      registry.refreshForRecovery(workspaceRef, new AbortController().signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    unsubscribe();
    registry.dispose();
  });
  it("deduplicates mounted documents and excludes retained closed drafts", async () => {
    const read = vi.fn().mockResolvedValue(file("server", "v1"));
    const { registry } = setup({ read });
    const coordinator = new QueryRecoveryCoordinator("ws-1");
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryRecoveryContext.Provider value={coordinator}>
        <FileDocumentRegistryProvider registry={registry}>
          {children}
        </FileDocumentRegistryProvider>
      </QueryRecoveryContext.Provider>
    );
    const useDocument = () =>
      useFileDocument("ws-1", workspaceRef.ref, workspaceRef.path);
    const first = renderHook(useDocument, { wrapper }),
      second = renderHook(useDocument, { wrapper });
    await act(async () => coordinator.refresh());
    expect(read).toHaveBeenCalledTimes(1);
    act(() => first.result.current.edit("retained"));
    first.unmount();
    second.unmount();
    read.mockClear();
    await act(async () => coordinator.refresh());
    expect(read).not.toHaveBeenCalled();
    expect(registry.get(workspaceRef).content).toBe("retained");
    registry.dispose();
  });
});
