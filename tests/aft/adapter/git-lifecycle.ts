import { lstat, realpath } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import { AgentRef, Id, requireFact } from './protocol.js';
import { localGit, type GitReader } from './git.js';

export const GitLifecycleInput = z.object({agent:AgentRef,maxBytes:z.number().int().min(1).max(4*1024*1024)}).strict();
export const GitLifecycleOutput = z.object({agentId:Id,sourceRoot:Id,commonDir:Id,branch:Id,
  branchRef:z.object({ref:Id,oid:z.string().regex(/^[a-f0-9]{40,64}$/)}).strict().nullable(),
  worktree:Id,worktreePresent:z.boolean()}).strict();
/** Source-repository reads remain valid after the exact owned checkout is gone.
 * Missing checkout/ref are source facts; no deletion outcome is asserted here. */
export async function observeGitLifecycle(input:z.infer<typeof GitLifecycleInput>,
  owned:{sourceRoot:string;commonDir:string;branch:string;worktree:string},reader:GitReader=localGit) {
  const parent = path.dirname(owned.worktree);
  const attest = async()=>{
    requireFact(await realpath(owned.sourceRoot)===owned.sourceRoot && await realpath(owned.commonDir)===owned.commonDir &&
      await realpath(parent)===parent,'ownership-mismatch','Lifecycle Git roots are not canonical');
    const stamps = await Promise.all([lstat(owned.sourceRoot),lstat(owned.commonDir),lstat(parent)]);
    requireFact(stamps.every(stamp=>stamp.isDirectory()&&!stamp.isSymbolicLink()),'ownership-mismatch','Lifecycle Git root is not a directory');
    return stamps;
  };
  const before = await attest();
  const read = async(args:string[],missing=false)=>{
    const result = await reader.read(args,owned.sourceRoot,input.maxBytes);
    requireFact(result.code===0 || (missing&&result.code===1&&!result.stdout),'observation-failed','Lifecycle Git read is unavailable');
    requireFact(Buffer.byteLength(result.stdout)<=input.maxBytes,'incomplete-pages','Lifecycle Git read exceeds bound');
    return {missing:result.code===1,text:result.stdout.trim()};
  };
  const common = (await read(['rev-parse','--path-format=absolute','--git-common-dir'])).text;
  const source = (await read(['rev-parse','--show-toplevel'])).text;
  requireFact(common===owned.commonDir && source===owned.sourceRoot,'identity-mismatch','Lifecycle Git repository differs from owned source');
  const ref = `refs/heads/${owned.branch}`;
  await read(['check-ref-format',ref]);
  const readBranch = async()=>{
    const exists=await read(['show-ref','--verify','--quiet',ref],true);
    return exists.missing ? exists : read(['show-ref','--verify',ref]);
  };
  const branch = await readBranch();
  let branchRef:z.infer<typeof GitLifecycleOutput>['branchRef']=null;
  if (!branch.missing) {
    const [oid,name,...extra]=branch.text.split(/\s+/);
    requireFact(name===ref && oid && /^[a-f0-9]{40,64}$/.test(oid) && !extra.length,'identity-mismatch','Lifecycle branch ref differs from selector');
    branchRef={ref,oid};
  }
  const checkout = async()=>{
    try {
      const stat=await lstat(owned.worktree);
      requireFact(stat.isDirectory()&&!stat.isSymbolicLink()&&await realpath(owned.worktree)===owned.worktree,
        'identity-mismatch','Owned lifecycle checkout was replaced'); return stat;
    } catch(error) { if ((error as NodeJS.ErrnoException).code!=='ENOENT') throw error; return null; }
  };
  const worktree = await checkout();
  const after = await attest();
  requireFact(after.every((stamp,index)=>stamp.dev===before[index]!.dev&&stamp.ino===before[index]!.ino),
    'identity-mismatch','Lifecycle Git source or checkout parent changed');
  const afterRef=await readBranch();
  requireFact(afterRef.missing===branch.missing&&afterRef.text===branch.text,'identity-mismatch','Lifecycle branch ref changed during read');
  const afterCheckout=await checkout();
  requireFact(worktree===null ? afterCheckout===null : afterCheckout!==null&&afterCheckout.dev===worktree.dev&&afterCheckout.ino===worktree.ino,
    'identity-mismatch','Lifecycle checkout changed during read');
  return GitLifecycleOutput.parse({agentId:input.agent.agentId,...owned,branchRef,worktreePresent:worktree!==null});
}
