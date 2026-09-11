import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import { QueryRecoveryContext } from "./queryRecovery";

import { readScopedFile, writeScopedFile } from "@/api/workspace";
import {
  FileDocumentRegistry,
  routeFileDocumentOperations,
  type CheckoutDocumentRef,
  type FileDocumentRef,
  type FileDocumentState,
  type FileDocumentTransport,
} from "@/stores/fileDocumentRegistry";
import { skillsFileDocumentTransport } from "@/stores/skillsStore";
import {
  checkoutExplorerRef,
  skillsExplorerRef,
  type ExplorerRef,
} from "@/utils/explorerRefs";

export const checkoutFileDocumentTransport: FileDocumentTransport<CheckoutDocumentRef> =
  {
    conditionalSave: false,
    read: ({ workspaceId, path, ref }, signal) =>
      readScopedFile(workspaceId, ref.checkout, path, undefined, { signal }),
    write: ({ workspaceId, path, ref }, content, signal, ifMatch) =>
      writeScopedFile(
        workspaceId,
        ref.checkout,
        path,
        content,
        ifMatch ? { ifMatch } : {},
        { signal },
      ),
  };

const sessionFileDocumentRegistry = new FileDocumentRegistry(
  routeFileDocumentOperations({
    checkout: checkoutFileDocumentTransport,
    skills: skillsFileDocumentTransport,
  }),
);

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
  explorerRef: ExplorerRef,
  path: string,
): UseFileDocumentReturn {
  const registry = useContext(FileDocumentRegistryContext);
  const recovery = useContext(QueryRecoveryContext);
  const kind = explorerRef.kind;
  const checkout = kind === "checkout" ? explorerRef.checkout : null;
  const group = kind === "skills" ? explorerRef.group : null;
  const groupKind = group?.kind;
  const role = group?.kind === "role" ? group.role : null;
  const stableExplorerRef = useMemo<ExplorerRef>(
    () =>
      kind === "checkout"
        ? checkoutExplorerRef({
            scope: checkout?.scope ?? "workspace",
            ...(checkout?.target ? { target: checkout.target } : {}),
            ...(checkout?.repo ? { repo: checkout.repo } : {}),
          })
        : skillsExplorerRef(
            groupKind === "role"
              ? { kind: "role", role: role ?? "" }
              : { kind: "workspace" },
          ),
    [checkout?.repo, checkout?.scope, checkout?.target, groupKind, kind, role],
  );
  const ref = useMemo<FileDocumentRef>(
    () => ({
      workspaceId,
      ref: stableExplorerRef,
      path,
    }),
    [path, stableExplorerRef, workspaceId],
  );
  const subscribe = useCallback(
    (listener: () => void) => registry.subscribe(ref, listener),
    [ref, registry],
  );
  const getSnapshot = useCallback(() => registry.get(ref), [ref, registry]);
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  useEffect(() => {
    if (!workspaceId || !path || !recovery) return;
    return registry.enrollRecovery(ref, recovery);
  }, [registry, ref, workspaceId, path, recovery]);

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
