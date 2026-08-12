export type AgentLifecycleAction = "start" | "stop" | "restart";

export interface PendingAgentLifecycleCommand {
  workspace: string;
  agent: string;
  action: AgentLifecycleAction;
  commandId: string;
  acceptedAt: number;
  warningShown: boolean;
}

const pendingAgentLifecycleStoragePrefix = "loom.agent-lifecycle.pending.v1";
const agentLifecycleSubmissionStoragePrefix =
  "loom.agent-lifecycle.submitting.v1";
const agentLifecycleSubmissionLeaseMs = 60_000;

interface AgentLifecycleSubmissionLease {
  token: string;
  expiresAt: number;
}

type AgentLifecyclePendingListener = () => void;

const pendingListeners = new Map<string, Set<AgentLifecyclePendingListener>>();
const inProcessSubmissionLeases = new Map<string, string>();

export function pendingAgentLifecycleStorageKey(
  workspace: string,
  agent: string,
): string {
  return `${pendingAgentLifecycleStoragePrefix}:${encodeURIComponent(
    workspace,
  )}:${encodeURIComponent(agent)}`;
}

export function pendingAgentLifecycleCommandStorageKey(
  workspace: string,
  agent: string,
  commandId: string,
): string {
  return `${pendingAgentLifecycleStorageKey(
    workspace,
    agent,
  )}:${encodeURIComponent(commandId)}`;
}

function agentLifecycleSubmissionStorageKey(
  workspace: string,
  agent: string,
): string {
  return `${agentLifecycleSubmissionStoragePrefix}:${encodeURIComponent(
    workspace,
  )}:${encodeURIComponent(agent)}`;
}

function browserStorage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage;
  } catch {
    return null;
  }
}

function isLifecycleAction(value: unknown): value is AgentLifecycleAction {
  return value === "start" || value === "stop" || value === "restart";
}

export function parsePendingAgentLifecycleCommand(
  value: unknown,
  workspace: string,
  agent: string,
): PendingAgentLifecycleCommand | null {
  if (value == null || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  const candidate = value as Partial<PendingAgentLifecycleCommand>;
  if (
    candidate.workspace !== workspace ||
    candidate.agent !== agent ||
    !isLifecycleAction(candidate.action) ||
    typeof candidate.commandId !== "string" ||
    candidate.commandId.trim() === "" ||
    typeof candidate.acceptedAt !== "number" ||
    !Number.isFinite(candidate.acceptedAt) ||
    candidate.acceptedAt <= 0 ||
    typeof candidate.warningShown !== "boolean"
  ) {
    return null;
  }
  return {
    workspace,
    agent,
    action: candidate.action,
    commandId: candidate.commandId,
    acceptedAt: candidate.acceptedAt,
    warningShown: candidate.warningShown,
  };
}

function parsePendingAgentLifecycleStorageValue(
  raw: string | null,
  workspace: string,
  agent: string,
): PendingAgentLifecycleCommand | null {
  if (raw == null) return null;
  try {
    return parsePendingAgentLifecycleCommand(
      JSON.parse(raw) as unknown,
      workspace,
      agent,
    );
  } catch {
    return null;
  }
}

function listenerIdentity(workspace: string, agent: string): string {
  return `${workspace}\x00${agent}`;
}

function notifyPendingListeners(workspace: string, agent: string): void {
  const listeners = pendingListeners.get(listenerIdentity(workspace, agent));
  if (listeners == null) return;
  for (const listener of [...listeners]) listener();
}

function comparePendingCommands(
  left: PendingAgentLifecycleCommand,
  right: PendingAgentLifecycleCommand,
): number {
  if (left.acceptedAt !== right.acceptedAt) {
    return left.acceptedAt - right.acceptedAt;
  }
  return left.commandId.localeCompare(right.commandId);
}

function newestPendingCommand(
  current: PendingAgentLifecycleCommand | null,
  candidate: PendingAgentLifecycleCommand | null,
): PendingAgentLifecycleCommand | null {
  if (candidate == null) return current;
  if (current == null || comparePendingCommands(candidate, current) > 0) {
    return candidate;
  }
  return current;
}

function parseSubmissionLease(
  raw: string | null,
): AgentLifecycleSubmissionLease | null {
  if (raw == null) return null;
  try {
    const value = JSON.parse(raw) as unknown;
    if (value == null || typeof value !== "object" || Array.isArray(value)) {
      return null;
    }
    const candidate = value as Partial<AgentLifecycleSubmissionLease>;
    if (
      typeof candidate.token !== "string" ||
      candidate.token.trim() === "" ||
      typeof candidate.expiresAt !== "number" ||
      !Number.isFinite(candidate.expiresAt) ||
      candidate.expiresAt <= 0
    ) {
      return null;
    }
    return {
      token: candidate.token,
      expiresAt: candidate.expiresAt,
    };
  } catch {
    return null;
  }
}

function randomSubmissionToken(): string {
  try {
    return crypto.randomUUID();
  } catch {
    return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }
}

export function loadPendingAgentLifecycleCommand(
  workspace: string,
  agent: string,
  storage: Storage | null = browserStorage(),
): PendingAgentLifecycleCommand | null {
  if (storage == null || workspace.trim() === "" || agent.trim() === "") {
    return null;
  }
  const legacyKey = pendingAgentLifecycleStorageKey(workspace, agent);
  const commandKeyPrefix = `${legacyKey}:`;
  try {
    let newest = parsePendingAgentLifecycleStorageValue(
      storage.getItem(legacyKey),
      workspace,
      agent,
    );
    if (storage.getItem(legacyKey) != null && newest == null) {
      storage.removeItem(legacyKey);
    }
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key == null || !key.startsWith(commandKeyPrefix)) continue;
      const candidate = parsePendingAgentLifecycleStorageValue(
        storage.getItem(key),
        workspace,
        agent,
      );
      if (
        candidate == null ||
        key !==
          pendingAgentLifecycleCommandStorageKey(
            workspace,
            agent,
            candidate.commandId,
          )
      ) {
        storage.removeItem(key);
        index -= 1;
        continue;
      }
      newest = newestPendingCommand(newest, candidate);
    }
    return newest;
  } catch {
    try {
      storage.removeItem(legacyKey);
    } catch {
      // Storage can be unavailable in hardened/private browser contexts.
    }
    return null;
  }
}

export function savePendingAgentLifecycleCommand(
  pending: PendingAgentLifecycleCommand,
  storage: Storage | null = browserStorage(),
): boolean {
  if (
    storage == null ||
    parsePendingAgentLifecycleCommand(
      pending,
      pending.workspace,
      pending.agent,
    ) == null
  ) {
    return false;
  }
  try {
    const key = pendingAgentLifecycleCommandStorageKey(
      pending.workspace,
      pending.agent,
      pending.commandId,
    );
    const current = parsePendingAgentLifecycleStorageValue(
      storage.getItem(key),
      pending.workspace,
      pending.agent,
    );
    const next =
      current?.commandId === pending.commandId
        ? {
            ...pending,
            warningShown: current.warningShown || pending.warningShown,
          }
        : pending;
    storage.setItem(key, JSON.stringify(next));
    const legacyKey = pendingAgentLifecycleStorageKey(
      pending.workspace,
      pending.agent,
    );
    const legacy = parsePendingAgentLifecycleStorageValue(
      storage.getItem(legacyKey),
      pending.workspace,
      pending.agent,
    );
    if (legacy?.commandId === pending.commandId) storage.removeItem(legacyKey);
    notifyPendingListeners(next.workspace, next.agent);
    return true;
  } catch {
    return false;
  }
}

/**
 * Mark the delayed warning only while this command is still authoritative.
 * A stale timer must not put an older command back over a newer response.
 */
export function markPendingAgentLifecycleWarningShown(
  workspace: string,
  agent: string,
  commandId: string,
  storage: Storage | null = browserStorage(),
): PendingAgentLifecycleCommand | null {
  if (storage == null) return null;
  try {
    const current = loadPendingAgentLifecycleCommand(workspace, agent, storage);
    if (current?.commandId !== commandId) return null;
    if (current.warningShown) return current;
    const warned = { ...current, warningShown: true };
    storage.setItem(
      pendingAgentLifecycleCommandStorageKey(workspace, agent, commandId),
      JSON.stringify(warned),
    );
    const legacyKey = pendingAgentLifecycleStorageKey(workspace, agent);
    const legacy = parsePendingAgentLifecycleStorageValue(
      storage.getItem(legacyKey),
      workspace,
      agent,
    );
    if (legacy?.commandId === commandId) storage.removeItem(legacyKey);
    notifyPendingListeners(workspace, agent);
    return loadPendingAgentLifecycleCommand(workspace, agent, storage)
      ?.commandId === commandId
      ? warned
      : null;
  } catch {
    return null;
  }
}

/**
 * Remove only this command's independent record. Per-command keys make this
 * structurally safe: a stale terminal C1 cannot delete a concurrently accepted
 * C2, even though localStorage has no compare-and-delete primitive.
 */
export function clearPendingAgentLifecycleCommand(
  workspace: string,
  agent: string,
  commandId: string,
  storage: Storage | null = browserStorage(),
): boolean {
  if (storage == null) return false;
  try {
    const wasCurrent =
      loadPendingAgentLifecycleCommand(workspace, agent, storage)?.commandId ===
      commandId;
    const commandKey = pendingAgentLifecycleCommandStorageKey(
      workspace,
      agent,
      commandId,
    );
    const command = parsePendingAgentLifecycleStorageValue(
      storage.getItem(commandKey),
      workspace,
      agent,
    );
    if (command?.commandId === commandId) storage.removeItem(commandKey);
    const legacyKey = pendingAgentLifecycleStorageKey(workspace, agent);
    const legacy = parsePendingAgentLifecycleStorageValue(
      storage.getItem(legacyKey),
      workspace,
      agent,
    );
    if (legacy?.commandId === commandId) storage.removeItem(legacyKey);
    notifyPendingListeners(workspace, agent);
    return (
      wasCurrent &&
      loadPendingAgentLifecycleCommand(workspace, agent, storage)?.commandId !==
        commandId
    );
  } catch {
    // A failed cleanup must not break terminal lifecycle rendering.
    return false;
  }
}

/**
 * Notify both duplicate mounts in this document and other tabs for one exact
 * workspace-agent identity. Native storage events do not fire in the document
 * that performed the write, hence the small in-process listener set.
 */
export function subscribeAgentLifecyclePending(
  workspace: string,
  agent: string,
  listener: AgentLifecyclePendingListener,
): () => void {
  if (workspace.trim() === "" || agent.trim() === "") return () => undefined;
  const identity = listenerIdentity(workspace, agent);
  let listeners = pendingListeners.get(identity);
  if (listeners == null) {
    listeners = new Set();
    pendingListeners.set(identity, listeners);
  }
  listeners.add(listener);

  const pendingKey = pendingAgentLifecycleStorageKey(workspace, agent);
  const pendingCommandKeyPrefix = `${pendingKey}:`;
  const submissionKey = agentLifecycleSubmissionStorageKey(workspace, agent);
  const handleStorage = (event: StorageEvent) => {
    if (
      event.storageArea !== window.localStorage ||
      (event.key !== pendingKey &&
        !event.key?.startsWith(pendingCommandKeyPrefix) &&
        event.key !== submissionKey)
    ) {
      return;
    }
    if (
      event.key === pendingKey &&
      event.newValue != null &&
      parsePendingAgentLifecycleStorageValue(
        event.newValue,
        workspace,
        agent,
      ) == null
    ) {
      return;
    }
    listener();
  };
  window.addEventListener("storage", handleStorage);

  return () => {
    window.removeEventListener("storage", handleStorage);
    const current = pendingListeners.get(identity);
    current?.delete(listener);
    if (current?.size === 0) pendingListeners.delete(identity);
  };
}

export function isAgentLifecycleSubmissionLocked(
  workspace: string,
  agent: string,
  storage: Storage | null = browserStorage(),
  now = Date.now(),
): boolean {
  const identity = listenerIdentity(workspace, agent);
  if (inProcessSubmissionLeases.has(identity)) return true;
  if (storage == null) return false;
  try {
    const key = agentLifecycleSubmissionStorageKey(workspace, agent);
    const lease = parseSubmissionLease(storage.getItem(key));
    if (lease == null || lease.expiresAt <= now) {
      if (lease != null) storage.removeItem(key);
      return false;
    }
    return true;
  } catch {
    return false;
  }
}

/**
 * Best-effort cross-view submission lease. The synchronous in-process lease is
 * exact for duplicate React mounts; localStorage extends the guard across tabs
 * with write-then-read ownership verification (the strongest available
 * compare-before-write pattern without a browser CAS primitive).
 */
export function acquireAgentLifecycleSubmission(
  workspace: string,
  agent: string,
  storage: Storage | null = browserStorage(),
  now = Date.now(),
): string | null {
  if (
    workspace.trim() === "" ||
    agent.trim() === "" ||
    loadPendingAgentLifecycleCommand(workspace, agent, storage) != null ||
    isAgentLifecycleSubmissionLocked(workspace, agent, storage, now)
  ) {
    return null;
  }

  const identity = listenerIdentity(workspace, agent);
  const token = randomSubmissionToken();
  inProcessSubmissionLeases.set(identity, token);
  if (storage != null) {
    try {
      const key = agentLifecycleSubmissionStorageKey(workspace, agent);
      storage.setItem(
        key,
        JSON.stringify({
          token,
          expiresAt: now + agentLifecycleSubmissionLeaseMs,
        } satisfies AgentLifecycleSubmissionLease),
      );
      if (parseSubmissionLease(storage.getItem(key))?.token !== token) {
        inProcessSubmissionLeases.delete(identity);
        return null;
      }
    } catch {
      // The in-process lease still protects duplicate mounts in this document.
    }
  }
  notifyPendingListeners(workspace, agent);
  return token;
}

export function releaseAgentLifecycleSubmission(
  workspace: string,
  agent: string,
  token: string,
  storage: Storage | null = browserStorage(),
): void {
  const identity = listenerIdentity(workspace, agent);
  if (inProcessSubmissionLeases.get(identity) === token) {
    inProcessSubmissionLeases.delete(identity);
  }
  if (storage != null) {
    try {
      const key = agentLifecycleSubmissionStorageKey(workspace, agent);
      if (parseSubmissionLease(storage.getItem(key))?.token === token) {
        storage.removeItem(key);
      }
    } catch {
      // An inaccessible storage lease expires independently.
    }
  }
  notifyPendingListeners(workspace, agent);
}
