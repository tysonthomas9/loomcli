import { lstat, realpath } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import { AgentRow, AgentRef, Id, RelativePath, requireFact, type NativeAccess } from './protocol.js';
import { observeFilesystem, FilesystemOutput, type FilesystemInput } from './filesystem.js';
import { observeGit, GitOutput, type GitInput, type GitReader } from './git.js';

const RootSelector = z.discriminatedUnion('kind', [z.object({ kind: z.literal('managed-repo') }).strict(),
  z.object({ kind: z.literal('agent-worktree'), agentId: Id }).strict()]);
const Paths = z.array(RelativePath).min(1).max(1000);
const Bounds = { maxBytes: z.number().int().min(1).max(16 * 1024 * 1024), maxEntries: z.number().int().min(1).max(10000) };
export const ContainerObservationRequest = z.discriminatedUnion('operation', [
  z.object({ operation: z.literal('git-observe'), agentId: Id, view: z.enum(['status', 'refs', 'diff', 'origin']),
    paths: z.array(RelativePath).max(1000), maxBytes: z.number().int().min(1).max(4 * 1024 * 1024) }).strict(),
  z.object({ operation: z.literal('filesystem-root'), root: RootSelector }).strict(),
  z.object({ operation: z.literal('filesystem-observe'), root: RootSelector, relativePaths: Paths,
    view: z.enum(['presence', 'bytes', 'tree-digest']), ...Bounds }).strict(),
]);
export const ContainerRootIdentity = z.object({ path: Id, device: z.number().int().nonnegative(), inode: z.number().int().nonnegative() }).strict();
export const ContainerFilesystemOutput = z.object({ root: ContainerRootIdentity, data: FilesystemOutput }).strict();
export interface ContainerOwnedPaths { workspaceId: string; repo: string; worktreeParent: string; commonDir: string }
/** Runs inside the exact owned container through its fixed source-built reader.
 * Paths are product-resolved from the attested source/agent store, never YAML. */
export async function readContainerObservation(raw: unknown, access: NativeAccess, paths: ContainerOwnedPaths, reader?: GitReader) {
  const request = ContainerObservationRequest.parse(raw);
  const ownedAgent = async (agentId: string) => {
    requireFact(/^agt_[A-Za-z0-9_-]+$/.test(agentId), 'ownership-mismatch', 'Invalid owned agent identifier');
    const row = AgentRow.parse(await access.agent(agentId));
    requireFact(row.agent_id === agentId && row.workspace_id === paths.workspaceId && row.repo === paths.repo &&
      row.worktree_path === path.join(paths.worktreeParent, agentId), 'ownership-mismatch', 'Container agent path is foreign');
    requireFact(await realpath(row.worktree_path) === row.worktree_path && await realpath(paths.repo) === paths.repo,
      'ownership-mismatch', 'Container repository/worktree is not canonical');
    return row;
  };
  if (request.operation === 'git-observe') {
    const row = await ownedAgent(request.agentId);
    return observeGit({ agent: { fixtureLeaseId: 'private-container', workspaceId: paths.workspaceId, agentId: request.agentId },
      view: request.view, paths: request.paths, maxBytes: request.maxBytes },
      { worktree: row.worktree_path, commonDir: paths.commonDir, branch: row.branch }, reader);
  }
  const root = request.root.kind === 'managed-repo' ? paths.repo : (await ownedAgent(request.root.agentId)).worktree_path;
  const canonical = await realpath(root); const before = await lstat(root);
  requireFact(canonical === root && before.isDirectory() && !before.isSymbolicLink(), 'ownership-mismatch', 'Container filesystem root is foreign');
  const identity = { path: root, device: before.dev, inode: before.ino };
  if (request.operation === 'filesystem-root') return identity;
  const data = await observeFilesystem({ leaseId: 'private-container', rootId: request.root.kind,
    relativePaths: request.relativePaths, view: request.view, maxBytes: request.maxBytes, maxEntries: request.maxEntries }, root);
  const after = await lstat(root);
  requireFact(after.dev === before.dev && after.ino === before.ino, 'identity-mismatch', 'Container filesystem root changed during read');
  return { root: identity, data };
}
/** Private read port is bound by the fixture to one attested container ID and
 * generation. This code cannot select a container, command or arbitrary path. */
export type ContainerObservationRead = (request: z.infer<typeof ContainerObservationRequest>, signal: AbortSignal) => Promise<unknown>;
export function containerFilesystemObserver(read: ContainerObservationRead, selector: z.infer<typeof RootSelector>, stamp: z.infer<typeof ContainerRootIdentity>) {
  RootSelector.parse(selector); ContainerRootIdentity.parse(stamp);
  return async (input: z.infer<typeof FilesystemInput>, signal: AbortSignal) => {
    const response = ContainerFilesystemOutput.parse(await read({ operation: 'filesystem-observe', root: selector,
      relativePaths: input.relativePaths, view: input.view, maxBytes: input.maxBytes, maxEntries: input.maxEntries }, signal));
    requireFact(response.root.path === stamp.path && response.root.device === stamp.device && response.root.inode === stamp.inode,
      'identity-mismatch', 'Remote filesystem ownership changed');
    return response.data;
  };
}
export function containerGitObserver(read: ContainerObservationRead, agent: AgentRef, owned: { worktree: string; commonDir: string; branch: string }) {
  return async (input: z.infer<typeof GitInput>, signal: AbortSignal) => {
    requireFact(input.agent.agentId === agent.agentId && input.agent.workspaceId === agent.workspaceId && input.agent.fixtureLeaseId === agent.fixtureLeaseId,
      'ownership-mismatch', 'Remote Git agent is foreign');
    const output = GitOutput.parse(await read({ operation: 'git-observe', agentId: agent.agentId, view: input.view,
      paths: input.paths, maxBytes: input.maxBytes }, signal));
    requireFact(output.worktree === owned.worktree && output.commonDir === owned.commonDir && output.branch === owned.branch,
      'identity-mismatch', 'Remote Git ownership changed');
    return output;
  };
}
