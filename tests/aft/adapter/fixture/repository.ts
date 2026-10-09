import * as fs from 'node:fs/promises';
import {constants} from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { FixtureError } from './lifecycle.js';
import type { HostProcesses } from './process.js';
import type { RegisteredBuild } from './production.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const hash=(bytes:Uint8Array)=>createHash('sha256').update(bytes).digest('hex');
export type RepositoryFixture='empty-legacy'|'slack-clone';
/** Launcher-only coordinates. Operation input cannot select a path, binary,
 * command or source module. The enclosing runtime root is already enrolled. */
export interface RepositoryCoordinates {
 runtimeRoot:string;destination:string;source:RegisteredBuild;gitBinary:string;toolPath:string;
}
/** Preserves scripts/seed-slack-clone.sh and zz-agent-flow setup Git semantics.
 * This creates source files, not a product workspace or an actor registration. */
export async function seedOwnedRepository(kind:RepositoryFixture,coordinates:RepositoryCoordinates,
 processes:Pick<HostProcesses,'run'>,signal:AbortSignal,files:typeof fs=fs){
 signal.throwIfAborted();check(kind==='empty-legacy'||kind==='slack-clone');
 const {runtimeRoot,destination,source,gitBinary,toolPath}=coordinates;
 check(path.isAbsolute(runtimeRoot)&&path.isAbsolute(destination)&&path.isAbsolute(gitBinary)&&
  destination.startsWith(runtimeRoot+path.sep)&&await files.realpath(runtimeRoot)===runtimeRoot);
 const parent=path.dirname(destination),before=await files.lstat(parent);
 check(before.isDirectory()&&!before.isSymbolicLink()&&await files.realpath(parent)===parent);
 let copied:{relative:string;bytes:Buffer;mode:number}[]=[];
 if(kind==='slack-clone'){
  const prefix='tests/fixtures/slack-clone/',root=path.join(source.source.root,'tests/fixtures/slack-clone');
  check(await files.realpath(root)===root);const rootStat=await files.lstat(root);check(rootStat.isDirectory()&&!rootStat.isSymbolicLink());
  const expected=new Map(source.source.entries.filter(entry=>entry.relativePath.startsWith(prefix)).map(entry=>[entry.relativePath.slice(prefix.length),entry.sha256]));
  check(expected.size>0&&expected.size<=1000);
  let total=0,count=0;
  const walk=async(directory:string)=>{
   const entries=await files.readdir(directory,{withFileTypes:true});
   for(const entry of entries){signal.throwIfAborted();check(++count<=2048);const filename=path.join(directory,entry.name),stat=await files.lstat(filename);
    check(!stat.isSymbolicLink()&&await files.realpath(filename)===filename);
    if(stat.isDirectory())await walk(filename);
    else {check(stat.isFile());const relative=path.relative(root,filename).split(path.sep).join('/'),digest=expected.get(relative);
     check(digest&&stat.size<=4*1024*1024);
     const handle=await files.open(filename,constants.O_RDONLY|constants.O_NOFOLLOW);let bytes:Buffer;
     try{const opened=await handle.stat();check(opened.dev===stat.dev&&opened.ino===stat.ino&&opened.isFile());bytes=await handle.readFile();
      const after=await files.lstat(filename);check(!after.isSymbolicLink()&&after.dev===stat.dev&&after.ino===stat.ino&&after.size===bytes.length);
     }finally{await handle.close();}
     total+=bytes.length;
     check(total<=4*1024*1024&&hash(bytes)===digest);expected.delete(relative);copied.push({relative,bytes,mode:stat.mode&0o777});}
   }
  };
  await walk(root);check(expected.size===0);
  const after=await files.lstat(root);check(rootStat.dev===after.dev&&rootStat.ino===after.ino&&!after.isSymbolicLink());
  copied=copied.filter(entry=>entry.relative!=='data.json');
 }
 signal.throwIfAborted();const afterParent=await files.lstat(parent);
 check(before.dev===afterParent.dev&&before.ino===afterParent.ino&&!afterParent.isSymbolicLink());
 // Exclusive mkdir prevents reuse or removal of any pre-existing resource.
 await files.mkdir(destination,{mode:0o700});
 const stamp=await files.lstat(destination);check(stamp.isDirectory()&&!stamp.isSymbolicLink());
 for(const entry of copied){const filename=path.join(destination,entry.relative);await files.mkdir(path.dirname(filename),{recursive:true,mode:0o700});
  await files.writeFile(filename,entry.bytes,{flag:'wx',mode:entry.mode});}
 const run=async(argv:string[])=>{
  signal.throwIfAborted();const current=await files.lstat(destination);
  check(current.dev===stamp.dev&&current.ino===stamp.ino&&!current.isSymbolicLink()&&await files.realpath(destination)===destination);
  await processes.run({executable:gitBinary,argv,cwd:destination,env:{PATH:toolPath,HOME:path.join(runtimeRoot,'home'),GIT_CONFIG_NOSYSTEM:'1'}},signal);
  const after=await files.lstat(destination);check(after.dev===stamp.dev&&after.ino===stamp.ino&&!after.isSymbolicLink());
 };
 await run(kind==='slack-clone'?['init','-q','-b','main']:['init','-q']);
 if(kind==='slack-clone')await run(['add','-A']);
 await run(kind==='slack-clone'?['-c','user.name=Loom Fixture','-c','user.email=fixture@loom.invalid','commit','-q','-m','Initial Slack clone']:
  ['-c','user.email=e2e@x','-c','user.name=e2e','commit','--allow-empty','-m','init','-q']);
 return {path:destination,device:stamp.dev,inode:stamp.ino};
}
