import * as fs from 'node:fs/promises';
import path from 'node:path';
import type { FixtureAuthorityOwner } from '../authority.js';
import { fixtureOwnerIdentity } from '../authority.js';
import { createNativeHostAccess, type NativeHostOptions } from '../native-host.js';
import type { OwnedRoot } from '../ownership.js';
import { FixtureError } from './lifecycle.js';
import { NativeWorkspaceRecords } from './native-records.js';
import { captureNativeStore } from './native-store.js';
import type { WorkspaceRecordPorts } from './workspace-records.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
/** Trusted provisioning coordinates, never capability input. The enclosing
 * configuration root and executable come from the owning build/runtime. */
export interface CapturedNativeCoordinates {
 owner:FixtureAuthorityOwner;
 configurationRoot:OwnedRoot;
 pinnedExecutable:string;
 enrollCleanup(cleanup:()=>Promise<void>):void;
 processIdentity?:NativeHostOptions['processIdentity'];
 fetch?:NativeHostOptions['fetch'];
}
/** Composes the real descriptor capture and fixed read-only SQLite transport.
 * Workspace topology must first enter through successful creation receipts;
 * neither a listing nor the first database row can grant ownership. */
export async function captureNativeWorkspaceBinding(coordinates:CapturedNativeCoordinates,
 ports:Omit<WorkspaceRecordPorts,'store'>,signal:AbortSignal,files:typeof fs=fs){
 signal.throwIfAborted();
 const owner=Object.freeze(fixtureOwnerIdentity(coordinates.owner));
 const configurationRoot=Object.freeze({...coordinates.configurationRoot});
 const pinnedExecutable=coordinates.pinnedExecutable;
 check(path.isAbsolute(pinnedExecutable)&&path.normalize(pinnedExecutable)===pinnedExecutable);
 const processIdentity=coordinates.processIdentity,fetch=coordinates.fetch;
 const read=ports.read.bind(ports),commonDir=ports.commonDir.bind(ports);
 const store=await captureNativeStore(configurationRoot,cleanup=>coordinates.enrollCleanup(cleanup),signal,files);
 const nativeAccess=async(workspaceId:string,readSignal:AbortSignal)=>{
  await store.verify(readSignal);
  const workspace=await records.workspace(workspaceId,readSignal);
  check(workspace.store.storeId===store.storeId&&workspace.store.storeGeneration===store.storeGeneration);
  const native=createNativeHostAccess({configRoot:configurationRoot.path,workspaceId,repo:workspace.repo,
   ownedRepositories:workspace.repositories,capturedStore:store,pinnedExecutable,processIdentity,fetch,signal:readSignal});
  await store.verify(readSignal);return native;
 };
 const records=new NativeWorkspaceRecords({read,commonDir,store:async readSignal=>{
  await store.verify(readSignal);return {storeId:store.storeId,storeGeneration:store.storeGeneration};
 }},async(workspaceId,agentId,readSignal)=>(await nativeAccess(workspaceId,readSignal)).rawAgent(agentId,readSignal));
 return Object.freeze({records,store,nativeAccess,
  readWorkspaceAgent:(workspaceId:string,agentId:string,readSignal:AbortSignal)=>records.nativeAgent(owner,workspaceId,agentId,readSignal)});
}
