import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";
import {
  createSkill as createSkillRequest,
  deleteSkill as deleteSkillRequest,
  deleteSkillFile as deleteSkillFileRequest,
  getSkillCapabilities,
  getSkillFile,
  listSkills,
  mapSkillApiError,
  patchSkill as patchSkillRequest,
  putSkillFile,
  type FileEntry,
  type FileMutationData,
  type FileReadData,
  type SkillCapabilitiesResponse,
  type SkillCatalogGroup,
  type SkillCatalogSkill,
  type SkillsCatalogResponse,
  type SkillsScopeGroup,
} from "@/api/workspace";
import { explorerRefKey, type SkillsExplorerRef } from "@/utils/explorerRefs";
import { parseSkillPath, skillPathKey, SKILL_MD } from "@/utils/skillsPaths";
import type {
  FileDocumentTransport,
  SkillsDocumentRef,
} from "@/stores/fileDocumentRegistry";

export type SkillsLoadStatus = "idle" | "loading" | "loaded" | "error";

export interface SkillsCatalogSnapshot {
  status: SkillsLoadStatus;
  revision: number;
  groups: SkillCatalogGroup[];
  error: string | null;
  shadowedByRef: Record<string, Set<string>>;
  shadowsByRef: Record<string, Set<string>>;
  readOnlyRefs: Set<string>;
}

export interface SkillCapabilitiesSnapshot {
  status: SkillsLoadStatus;
  data: SkillCapabilitiesResponse | null;
  error: string | null;
}

const EMPTY_CATALOG: SkillsCatalogSnapshot = {
  status: "idle",
  revision: 0,
  groups: [],
  error: null,
  shadowedByRef: {},
  shadowsByRef: {},
  readOnlyRefs: new Set(),
};

const EMPTY_CAPABILITIES: SkillCapabilitiesSnapshot = {
  status: "idle",
  data: null,
  error: null,
};

function groupMatches(
  catalogGroup: SkillCatalogGroup,
  group: SkillsScopeGroup,
): boolean {
  return group.kind === "workspace"
    ? catalogGroup.scope === "workspace"
    : catalogGroup.scope === "role" && catalogGroup.role === group.role;
}

function groupRef(group: SkillsScopeGroup): SkillsExplorerRef {
  return { kind: "skills", group };
}

function shadowMaps(
  groups: SkillCatalogGroup[],
): Pick<SkillsCatalogSnapshot, "shadowedByRef" | "shadowsByRef"> {
  const workspaceNames = new Set(
    groups
      .find((group) => group.scope === "workspace")
      ?.skills.map((skill) => skill.name) ?? [],
  );
  const shadowed = new Set<string>();
  const shadowedByRef: Record<string, Set<string>> = {};
  const shadowsByRef: Record<string, Set<string>> = {};
  for (const group of groups) {
    if (group.scope !== "role" || !group.role) continue;
    const names = new Set(
      group.skills
        .map((skill) => skill.name)
        .filter((name) => workspaceNames.has(name)),
    );
    if (names.size === 0) continue;
    for (const name of names) shadowed.add(name);
    shadowsByRef[explorerRefKey(groupRef({ kind: "role", role: group.role }))] =
      names;
  }
  if (shadowed.size > 0) {
    shadowedByRef[explorerRefKey(groupRef({ kind: "workspace" }))] = shadowed;
  }
  return { shadowedByRef, shadowsByRef };
}

function normalizeCatalog(
  response: SkillsCatalogResponse,
): SkillCatalogGroup[] {
  return response.groups.map((group) => ({
    ...group,
    skills: [...group.skills].sort((a, b) => a.name.localeCompare(b.name)),
  }));
}

function immediateEntries(
  skill: SkillCatalogSkill,
  relativeDir: string,
): FileEntry[] {
  const entries = new Map<string, FileEntry>();
  const add = (name: string, isDir: boolean) => {
    const key = skillPathKey(name);
    const existing = entries.get(key);
    if (existing?.is_dir || (!isDir && existing)) return;
    entries.set(key, {
      name,
      is_dir: isDir,
      size: 0,
      mod_time: skill.updated_at,
    });
  };
  if (!relativeDir) add(SKILL_MD, false);
  const prefix = relativeDir ? `${relativeDir}/` : "";
  for (const file of skill.files) {
    if (!file.path.startsWith(prefix)) continue;
    const remainder = file.path.slice(prefix.length);
    if (!remainder) continue;
    const slash = remainder.indexOf("/");
    add(slash === -1 ? remainder : remainder.slice(0, slash), slash !== -1);
  }
  return [...entries.values()].sort(
    (a, b) =>
      Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name),
  );
}

export function synthesizeSkillDirectory(
  groups: SkillCatalogGroup[],
  group: SkillsScopeGroup,
  path: string,
): FileEntry[] {
  const catalogGroup = groups.find((candidate) =>
    groupMatches(candidate, group),
  );
  if (!catalogGroup) return [];
  const clean = path.replace(/^\/+|\/+$/g, "");
  if (!clean) {
    return catalogGroup.skills.map((skill) => ({
      name: skill.name,
      is_dir: true,
      size: 0,
      mod_time: skill.updated_at,
    }));
  }
  const [skillName, ...rest] = clean.split("/");
  const skill = catalogGroup.skills.find((item) => item.name === skillName);
  return skill ? immediateEntries(skill, rest.join("/")) : [];
}

function skillFromGroups(
  groups: SkillCatalogGroup[],
  group: SkillsScopeGroup,
  name: string,
): SkillCatalogSkill | null {
  return (
    groups
      .find((candidate) => groupMatches(candidate, group))
      ?.skills.find((skill) => skill.name === name) ?? null
  );
}

function presentSkillMutationError(error: unknown): unknown {
  const failure = mapSkillApiError(error);
  if (failure?.code !== "skill_provenance_conflict") return error;
  const owner = failure.owner ? `owner: ${failure.owner}` : null;
  const source = failure.source ? `source: ${failure.source}` : null;
  const context = [owner, source].filter(Boolean).join(", ");
  return new Error(
    context ? `${failure.message} (${context})` : failure.message,
  );
}

// A directory waiter may leave without canceling a read shared by other views.
async function waitForCatalog(
  promise: Promise<void>,
  signal?: AbortSignal,
): Promise<void> {
  if (!signal) return promise;
  signal.throwIfAborted();
  let onAbort = () => {};
  const aborted = new Promise<never>((_, reject) => {
    onAbort = () => reject(signal.reason);
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
  try {
    await Promise.race([promise, aborted]);
  } finally {
    signal.removeEventListener("abort", onAbort);
  }
}

export class SkillsStore {
  private readonly catalogs = new Map<string, SkillsCatalogSnapshot>();
  private readonly catalogReads = new Map<
    string,
    ScopedQueryRequest<SkillCatalogGroup[]>
  >();
  private readonly capabilityReads = new Map<
    string,
    ScopedQueryRequest<SkillCapabilitiesResponse>
  >();
  private readonly catalogPending = new Map<string, Promise<void>>();
  private readonly capabilityPending = new Map<string, Promise<void>>();
  private readonly capabilities = new Map<string, SkillCapabilitiesSnapshot>();
  private readonly listeners = new Set<() => void>();
  private readonly fileMetadata = new Map<string, { executable: boolean }>();
  private readonly writeQueues = new Map<string, Promise<void>>();

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  catalog(workspaceId: string): SkillsCatalogSnapshot {
    return this.catalogs.get(workspaceId) ?? EMPTY_CATALOG;
  }

  capability(workspaceId: string): SkillCapabilitiesSnapshot {
    return this.capabilities.get(workspaceId) ?? EMPTY_CAPABILITIES;
  }

  private catalogReader(
    workspaceId: string,
  ): ScopedQueryRequest<SkillCatalogGroup[]> {
    let reader = this.catalogReads.get(workspaceId);
    if (!reader) {
      reader = new ScopedQueryRequest({
        load: async (signal) =>
          normalizeCatalog(await listSkills(workspaceId, { signal })),
        commit: (groups) => {
          const current = this.catalog(workspaceId);
          this.setCatalog(workspaceId, {
            ...current,
            status: "loaded",
            revision: current.revision + 1,
            groups,
            error: null,
            ...shadowMaps(groups),
          });
        },
        onError: (error) => {
          const current = this.catalog(workspaceId);
          this.setCatalog(workspaceId, {
            ...current,
            status: "error",
            revision: current.revision + 1,
            error: error.message,
          });
        },
        onLoading: (loading) => {
          const current = this.catalog(workspaceId);
          if (loading)
            this.setCatalog(workspaceId, {
              ...current,
              status: "loading",
              error: null,
            });
          else if (current.status === "loading")
            this.setCatalog(workspaceId, { ...current, status: "idle" });
        },
      });
      this.catalogReads.set(workspaceId, reader);
    }
    return reader;
  }

  private capabilityReader(
    workspaceId: string,
  ): ScopedQueryRequest<SkillCapabilitiesResponse> {
    let reader = this.capabilityReads.get(workspaceId);
    if (!reader) {
      reader = new ScopedQueryRequest({
        load: (signal) => getSkillCapabilities(workspaceId, { signal }),
        commit: (data) =>
          this.setCapabilities(workspaceId, {
            status: "loaded",
            data,
            error: null,
          }),
        onError: (error) =>
          this.setCapabilities(workspaceId, {
            status: "error",
            data: null,
            error: error.message,
          }),
        onLoading: (loading) => {
          const current = this.capability(workspaceId);
          if (loading)
            this.setCapabilities(workspaceId, {
              status: "loading",
              data: null,
              error: null,
            });
          else if (current.status === "loading")
            this.setCapabilities(workspaceId, { ...current, status: "idle" });
        },
      });
      this.capabilityReads.set(workspaceId, reader);
    }
    return reader;
  }

  private trackRead(
    pending: Map<string, Promise<void>>,
    workspaceId: string,
    promise: Promise<void>,
  ): Promise<void> {
    pending.set(workspaceId, promise);
    const finish = () => {
      if (pending.get(workspaceId) === promise) pending.delete(workspaceId);
    };
    void promise.then(finish, finish);
    return promise;
  }

  async loadCatalog(workspaceId: string, force = false): Promise<void> {
    if (
      !force &&
      this.catalog(workspaceId).status === "loaded" &&
      !this.catalogPending.has(workspaceId)
    )
      return;
    await this.trackRead(
      this.catalogPending,
      workspaceId,
      this.catalogReader(workspaceId).run({ fresh: force }),
    ).catch(() => {});
  }

  refreshCatalogForRecovery(
    workspaceId: string,
    signal: AbortSignal,
  ): Promise<void> {
    return this.trackRead(
      this.catalogPending,
      workspaceId,
      this.catalogReader(workspaceId).run({ signal, fresh: true }),
    );
  }

  invalidate(workspaceId: string): void {
    this.catalogReads.get(workspaceId)?.cancel();
    const current = this.catalog(workspaceId);
    this.setCatalog(workspaceId, {
      ...current,
      status: "idle",
      revision: current.revision + 1,
      error: null,
    });
  }

  async loadCapabilities(workspaceId: string, force = false): Promise<void> {
    if (
      !force &&
      this.capability(workspaceId).status === "loaded" &&
      !this.capabilityPending.has(workspaceId)
    )
      return;
    await this.trackRead(
      this.capabilityPending,
      workspaceId,
      this.capabilityReader(workspaceId).run({ fresh: force }),
    ).catch(() => {});
  }

  refreshCapabilitiesForRecovery(
    workspaceId: string,
    signal: AbortSignal,
  ): Promise<void> {
    return this.trackRead(
      this.capabilityPending,
      workspaceId,
      this.capabilityReader(workspaceId).run({ signal, fresh: true }),
    );
  }

  canEdit(workspaceId: string, group: SkillsScopeGroup): boolean {
    if (group.kind === "workspace") return false;
    const refKey = explorerRefKey(groupRef(group));
    return (
      this.capability(workspaceId).data?.can_edit_role_scope === true &&
      !this.catalog(workspaceId).readOnlyRefs.has(refKey)
    );
  }

  skills(group: SkillsScopeGroup, workspaceId: string): SkillCatalogSkill[] {
    return (
      this.catalog(workspaceId).groups.find((candidate) =>
        groupMatches(candidate, group),
      )?.skills ?? []
    );
  }

  skill(
    workspaceId: string,
    group: SkillsScopeGroup,
    name: string,
  ): SkillCatalogSkill | null {
    return skillFromGroups(this.catalog(workspaceId).groups, group, name);
  }

  listIndexPaths(workspaceId: string, group: SkillsScopeGroup): string[] {
    return this.skills(group, workspaceId).flatMap((skill) => [
      `${skill.name}/${SKILL_MD}`,
      ...skill.files.map((file) => `${skill.name}/${file.path}`),
    ]);
  }

  loader(workspaceId: string, group: SkillsScopeGroup) {
    return async (path: string, options: { signal?: AbortSignal } = {}) => {
      if (options.signal?.aborted)
        throw new DOMException("Aborted", "AbortError");
      const snapshot = this.catalog(workspaceId);
      if (
        snapshot.status === "idle" ||
        snapshot.status === "loading" ||
        this.catalogPending.has(workspaceId)
      ) {
        const loading = this.trackRead(
          this.catalogPending,
          workspaceId,
          this.catalogReader(workspaceId).run(),
        );
        await waitForCatalog(loading, options.signal);
      }
      if (options.signal?.aborted)
        throw new DOMException("Aborted", "AbortError");
      const loaded = this.catalog(workspaceId);
      if (loaded.status !== "loaded")
        throw new Error(loaded.error ?? "Skills failed to load");
      return synthesizeSkillDirectory(loaded.groups, group, path);
    };
  }

  async createSkill(
    workspaceId: string,
    group: SkillsScopeGroup,
    input: { name: string; description: string; content?: string },
  ): Promise<void> {
    try {
      await createSkillRequest(workspaceId, group, input);
      this.invalidate(workspaceId);
    } catch (error) {
      this.markForbidden(workspaceId, group, error);
      throw presentSkillMutationError(error);
    }
  }

  async updateMetadata(
    workspaceId: string,
    group: SkillsScopeGroup,
    name: string,
    description: string,
  ): Promise<void> {
    const skill = this.skill(workspaceId, group, name);
    if (!skill) throw new Error(`Skill ${name} is not in the catalog`);
    try {
      await patchSkillRequest(
        workspaceId,
        group,
        name,
        { description },
        skill.content_revision,
      );
      this.invalidate(workspaceId);
    } catch (error) {
      this.markForbidden(workspaceId, group, error);
      throw presentSkillMutationError(error);
    }
  }

  async deleteSkill(
    workspaceId: string,
    group: SkillsScopeGroup,
    name: string,
  ): Promise<void> {
    const skill = this.skill(workspaceId, group, name);
    if (!skill) throw new Error(`Skill ${name} is not in the catalog`);
    try {
      await deleteSkillRequest(
        workspaceId,
        group,
        name,
        skill.content_revision,
      );
      this.invalidate(workspaceId);
    } catch (error) {
      this.markForbidden(workspaceId, group, error);
      throw presentSkillMutationError(error);
    }
  }

  async createFile(
    workspaceId: string,
    group: SkillsScopeGroup,
    name: string,
    path: string,
  ): Promise<void> {
    try {
      await putSkillFile(
        workspaceId,
        group,
        name,
        path,
        { content: "", executable: false },
        { createOnly: true },
      );
      this.invalidate(workspaceId);
    } catch (error) {
      this.markForbidden(workspaceId, group, error);
      throw presentSkillMutationError(error);
    }
  }

  async deleteFile(
    workspaceId: string,
    group: SkillsScopeGroup,
    name: string,
    path: string,
    ifMatch?: string,
  ): Promise<void> {
    try {
      const revision =
        ifMatch ??
        (await getSkillFile(workspaceId, group, name, path)).revision;
      await deleteSkillFileRequest(workspaceId, group, name, path, revision);
      this.invalidate(workspaceId);
    } catch (error) {
      this.markForbidden(workspaceId, group, error);
      throw presentSkillMutationError(error);
    }
  }

  documentTransport(): FileDocumentTransport<SkillsDocumentRef> {
    return {
      conditionalSave: true,
      read: async (ref, signal) => {
        const parsed = parseSkillPath(ref.path);
        if (!parsed)
          throw new Error(`Invalid skill document path: ${ref.path}`);
        const data = await getSkillFile(
          ref.workspaceId,
          ref.ref.group,
          parsed.skill,
          parsed.file,
          { signal },
        );
        this.fileMetadata.set(this.documentKey(ref), {
          executable: data.executable,
        });
        return {
          path: ref.path,
          content: data.content,
          size: new TextEncoder().encode(data.content).length,
          binary: false,
          version: data.revision,
        } satisfies FileReadData;
      },
      write: (ref, content, signal, ifMatch) =>
        this.serializedWrite(ref, async () => {
          const parsed = parseSkillPath(ref.path);
          if (!parsed)
            throw new Error(`Invalid skill document path: ${ref.path}`);
          const executable =
            parsed.file === SKILL_MD
              ? false
              : (this.fileMetadata.get(this.documentKey(ref))?.executable ??
                false);
          try {
            const data = await putSkillFile(
              ref.workspaceId,
              ref.ref.group,
              parsed.skill,
              parsed.file,
              { content, executable },
              ifMatch ? { ifMatch } : {},
              { signal },
            );
            this.fileMetadata.set(this.documentKey(ref), {
              executable: data.executable,
            });
            this.invalidate(ref.workspaceId);
            return {
              success: true,
              version: data.revision,
            } satisfies FileMutationData;
          } catch (error) {
            this.markForbidden(ref.workspaceId, ref.ref.group, error);
            throw presentSkillMutationError(error);
          }
        }),
    };
  }

  private documentKey(ref: SkillsDocumentRef): string {
    return `${ref.workspaceId}\u001e${explorerRefKey(ref.ref)}\u001e${ref.path}`;
  }

  private serializedWrite<T>(
    ref: SkillsDocumentRef,
    operation: () => Promise<T>,
  ): Promise<T> {
    const parsed = parseSkillPath(ref.path);
    const key = `${ref.workspaceId}:${explorerRefKey(ref.ref)}:${parsed?.skill ?? ref.path}`;
    const previous = this.writeQueues.get(key) ?? Promise.resolve();
    const current = previous.catch(() => undefined).then(operation);
    this.writeQueues.set(
      key,
      current.then(
        () => undefined,
        () => undefined,
      ),
    );
    return current;
  }

  private markForbidden(
    workspaceId: string,
    group: SkillsScopeGroup,
    error: unknown,
  ): void {
    if (mapSkillApiError(error)?.status !== 403) return;
    const current = this.catalog(workspaceId);
    const readOnlyRefs = new Set(current.readOnlyRefs);
    readOnlyRefs.add(explorerRefKey(groupRef(group)));
    this.setCatalog(workspaceId, { ...current, readOnlyRefs });
  }

  private setCatalog(workspaceId: string, state: SkillsCatalogSnapshot): void {
    this.catalogs.set(workspaceId, state);
    this.emit();
  }

  private setCapabilities(
    workspaceId: string,
    state: SkillCapabilitiesSnapshot,
  ): void {
    this.capabilities.set(workspaceId, state);
    this.emit();
  }

  private emit(): void {
    for (const listener of this.listeners) listener();
  }
}

export const skillsStore = new SkillsStore();
export const skillsFileDocumentTransport = skillsStore.documentTransport();
