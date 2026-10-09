import * as fs from 'node:fs/promises';
import { constants } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
import type { OwnedRoot } from '../ownership.js';
import { FixtureError } from './lifecycle.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
/** Private physical binding for the fixed owning store, not database contents,
 * process identity, a workspace receipt or a second resource registry. */
export interface NativeStoreBinding {
 readonly root:Readonly<OwnedRoot>;
 readonly storeId:string;
 readonly storeGeneration:string;
 verify(signal:AbortSignal):Promise<void>;
 close():Promise<void>;
}
/** Called by trusted provisioning after agents.db exists. Enrolls cleanup
 * before opening; a retained descriptor prevents inode reuse while leased.
 * Canonical native access owns queries and must verify around each read. */
export async function captureNativeStore(configurationRoot:OwnedRoot,
 enrollCleanup:(cleanup:()=>Promise<void>)=>void,signal:AbortSignal,files:typeof fs=fs):Promise<NativeStoreBinding>{
 signal.throwIfAborted();
 const config=Object.freeze({path:configurationRoot.path,device:configurationRoot.device,inode:configurationRoot.inode});
 check(path.isAbsolute(config.path)&&path.normalize(config.path)===config.path);
 const filename=path.join(config.path,'agents.db');
 let handle:fs.FileHandle|undefined,closed=false;
 const close=async()=>{
  if(closed)return;
  if(handle)await handle.close();
  closed=true;
 };
 enrollCleanup(close);
 const verifyConfig=async()=>{
  const current=await files.lstat(config.path);
  check(current.isDirectory()&&!current.isSymbolicLink()&&current.dev===config.device&&current.ino===config.inode&&
   await files.realpath(config.path)===config.path);
 };
 try{
  await verifyConfig();signal.throwIfAborted();
  const before=await files.lstat(filename);
  check(before.isFile()&&!before.isSymbolicLink()&&await files.realpath(filename)===filename);
  handle=await files.open(filename,constants.O_RDONLY|constants.O_NOFOLLOW);
  const opened=await handle.stat();
  check(opened.isFile()&&opened.dev===before.dev&&opened.ino===before.ino);
  const root=Object.freeze({path:filename,device:opened.dev,inode:opened.ino});
  const verify=async(readSignal:AbortSignal)=>{
   readSignal.throwIfAborted();check(!closed&&handle);
   await verifyConfig();
   const retained=await handle!.stat(),named=await files.lstat(filename);
   check(retained.isFile()&&named.isFile()&&!named.isSymbolicLink()&&
    retained.dev===root.device&&retained.ino===root.inode&&named.dev===root.device&&named.ino===root.inode&&
    await files.realpath(filename)===filename);
   await verifyConfig();readSignal.throwIfAborted();
  };
  await verify(signal);
  const storeId=createHash('sha256').update(JSON.stringify(root)).digest('hex');
  const storeGeneration=createHash('sha256').update(JSON.stringify({config,root})).digest('hex');
  return Object.freeze({root,storeId,storeGeneration,verify,close});
 }catch(error){
  // If closing fails, the pre-enrolled callback retains the same descriptor
  // for retry. No replacement file or path is removed by this binding.
  await close();throw error;
 }
}
