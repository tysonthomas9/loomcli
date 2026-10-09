import { constants } from 'node:fs';
import { lstat, open, realpath } from 'node:fs/promises';
import path from 'node:path';
import { getRegisteredResource, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ArtifactRefSchema } from '@tysonthomas9/aft/types';
import { requireFact, sha256 } from './protocol.js';
import type { z } from 'zod';

export interface EvidenceStore {
  retain(serialized: string): Promise<z.infer<typeof ArtifactRefSchema>>;
  resolve(id: string): Promise<string>;
}
export const evidenceKey = '@loom/aft-adapter/evidence/v1';
const key = evidenceKey;
export function putEvidenceStore(context: CapabilityContext, store: EvidenceStore): void {
  requireFact(!context.resources.has(key), 'ownership-mismatch', 'Evidence store is already registered');
  context.resources.set(key, store);
}
export function bindEvidenceStore(context: CapabilityContext, leaseId: string): void {
  const store = context.resources.get(key) as EvidenceStore | undefined;
  requireFact(store, 'observation-failed', 'No owned evidence store is registered');
  context.resources.set(`${key}:${leaseId}`, store);
}
export function getFixtureEvidenceStore(context:CapabilityContext,leaseId:string):EvidenceStore {
  const resourceKey=`${evidenceKey}:${leaseId}`;
  let store:EvidenceStore|undefined;
  if(context.resources.has(resourceKey))store=getRegisteredResource(context,resourceKey,leaseId) as EvidenceStore;
  else if(context.scope==='case'&&context.suite?.id===context.suiteId&&context.suite.handles.includes(leaseId))
    store=context.suite.getResource(resourceKey,leaseId) as EvidenceStore|undefined;
  requireFact(store,'ownership-mismatch','Fixture evidence authority is missing or foreign');return store;
}
export async function retainEvidence(context: CapabilityContext, serialized: string, leaseId?: string) {
  let store = context.resources.get(key) as EvidenceStore | undefined;
  if (!store && leaseId && context.scope === 'case' && context.suite?.id === context.suiteId && context.suite.handles.includes(leaseId))
    store = context.suite.getResource(`${key}:${leaseId}`, leaseId) as EvidenceStore | undefined;
  requireFact(store, 'observation-failed', 'No owned evidence store is registered');
  return ArtifactRefSchema.parse(await store.retain(serialized));
}
/** Directory is created/owned by the fixture or launcher. It survives resource
 * teardown so report references resolve after process and native cleanup. */
export async function createEvidenceStore(directory: string): Promise<EvidenceStore> {
  const root = await realpath(directory); const identity = await lstat(root);
  requireFact(root === directory && identity.isDirectory() && !identity.isSymbolicLink(), 'ownership-mismatch', 'Evidence root is not canonical');
  const receipts = new Map<string, z.infer<typeof ArtifactRefSchema>>();
  const verify = async () => {
    const current = await lstat(root);
    requireFact(current.ino === identity.ino && current.dev === identity.dev && !current.isSymbolicLink(), 'ownership-mismatch', 'Evidence root changed');
  };
  return {
    async retain(serialized) {
      await verify();
      const digest = await sha256(serialized); const id = `loom-adapter-${digest}.json`;
      const file = path.join(root, id);
      if (!receipts.has(id)) {
        const handle = await open(file, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
        try { await handle.writeFile(serialized, 'utf8'); await handle.sync(); } finally { await handle.close(); }
        await verify();
        receipts.set(id, { id, sha256: digest, bytes: Buffer.byteLength(serialized), mediaType: 'application/json', redaction: 'sanitized' });
      }
      const receipt = receipts.get(id)!;
      const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
      try {
        const stat = await handle.stat();
        requireFact(stat.isFile() && stat.size === receipt.bytes, 'identity-mismatch', 'Evidence file changed');
        requireFact(await sha256(await handle.readFile()) === receipt.sha256, 'identity-mismatch', 'Evidence bytes changed');
      } finally { await handle.close(); }
      return { ...receipt };
    },
    async resolve(id) {
      await verify();
      const receipt = receipts.get(id);
      requireFact(receipt, 'ownership-mismatch', 'Evidence reference is unknown');
      const file = path.join(root, id);
      const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
      let bytes: Buffer;
      try {
        const stat = await handle.stat();
        requireFact(stat.isFile() && stat.size === receipt.bytes, 'identity-mismatch', 'Evidence file changed');
        bytes = await handle.readFile();
      } finally { await handle.close(); }
      requireFact(bytes.byteLength === receipt.bytes && await sha256(bytes) === receipt.sha256, 'identity-mismatch', 'Evidence bytes changed');
      return file;
    },
  };
}
