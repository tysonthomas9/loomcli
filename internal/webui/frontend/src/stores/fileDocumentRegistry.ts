import type { FileMutationData, FileReadData } from "@/api/workspace";
import { cleanPath } from "@/utils/fileExplorerRefs";
import {
  explorerRefKey,
  isSkillsRef,
  normalizeExplorerRef,
  sameExplorerRef,
  type CheckoutExplorerRef,
  type ExplorerRef,
  type SkillsExplorerRef,
} from "@/utils/explorerRefs";

export interface FileDocumentRefOf<R extends ExplorerRef> {
  workspaceId: string;
  ref: R;
  path: string;
}

export type FileDocumentRef = FileDocumentRefOf<ExplorerRef>;
export type CheckoutDocumentRef = FileDocumentRefOf<CheckoutExplorerRef>;
export type SkillsDocumentRef = FileDocumentRefOf<SkillsExplorerRef>;

export function isSkillsDocumentRef(
  ref: FileDocumentRef,
): ref is SkillsDocumentRef {
  return isSkillsRef(ref.ref);
}

export interface ExternalFileConflict {
  fileData: FileReadData;
  content: string;
  version: string;
}

export interface FileDocumentState {
  key: string;
  ref: FileDocumentRef;
  fileData: FileReadData | null;
  content: string;
  baseContent: string;
  baseVersion: string | null;
  dirty: boolean;
  isLoading: boolean;
  isSaving: boolean;
  error: string | null;
  externalConflict: ExternalFileConflict | null;
  requestGeneration: number;
  draftRevision: number;
}

export interface FileDocumentTransport<R extends FileDocumentRef> {
  read: (ref: R, signal: AbortSignal) => Promise<FileReadData>;
  write: (
    ref: R,
    content: string,
    signal: AbortSignal,
    ifMatch?: string,
  ) => Promise<FileMutationData>;
  conditionalSave: boolean;
}

export interface FileDocumentOperations {
  read: FileDocumentTransport<FileDocumentRef>["read"];
  write: FileDocumentTransport<FileDocumentRef>["write"];
  conditionalSave: (ref: FileDocumentRef) => boolean;
}

export function routeFileDocumentOperations(routes: {
  checkout: FileDocumentTransport<CheckoutDocumentRef>;
  skills: FileDocumentTransport<SkillsDocumentRef>;
}): FileDocumentOperations {
  return {
    read: (ref, signal) =>
      isSkillsDocumentRef(ref)
        ? routes.skills.read(ref, signal)
        : routes.checkout.read(ref as CheckoutDocumentRef, signal),
    write: (ref, content, signal, ifMatch) =>
      isSkillsDocumentRef(ref)
        ? routes.skills.write(ref, content, signal, ifMatch)
        : routes.checkout.write(
            ref as CheckoutDocumentRef,
            content,
            signal,
            ifMatch,
          ),
    conditionalSave: (ref) =>
      isSkillsDocumentRef(ref)
        ? routes.skills.conditionalSave
        : routes.checkout.conditionalSave,
  };
}

interface DocumentRuntime {
  readController: AbortController | null;
  saveController: AbortController | null;
  listeners: Set<() => void>;
}

interface BeforeUnloadTarget {
  addEventListener: (
    type: "beforeunload",
    listener: (event: BeforeUnloadEvent) => void,
  ) => void;
  removeEventListener: (
    type: "beforeunload",
    listener: (event: BeforeUnloadEvent) => void,
  ) => void;
}

export function fileDocumentKey(ref: FileDocumentRef): string {
  const normalized = normalizeExplorerRef(ref.ref);
  return [
    ref.workspaceId,
    explorerRefKey(normalized),
    cleanPath(ref.path),
  ].join("\u001e");
}

function normalizeDocumentRef(ref: FileDocumentRef): FileDocumentRef {
  return {
    workspaceId: ref.workspaceId,
    ref: normalizeExplorerRef(ref.ref),
    path: cleanPath(ref.path),
  };
}

function initialState(ref: FileDocumentRef): FileDocumentState {
  const normalized = normalizeDocumentRef(ref);
  return {
    key: fileDocumentKey(normalized),
    ref: normalized,
    fileData: null,
    content: "",
    baseContent: "",
    baseVersion: null,
    dirty: false,
    isLoading: false,
    isSaving: false,
    error: null,
    externalConflict: null,
    requestGeneration: 0,
    draftRevision: 0,
  };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function isPreconditionFailure(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "status" in error &&
    (error as { status?: unknown }).status === 412
  );
}

export class FileDocumentRegistry {
  private readonly states = new Map<string, FileDocumentState>();
  private readonly runtimes = new Map<string, DocumentRuntime>();
  private readonly listeners = new Set<() => void>();
  private revision = 0;
  private beforeUnloadInstalled = false;

  private readonly handleBeforeUnload = (event: BeforeUnloadEvent): void => {
    event.preventDefault();
    event.returnValue = "";
  };

  constructor(
    private readonly operations: FileDocumentOperations,
    private readonly beforeUnloadTarget:
      | BeforeUnloadTarget
      | undefined = typeof window === "undefined" ? undefined : window,
  ) {}

  get(ref: FileDocumentRef): FileDocumentState {
    const key = fileDocumentKey(ref);
    const existing = this.states.get(key);
    if (existing) return existing;
    const state = initialState(ref);
    this.states.set(key, state);
    this.runtimes.set(key, {
      readController: null,
      saveController: null,
      listeners: new Set(),
    });
    return state;
  }

  getRevision(): number {
    return this.revision;
  }

  subscribeAll(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  subscribe(ref: FileDocumentRef, listener: () => void): () => void {
    const state = this.get(ref);
    const runtime = this.runtime(state.key);
    runtime.listeners.add(listener);
    return () => {
      runtime.listeners.delete(listener);
      if (runtime.listeners.size === 0 && runtime.readController) {
        runtime.readController.abort();
        runtime.readController = null;
        const current = this.states.get(state.key);
        if (current?.isLoading) {
          this.replace(state.key, {
            ...current,
            isLoading: false,
            requestGeneration: current.requestGeneration + 1,
          });
        }
      }
    };
  }

  edit(ref: FileDocumentRef, content: string): void {
    const current = this.get(ref);
    this.replace(current.key, {
      ...current,
      content,
      dirty: content !== current.baseContent,
      draftRevision: current.draftRevision + 1,
    });
  }

  async refresh(ref: FileDocumentRef): Promise<void> {
    if (this.get(ref).isSaving) return;
    const started = this.beginRequest(ref, "read");
    try {
      const data = await this.operations.read(started.ref, started.signal);
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) return;

      const serverContent = data.binary ? "" : (data.content ?? "");
      if (current.dirty) {
        this.replace(started.key, {
          ...current,
          fileData: current.fileData ?? data,
          isLoading: false,
          error: null,
          externalConflict:
            data.version === current.baseVersion
              ? null
              : {
                  fileData: data,
                  content: serverContent,
                  version: data.version,
                },
        });
        return;
      }

      this.replace(started.key, {
        ...current,
        fileData: data,
        content: serverContent,
        baseContent: serverContent,
        baseVersion: data.version,
        dirty: false,
        isLoading: false,
        error: null,
        externalConflict: null,
      });
    } catch (error) {
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) return;
      this.replace(started.key, {
        ...current,
        isLoading: false,
        error: isAbortError(error) ? current.error : errorMessage(error),
      });
    } finally {
      this.finishRequest(started.key, started.generation);
    }
  }

  async save(ref: FileDocumentRef): Promise<FileMutationData | null> {
    const before = this.get(ref);
    if (!before.dirty || before.isSaving) return null;
    const savedContent = before.content;
    const savedDraftRevision = before.draftRevision;
    const started = this.beginRequest(ref, "save");
    try {
      const result = this.operations.conditionalSave?.(started.ref)
        ? await this.operations.write(
            started.ref,
            savedContent,
            started.signal,
            before.baseVersion ?? undefined,
          )
        : await this.operations.write(
            started.ref,
            savedContent,
            started.signal,
          );
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) {
        return null;
      }
      const draftUnchanged = current.draftRevision === savedDraftRevision;
      const nextFileData: FileReadData | null = current.fileData
        ? {
            ...current.fileData,
            content: savedContent,
            size: new Blob([savedContent]).size,
            version: result.version,
          }
        : null;
      this.replace(started.key, {
        ...current,
        fileData: nextFileData,
        content: draftUnchanged ? savedContent : current.content,
        baseContent: savedContent,
        baseVersion: result.version,
        dirty: !draftUnchanged,
        isSaving: false,
        error: null,
        externalConflict: draftUnchanged ? null : current.externalConflict,
      });
      return result;
    } catch (error) {
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) {
        return null;
      }
      let latestConflict = current.externalConflict;
      if (isPreconditionFailure(error)) {
        try {
          const latest = await this.operations.read(
            started.ref,
            started.signal,
          );
          latestConflict = {
            fileData: latest,
            content: latest.binary ? "" : (latest.content ?? ""),
            version: latest.version,
          };
        } catch {
          // Keep the prior snapshot when the conflict refresh also fails.
        }
      }
      this.replace(started.key, {
        ...current,
        isSaving: false,
        dirty: true,
        error: isAbortError(error) ? current.error : errorMessage(error),
        externalConflict: latestConflict,
      });
      return null;
    } finally {
      this.finishRequest(started.key, started.generation);
    }
  }

  async overwriteExternal(
    ref: FileDocumentRef,
  ): Promise<FileMutationData | null> {
    const before = this.get(ref);
    if (!before.dirty || before.isSaving) return null;
    const draft = before.content;
    const draftRevision = before.draftRevision;
    const started = this.beginRequest(ref, "save");
    try {
      const latest = await this.operations.read(started.ref, started.signal);
      const result = await this.operations.write(
        started.ref,
        draft,
        started.signal,
        latest.version,
      );
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) {
        return null;
      }
      const draftUnchanged = current.draftRevision === draftRevision;
      const savedFileData: FileReadData = {
        ...latest,
        content: draft,
        size: new Blob([draft]).size,
        version: result.version,
      };
      this.replace(started.key, {
        ...current,
        fileData: savedFileData,
        content: draftUnchanged ? draft : current.content,
        baseContent: draft,
        baseVersion: result.version,
        dirty: !draftUnchanged,
        isSaving: false,
        error: null,
        externalConflict: draftUnchanged
          ? null
          : {
              fileData: savedFileData,
              content: draft,
              version: result.version,
            },
      });
      return result;
    } catch (error) {
      const current = this.states.get(started.key);
      if (!current || current.requestGeneration !== started.generation) {
        return null;
      }
      let latestConflict = current.externalConflict;
      if (isPreconditionFailure(error)) {
        try {
          const latest = await this.operations.read(
            started.ref,
            started.signal,
          );
          latestConflict = {
            fileData: latest,
            content: latest.binary ? "" : (latest.content ?? ""),
            version: latest.version,
          };
        } catch {
          // Keep the prior conflict snapshot when the refresh also fails.
        }
      }
      this.replace(started.key, {
        ...current,
        isSaving: false,
        dirty: true,
        error: isAbortError(error) ? current.error : errorMessage(error),
        externalConflict: latestConflict,
      });
      return null;
    } finally {
      this.finishRequest(started.key, started.generation);
    }
  }

  discard(ref: FileDocumentRef): void {
    const current = this.get(ref);
    this.replace(current.key, {
      ...current,
      content: current.baseContent,
      dirty: false,
      externalConflict: null,
      error: null,
      draftRevision: current.draftRevision + 1,
    });
  }

  useExternal(ref: FileDocumentRef): void {
    const current = this.get(ref);
    const conflict = current.externalConflict;
    if (!conflict) return;
    this.replace(current.key, {
      ...current,
      fileData: conflict.fileData,
      content: conflict.content,
      baseContent: conflict.content,
      baseVersion: conflict.version,
      dirty: false,
      externalConflict: null,
      error: null,
      draftRevision: current.draftRevision + 1,
    });
  }

  reset(ref?: FileDocumentRef): void {
    if (ref) {
      const key = fileDocumentKey(ref);
      this.resetKey(key);
    } else {
      for (const key of [...this.states.keys()]) this.resetKey(key);
    }
    this.syncBeforeUnload();
  }

  resetPathPrefix(workspaceId: string, ref: ExplorerRef, path: string): void {
    const prefix = cleanPath(path);
    for (const [key, state] of this.states) {
      if (
        state.ref.workspaceId === workspaceId &&
        sameExplorerRef(state.ref.ref, ref) &&
        (state.ref.path === prefix || state.ref.path.startsWith(`${prefix}/`))
      ) {
        this.resetKey(key);
      }
    }
    this.syncBeforeUnload();
  }

  dirtyPathsForPrefix(
    workspaceId: string,
    ref: ExplorerRef,
    path: string,
  ): string[] {
    const prefix = cleanPath(path);
    return [...this.states.values()]
      .filter(
        (state) =>
          state.dirty &&
          state.ref.workspaceId === workspaceId &&
          sameExplorerRef(state.ref.ref, ref) &&
          (state.ref.path === prefix ||
            state.ref.path.startsWith(`${prefix}/`)),
      )
      .map((state) => state.ref.path);
  }

  retargetPathPrefix(
    workspaceId: string,
    ref: ExplorerRef,
    from: string,
    to: string,
  ): void {
    const source = cleanPath(from);
    const destination = cleanPath(to);
    const moves = [...this.states.entries()].filter(
      ([, state]) =>
        state.ref.workspaceId === workspaceId &&
        sameExplorerRef(state.ref.ref, ref) &&
        (state.ref.path === source || state.ref.path.startsWith(`${source}/`)),
    );
    for (const [oldKey, state] of moves) {
      const suffix = state.ref.path.slice(source.length);
      const nextRef = normalizeDocumentRef({
        ...state.ref,
        path: `${destination}${suffix}`,
      });
      const nextKey = fileDocumentKey(nextRef);
      const runtime = this.runtimes.get(oldKey);
      runtime?.readController?.abort();
      runtime?.saveController?.abort();
      const nextState: FileDocumentState = {
        ...state,
        key: nextKey,
        ref: nextRef,
        isLoading: false,
        isSaving: false,
        requestGeneration: state.requestGeneration + 1,
      };

      let nextRuntime = this.runtimes.get(nextKey);
      if (!nextRuntime) {
        nextRuntime = {
          readController: null,
          saveController: null,
          listeners: new Set(),
        };
        this.runtimes.set(nextKey, nextRuntime);
      } else if (nextKey !== oldKey) {
        nextRuntime.readController?.abort();
        nextRuntime.saveController?.abort();
        nextRuntime.readController = null;
        nextRuntime.saveController = null;
      }
      this.replace(nextKey, nextState);
      if (nextKey !== oldKey) this.resetKey(oldKey);
    }
  }

  dispose(): void {
    for (const key of [...this.states.keys()]) this.forceDelete(key);
    this.listeners.clear();
    if (this.beforeUnloadInstalled) {
      this.beforeUnloadTarget?.removeEventListener(
        "beforeunload",
        this.handleBeforeUnload,
      );
      this.beforeUnloadInstalled = false;
    }
  }

  private beginRequest(
    ref: FileDocumentRef,
    kind: "read" | "save",
  ): {
    key: string;
    ref: FileDocumentRef;
    generation: number;
    signal: AbortSignal;
  } {
    const current = this.get(ref);
    const runtime = this.runtime(current.key);
    const controller = new AbortController();
    if (kind === "read") {
      runtime.readController?.abort();
      runtime.readController = controller;
    } else {
      runtime.readController?.abort();
      runtime.readController = null;
      runtime.saveController = controller;
    }
    const generation = current.requestGeneration + 1;
    this.replace(current.key, {
      ...current,
      requestGeneration: generation,
      isLoading: kind === "read",
      isSaving: kind === "save",
      error: null,
    });
    return {
      key: current.key,
      ref: current.ref,
      generation,
      signal: controller.signal,
    };
  }

  private finishRequest(key: string, generation: number): void {
    const current = this.states.get(key);
    const runtime = this.runtimes.get(key);
    if (current?.requestGeneration === generation && runtime) {
      runtime.readController = null;
      runtime.saveController = null;
    }
  }

  private runtime(key: string): DocumentRuntime {
    const runtime = this.runtimes.get(key);
    if (!runtime) throw new Error(`missing document runtime for ${key}`);
    return runtime;
  }

  private replace(key: string, state: FileDocumentState): void {
    this.states.set(key, state);
    this.revision += 1;
    this.syncBeforeUnload();
    for (const listener of this.runtimes.get(key)?.listeners ?? []) listener();
    for (const listener of this.listeners) listener();
  }

  private resetKey(key: string): void {
    const state = this.states.get(key);
    const runtime = this.runtimes.get(key);
    if (!state || !runtime) return;
    runtime?.readController?.abort();
    runtime?.saveController?.abort();
    runtime.readController = null;
    runtime.saveController = null;
    if (runtime.listeners.size > 0) {
      this.replace(key, {
        ...initialState(state.ref),
        requestGeneration: state.requestGeneration + 1,
      });
      return;
    }
    this.forceDelete(key);
  }

  private forceDelete(key: string): void {
    const runtime = this.runtimes.get(key);
    if (!runtime && !this.states.has(key)) return;
    runtime?.readController?.abort();
    runtime?.saveController?.abort();
    this.states.delete(key);
    this.runtimes.delete(key);
    this.revision += 1;
    for (const listener of this.listeners) listener();
  }

  private syncBeforeUnload(): void {
    const hasDirtyDocument = [...this.states.values()].some(
      (state) => state.dirty,
    );
    if (hasDirtyDocument && !this.beforeUnloadInstalled) {
      this.beforeUnloadTarget?.addEventListener(
        "beforeunload",
        this.handleBeforeUnload,
      );
      this.beforeUnloadInstalled = !!this.beforeUnloadTarget;
    } else if (!hasDirtyDocument && this.beforeUnloadInstalled) {
      this.beforeUnloadTarget?.removeEventListener(
        "beforeunload",
        this.handleBeforeUnload,
      );
      this.beforeUnloadInstalled = false;
    }
  }
}
