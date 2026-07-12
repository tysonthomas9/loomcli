import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import {
  readScopedFile,
  writeScopedFile,
  type FileScopeRef,
} from "@/api/workspace";
import {
  FileDocumentRegistry,
  type FileDocumentRef,
  type FileDocumentState,
} from "@/stores/fileDocumentRegistry";

const sessionFileDocumentRegistry = new FileDocumentRegistry({
  read: ({ workspaceId, path, ...scopeRef }, signal) =>
    readScopedFile(workspaceId, scopeRef, path, undefined, { signal }),
  write: ({ workspaceId, path, ...scopeRef }, content, signal, ifMatch) =>
    writeScopedFile(
      workspaceId,
      scopeRef,
      path,
      content,
      ifMatch ? { ifMatch } : {},
      { signal },
    ),
});

const FileDocumentRegistryContext = createContext<FileDocumentRegistry>(
  sessionFileDocumentRegistry,
);

export function FileDocumentRegistryProvider({
  registry = sessionFileDocumentRegistry,
  children,
}: {
  registry?: FileDocumentRegistry;
  children: ReactNode;
}): JSX.Element {
  return (
    <FileDocumentRegistryContext.Provider value={registry}>
      {children}
    </FileDocumentRegistryContext.Provider>
  );
}

export interface UseFileDocumentReturn extends FileDocumentState {
  refresh: () => Promise<void>;
  edit: (content: string) => void;
  save: () => ReturnType<FileDocumentRegistry["save"]>;
  discard: () => void;
  useExternal: () => void;
  overwriteExternal: () => ReturnType<
    FileDocumentRegistry["overwriteExternal"]
  >;
}

export function useFileDocument(
  workspaceId: string,
  scopeRef: FileScopeRef,
  path: string,
): UseFileDocumentReturn {
  const registry = useContext(FileDocumentRegistryContext);
  const { scope, target, repo } = scopeRef;
  const ref = useMemo<FileDocumentRef>(
    () => ({
      workspaceId,
      scope,
      ...(target ? { target } : {}),
      ...(repo ? { repo } : {}),
      path,
    }),
    [path, repo, scope, target, workspaceId],
  );
  const subscribe = useCallback(
    (listener: () => void) => registry.subscribe(ref, listener),
    [ref, registry],
  );
  const getSnapshot = useCallback(() => registry.get(ref), [ref, registry]);
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  return useMemo(
    () => ({
      ...state,
      refresh: () => registry.refresh(ref),
      edit: (content: string) => registry.edit(ref, content),
      save: () => registry.save(ref),
      discard: () => registry.discard(ref),
      useExternal: () => registry.useExternal(ref),
      overwriteExternal: () => registry.overwriteExternal(ref),
    }),
    [ref, registry, state],
  );
}

export function useFileDocumentRegistry(): FileDocumentRegistry {
  return useContext(FileDocumentRegistryContext);
}

export function useFileDocumentRegistryRevision(): number {
  const registry = useFileDocumentRegistry();
  return useSyncExternalStore(
    (listener) => registry.subscribeAll(listener),
    () => registry.getRevision(),
    () => registry.getRevision(),
  );
}
