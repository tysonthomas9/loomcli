import { randomUUID, createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import { FixtureError, type Artifact, type FixtureDriver, type FixturePlan, type Inventory, type Resource } from './lifecycle.js';
import { ComposeFixtureDriver, type ProductionConfig } from './production.js';
import { LaunchNotStarted, nodeProcesses, reservePort, type PortReservation, type HostProcesses, type OwnedProcess, type HostCommand, type CliCompletion } from './process.js';
import { initializeCodex, type CodexProtocolProbe } from './codex-probe.js';
import { prepareRenderer, type PreparedRenderer } from './renderer.js';
import { fixtureRouting, fixtureOperationAuthority } from './routing.js';
import type { FixtureAuthorityOwner } from '../authority.js';
import {OwnedDescendants,createRegisteredProcessPort,readRegisteredHostServices,type RegisteredProcessPort,type ServiceSuccession} from './descendants.js';
import { StartupBaselines, type BaselineTarget } from './baseline.js';
import { HostWorkspaceRecords } from './workspace-records.js';
import { seedOwnedRepository } from './repository.js';
import { RegisteredBuiltinWorkers, type OwnedWorkerFact } from './workers.js';
import type { EvidenceStore } from '../evidence.js';
import { fixtureOwnerIdentity } from '../authority.js';
import { z } from 'zod';
import type { OwnedRoot,OwnedFixture } from '../ownership.js';
import { appendCreatedWorkspaces,validateOwnedWorkspaceRoster,enrollOwnedLegacyAgent,requireOwnedWorkspace,requireOwnedWorkspaceRecord } from '../workspaces.js';

const check = (condition: unknown, code: FixtureError['code'] = 'ownership-mismatch') => { if (!condition) throw new FixtureError(code); };
const hash = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
export const legacyProfiles = ['legacy-deterministic', 'legacy-real-codex', 'legacy-real-claude', 'legacy-real-cursor', 'legacy-real-opencode'] as const;
export interface HostConfig extends ProductionConfig {
  loomBinary: string; fleetBinary: string; nodeBinary: string; gitBinary: string;
  pinnedOpenCodeBinary: string;
  // Paths and provider auth roots are pinned by the reviewed launcher, not YAML.
  realBinaries: Partial<Record<'codex' | 'claude' | 'cursor' | 'opencode', { executable: string; sha256: string; authRoot: string }>>;
  registeredProcesses?:{pythonBinary:string};
  daemon: boolean; fakeGitHub: boolean; maxBudgetUsd: string;
}
export type Http = (origin: string, method: 'GET' | 'POST' | 'PATCH' | 'DELETE', relative: string, body: unknown, signal: AbortSignal) => Promise<{ status: number; body: unknown }>;
export const readHttp: Http = async (origin, method, relative, body, signal) => {
  check(relative.startsWith('/') && !relative.startsWith('//') && !relative.includes('\\') && !decodeURIComponent(relative).split(/[/?]/).includes('..'));
  const response = await fetch(new URL(relative, origin), { method, signal, redirect: 'error',
    ...(method === 'POST' || method === 'PATCH' ? { headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) } : {}) });
  const text = await response.text(); check(Buffer.byteLength(text) <= 4 * 1024 * 1024, 'observation-failed');
  return { status: response.status, body: relative === '/readyz' ? text : text ? JSON.parse(text) : null };
};

export class HostFixtureDriver implements FixtureDriver {
  private root = '';
  private profile = '';
  private leaseId = '';
  private provisionedRunId='';
  private readonly stamps = new Map<string, { dev: number; ino: number }>();
  private readonly handles = new Map<string, OwnedProcess>();
  private descendants?:OwnedDescendants;
  private readonly retainedServiceRegistrations=new Map<string,unknown>();
  private readonly serviceIds=new Map<string,string>();
  private readonly terminatedServices=new Set<string>();
  private readonly pendingSuccessors:ServiceSuccession['pending']=new Map();
  private readonly parentHandles=new Map<string,{id:string;handle:OwnedProcess;generation:string}>();
  private workspaceRecords?:HostWorkspaceRecords;
  private workspaceOwner?:Readonly<FixtureAuthorityOwner>;
  private workspaceEvidence?:EvidenceStore;
  private workspaceFixture?:OwnedFixture;
  private workerRegistrations?:RegisteredBuiltinWorkers;
  private runtimeRemoved=false;
  private readonly activeOperations = new Set<string>();
  private async withServiceOperation<T>(id:string,operation:()=>Promise<T>):Promise<T>{
    check(!this.activeOperations.has(id),'identity-mismatch');
    this.activeOperations.add(id);
    try{return await operation();}finally{this.activeOperations.delete(id);}
  }
  private requireWorkerRegistrationIdle(){check(!['worker-registration','cli-launch'].some(id=>this.activeOperations.has(id)),'identity-mismatch');}
  private requireHandle(id:string,generation:string){
    const handle=this.handles.get(id);check(handle&&handle.generation===generation,'identity-mismatch');return handle!;
  }
  private requireCurrentHandle(id:string,handle:OwnedProcess){check(this.handles.get(id)===handle,'identity-mismatch');}
  private readonly commands = new Map<string, { command: HostCommand; readiness: string }>();
  private record?: (resource: Resource) => void;
  private readonly cleanups: (() => Promise<void>)[] = [];
  private readonly unspawned = new Map<string, string>();
  private sockets: PortReservation[] = [];
  private ports: number[] = [];
  private lock?: { path: string; contents: string };
  private backend = '';
  private plan?: FixturePlan;
  private renderer?: PreparedRenderer;
  private readonly config: HostConfig;
  private readonly baselines=new StartupBaselines({generation:async(target,signal)=>{
    const handle=this.handles.get(target);check(handle?.state()==='running');await this.inspectOwnedProcess(target,handle!.generation,signal);return handle!.generation;
  },request:(target,method,relative,signal,generation)=>this.requestOwnedHttp(target,method,relative,null,signal,generation)});
  constructor(config: HostConfig, private readonly processes: HostProcesses = nodeProcesses,
    private readonly files: typeof fs = fs, private readonly http: Http = readHttp,
    private readonly uuid: () => string = randomUUID, private readonly reserve: () => Promise<PortReservation> = reservePort,
    private readonly codexProtocol: CodexProtocolProbe = initializeCodex,private readonly registeredPort?:RegisteredProcessPort,
    private readonly clock:()=>number=Date.now) { this.config = structuredClone(config); }
  get runtimeRoot() { return this.root; }
  get workspaceRoot() { return path.join(this.root, 'runtime', 'e2e-workspace'); }
  get processesById(): ReadonlyMap<string, OwnedProcess> { return this.handles; }
  get configurationRoot() { return path.join(this.workspaceRoot, '.loom-config'); }
  get cliRegistration() { return { binary: this.config.loomBinary, cwd: this.workspaceRoot, env: this.env() }; }
  get executionRouting() { check(this.plan); return fixtureRouting(this.plan!); }
  createOperationAuthority(owner:FixtureAuthorityOwner) {check(this.plan);return fixtureOperationAuthority(owner,this.plan!);}
  async runtimeIdentity(signal:AbortSignal){
    return this.withServiceOperation('registered-services',async()=>{
      const parents=await this.registeredParents(signal),serve=this.parentHandles.get('serve');check(serve,'unsupported-capability');
      const parent=parents.find(value=>value.id===serve!.id);check(parent,'identity-mismatch');
      const actual=await this.descendants!.inspect(parent!.id,parent!.generation);
      check(actual.fixtureRunId===this.provisionedRunId&&this.provisionedRunId.length>0,'identity-mismatch');
      await this.verifyRegisteredParents(parents,signal);return {fixtureRunId:this.provisionedRunId};
    });
  }
  async ownedWorkspaceRoster(owner:FixtureAuthorityOwner,store:EvidenceStore,signal:AbortSignal){
    check(owner.leaseId===this.leaseId&&owner.profile===this.profile&&this.workspaceRecords,'unsupported-capability');
    check(!this.workspaceOwner||JSON.stringify(this.workspaceOwner)===JSON.stringify(fixtureOwnerIdentity(owner)));
    const roster=await this.workspaceRecords!.roster(owner,store,signal);
    this.workspaceOwner=Object.freeze(fixtureOwnerIdentity(owner));this.workspaceEvidence=store;return roster;
  }
  bindOwnedFixture(fixture:OwnedFixture,store:EvidenceStore){
    check(this.workspaceOwner&&store===this.workspaceEvidence&&fixture.leaseId===this.leaseId&&
      JSON.stringify(fixtureOwnerIdentity(fixture))===JSON.stringify(this.workspaceOwner)&&!this.workspaceFixture);
    validateOwnedWorkspaceRoster(fixture);this.workspaceFixture=fixture;
  }
  async createOwnedWorkspaceFixture(fixtureId:'slack-clone'|'legacy-e2e-repo'|'agent-api-source-repo',workspaceId:string,name:string,signal:AbortSignal){
    signal.throwIfAborted();check(this.workspaceOwner&&this.workspaceEvidence&&this.workspaceRecords,'unsupported-capability');
    // Native source setups have distinct main/README/multiple-repo contracts;
    // this legacy factory cannot silently stand in for those source fixtures.
    check(fixtureId==='slack-clone'||fixtureId==='legacy-e2e-repo','unsupported-capability');
    check(/^[a-z][a-z0-9-]{0,63}$/.test(name)&&workspaceId===name.toUpperCase()&&!this.workspaceRecords!.has(workspaceId));
    return this.withServiceOperation('workspace-fixture',async()=>{
      check(!this.workspaceRecords!.has(workspaceId));const before=await this.workspaceStoreIdentity(signal);
      const sourceParent=path.join(this.root,'runtime',`repositories-${this.uuid()}`);await this.files.mkdir(sourceParent,{mode:0o700});
      const sourceRepo=path.join(sourceParent,fixtureId==='slack-clone'?'slack-clone':'agent-repo');
      await seedOwnedRepository(fixtureId==='slack-clone'?'slack-clone':'empty-legacy',{
        runtimeRoot:path.join(this.root,'runtime'),destination:sourceRepo,source:this.config.loom,
        gitBinary:this.config.gitBinary,toolPath:this.config.toolPath},this.processes,signal,this.files);
      const created=await this.requestOwnedHttp('api','POST','/api/workspaces',{name,type:'empty',repos:[sourceRepo]},signal);
      await this.workspaceRecords!.captureCreated(workspaceId,sourceRepo,created,before,signal);
      const record=await this.workspaceRecords!.creationRecord(this.workspaceOwner!,workspaceId,this.workspaceEvidence!,signal);
      if(this.workspaceFixture)await appendCreatedWorkspaces(this.workspaceFixture,[record],signal,this.workspaceEvidence!);
      return record;
    });
  }
  async readWorkspaceLegacyAgent(owner:FixtureAuthorityOwner,workspaceId:string,name:string,signal:AbortSignal){
    check(owner.leaseId===this.leaseId&&owner.profile===this.profile&&this.workspaceRecords,'unsupported-capability');
    check(this.workspaceOwner&&JSON.stringify(this.workspaceOwner)===JSON.stringify(fixtureOwnerIdentity(owner)));
    return this.workspaceRecords!.legacyAgent(owner,workspaceId,name,signal);
  }
  /** Returns only concretely registered builtin workers. Native harness and
   * terminal metadata are separate contracts; this is not their absence proof. */
  async refreshOwnedProductProcesses(signal:AbortSignal):Promise<readonly OwnedWorkerFact[]>{
    signal.throwIfAborted();
    check(this.workspaceFixture&&this.workspaceOwner&&this.workspaceEvidence&&this.workspaceRecords&&this.descendants,'unsupported-capability');
    check(!['registered-services','serve','daemon','cleanup-preparation','cleanup-resource','cli-launch'].some(id=>this.activeOperations.has(id)),'identity-mismatch');
    const daemon=this.handles.get('daemon');check(daemon?.state()==='running','unsupported-capability');
    return this.withServiceOperation('worker-registration',async()=>{
      if(!this.workerRegistrations){
        const root=this.stamps.get(this.root),config=this.stamps.get(this.configurationRoot);check(root&&config);
        this.workerRegistrations=new RegisteredBuiltinWorkers({runtimeRoot:{path:this.root,device:root!.dev,inode:root!.ino},
          configurationRoot:{path:this.configurationRoot,device:config!.dev,inode:config!.ino},workspaceId:this.workspaceFixture!.workspaceId,
          daemonCwd:this.workspaceRoot,loomExecutable:this.config.loomBinary},{
          parent:async abort=>{
            const parents=await this.registeredParents(abort),record=this.parentHandles.get('daemon');check(record);
            const current=this.handles.get('daemon');check(current&&current.state()==='running'&&record!.handle===current);
            const parent=parents.find(value=>value.id===record!.id);check(parent);
            return {id:parent!.id,identity:await this.descendants!.inspect(parent!.id,parent!.generation)};
          },verifyParent:async(parent,abort)=>{
            await this.verifyRegisteredParents([{id:parent.id,pid:parent.identity.pid,generation:parent.identity.generation}],abort);
          },verifyActor:async(name,worktree,abort)=>{
            const fixture=this.workspaceFixture!;
            requireOwnedWorkspaceRecord(fixture,fixture.workspaceId,'legacy-agent-name');
            await enrollOwnedLegacyAgent(fixture,fixture.workspaceId,name,abort,this.workspaceEvidence!);
            const before=await this.files.lstat(worktree),common=await this.ownedCommonDir(worktree,abort);
            const source=await this.workspaceRecords!.legacyPhysicalSource(this.workspaceOwner!,fixture.workspaceId,name,common,abort);
            const owned=requireOwnedWorkspace(fixture,fixture.workspaceId,name,'legacy-agent-name',source.repoName);
            check(owned.repo===source.repo&&owned.commonDir===common);
            const after=await this.files.lstat(worktree);check(!after.isSymbolicLink()&&before.dev===after.dev&&before.ino===after.ino);
          },nextId:()=>this.uuid()},this.descendants!,this.files);
      }
      return this.workerRegistrations.refresh(signal);
    });
  }
  async resolveLegacyWorktree(workspaceId:string,agentName:string,signal:AbortSignal,repoName?:string):Promise<{complete:true;workspaceId:string;agentName:string;root:OwnedRoot;branch:string;commonDir:string}>{
    check(this.workspaceOwner&&this.workspaceRecords,'unsupported-capability');
    if(this.workspaceFixture){
      requireOwnedWorkspaceRecord(this.workspaceFixture,workspaceId,'legacy-agent-name');
      await enrollOwnedLegacyAgent(this.workspaceFixture,workspaceId,agentName,signal,this.workspaceEvidence!);
    }
    const actor=await this.readWorkspaceLegacyAgent(this.workspaceOwner!,workspaceId,agentName,signal);
    await this.workspaceRecords!.workspace(workspaceId,signal);
    const completion=(await this.launchOwnedCli(['workspace','ops','diagnose',workspaceId,'--json'],{},'',true,signal)).completion;
    check(completion.complete&&completion.exitCode===0,'observation-failed');
    const status=z.object({ok:z.boolean(),workspace:z.object({key:z.string()}).passthrough(),agents:z.array(z.object({
      name:z.string(),worktree_path:z.string().optional(),worktree_ready:z.boolean()}).passthrough()).max(1000)}).passthrough().parse(JSON.parse(completion.stdout));
    check(status.workspace.key===workspaceId);
    const matches=status.agents.filter(agent=>agent.name===agentName);check(matches.length===1&&matches[0]!.worktree_ready&&matches[0]!.worktree_path);
    const worktree=matches[0]!.worktree_path!,before=await this.files.lstat(worktree),commonDir=await this.ownedCommonDir(worktree,signal);
    const selected=await this.workspaceRecords!.legacyPhysicalSource(this.workspaceOwner!,workspaceId,agentName,commonDir,signal,repoName);
    if(this.workspaceFixture){
      const owned=requireOwnedWorkspace(this.workspaceFixture,workspaceId,agentName,'legacy-agent-name',selected.repoName);
      check(owned.repo===selected.repo&&owned.commonDir===commonDir);
    }
    const branch=(await this.processes.run({executable:this.config.gitBinary,argv:['symbolic-ref','--quiet','--short','HEAD'],cwd:worktree,
      env:{PATH:this.config.toolPath,HOME:path.join(this.root,'runtime','home'),GIT_CONFIG_NOSYSTEM:'1'}},signal)).trim();
    check(branch.length>0&&!branch.includes('\n')&&!branch.includes('\0'));
    await this.workspaceRecords!.workspace(workspaceId,signal);
    check(JSON.stringify(await this.readWorkspaceLegacyAgent(this.workspaceOwner!,workspaceId,agentName,signal))===JSON.stringify(actor));
    check(JSON.stringify(await this.workspaceRecords!.legacyPhysicalSource(this.workspaceOwner!,workspaceId,agentName,commonDir,signal,repoName))===JSON.stringify(selected));
    if(this.workspaceFixture){
      const owned=requireOwnedWorkspace(this.workspaceFixture,workspaceId,agentName,'legacy-agent-name',selected.repoName);
      check(owned.repo===selected.repo&&owned.commonDir===commonDir);
    }
    check(await this.ownedCommonDir(worktree,signal)===commonDir);
    const after=await this.files.lstat(worktree);check(!after.isSymbolicLink()&&before.dev===after.dev&&before.ino===after.ino);
    return {complete:true,workspaceId,agentName,root:{path:worktree,device:after.dev,inode:after.ino},branch,commonDir};
  }
  async readLegacyWorktreeHead(workspaceId:string,agentName:string,signal:AbortSignal,repoName?:string){
    check(this.workspaceOwner&&this.workspaceRecords,'unsupported-capability');
    const actor=await this.readWorkspaceLegacyAgent(this.workspaceOwner!,workspaceId,agentName,signal);
    const resolved=await this.resolveLegacyWorktree(workspaceId,agentName,signal,repoName);
    const head=(await this.processes.run({executable:this.config.gitBinary,argv:['rev-parse','HEAD'],cwd:resolved.root.path,
      env:{PATH:this.config.toolPath,HOME:path.join(this.root,'runtime','home'),GIT_CONFIG_NOSYSTEM:'1'}},signal)).trim();
    check(/^[a-f0-9]{40}$/.test(head));const after=await this.files.lstat(resolved.root.path);
    check(!after.isSymbolicLink()&&after.dev===resolved.root.device&&after.ino===resolved.root.inode);
    check(await this.ownedCommonDir(resolved.root.path,signal)===resolved.commonDir);
    await this.workspaceRecords!.workspace(workspaceId,signal);
    check(JSON.stringify(await this.readWorkspaceLegacyAgent(this.workspaceOwner!,workspaceId,agentName,signal))===JSON.stringify(actor));
    const selected=await this.workspaceRecords!.legacyPhysicalSource(this.workspaceOwner!,workspaceId,agentName,resolved.commonDir,signal,repoName);
    if(this.workspaceFixture){
      const owned=requireOwnedWorkspace(this.workspaceFixture,workspaceId,agentName,'legacy-agent-name',selected.repoName);
      check(owned.repo===selected.repo&&owned.commonDir===resolved.commonDir);
    }
    return head;
  }
  private async workspaceStoreIdentity(signal:AbortSignal){
    await this.prepareObserve(signal);check(this.descendants?.has('registered-fleet-db'),'unsupported-capability');
    const identity=this.descendants!.initial('registered-fleet-db');
    check((await this.descendants!.inspect('registered-fleet-db',identity.generation)).state==='running');
    const directory=path.join(this.configurationRoot,'fleet-db'),stat=await this.files.lstat(directory);
    check(stat.isDirectory()&&!stat.isSymbolicLink()&&await this.files.realpath(directory)===directory);
    return {storeId:`${directory}#${stat.dev}:${stat.ino}`,storeGeneration:identity.generation};
  }
  private async ownedCommonDir(repo:string,signal:AbortSignal){
    check(repo.startsWith(path.join(this.root,'runtime')+path.sep)&&await this.files.realpath(repo)===repo);
    const before=await this.files.lstat(repo);check(before.isDirectory()&&!before.isSymbolicLink());
    const relative=(await this.processes.run({executable:this.config.gitBinary,argv:['rev-parse','--git-common-dir'],cwd:repo,
      env:{PATH:this.config.toolPath,HOME:path.join(this.root,'runtime','home'),GIT_CONFIG_NOSYSTEM:'1'}},signal)).trim();
    check(relative.length>0&&!relative.includes('\n'));
    const candidate=path.resolve(repo,relative),commonDir=await this.files.realpath(candidate);
    check(commonDir===candidate&&commonDir.startsWith(path.join(this.root,'runtime')+path.sep));
    const stat=await this.files.lstat(commonDir),after=await this.files.lstat(repo);
    check(stat.isDirectory()&&!stat.isSymbolicLink()&&before.dev===after.dev&&before.ino===after.ino&&!after.isSymbolicLink());
    for(const [filename,stamp] of [[repo,after],[commonDir,stat]] as const){const old=this.stamps.get(filename);check(!old||old.dev===stamp.dev&&old.ino===stamp.ino);this.stamps.set(filename,{dev:stamp.dev,ino:stamp.ino});}
    return commonDir;
  }
  freshFixtureBaseline(target:BaselineTarget,signal:AbortSignal){return this.baselines.freshFixtureBaseline(target,signal);}
  resetFixtureBaseline(target:BaselineTarget,generation:string,signal:AbortSignal){return this.baselines.resetFixtureBaseline(target,generation,signal);}
  async rendererRuntimeTarget(signal:AbortSignal) {
    const target=this.handles.get('frontend');check(target?.state()==='running'&&this.renderer,'identity-mismatch');
    await this.inspectOwnedProcess('frontend',target!.generation,signal);
    return {targetId:'frontend',generation:target!.generation,buildRoot:this.renderer!.buildRoot};
  }
  async fakeModelOrigin(signal: AbortSignal): Promise<string> {
    signal.throwIfAborted(); check(this.profile === 'legacy-deterministic', 'unsupported-capability');
    const handle = this.handles.get('fake-model');
    check(handle && handle.pid > 0 && handle.state() === 'running', 'ownership-mismatch');
    await this.inspectOwnedProcess('fake-model', handle!.generation, signal);
    return `http://127.0.0.1:${this.ports[2]}`;
  }
  enrollCleanup(cleanup: () => Promise<void>) { check(this.record); this.cleanups.push(cleanup); }
  private async drainCleanups() {
    while (this.cleanups.length) { await this.cleanups[this.cleanups.length - 1]!(); this.cleanups.pop(); }
  }
  private async registeredParents(signal:AbortSignal){
    signal.throwIfAborted();check(this.descendants,'unsupported-capability');const identities=[];
    for(const name of ['serve','daemon']){const handle=this.handles.get(name);if(!handle||handle.state()!=='running')continue;
      let recorded=this.parentHandles.get(name);
      if(!recorded||recorded.handle!==handle){
        const id=`registered-parent-${name}-${this.uuid()}`;
        await this.descendants!.enroll({id,pid:handle.pid,executable:this.config.loomBinary,configurationRoot:this.configurationRoot,
          argvSha256:hash(Buffer.from([this.config.loomBinary,...handle.argv].join('\0')+'\0'))});
        recorded={id,handle,generation:handle.generation};this.parentHandles.set(name,recorded);
      }
      this.requireCurrentHandle(name,recorded.handle);check(recorded.handle.state()==='running'&&recorded.handle.generation===recorded.generation);
      const initial=this.descendants!.initial(recorded.id),actual=await this.descendants!.inspect(recorded.id,initial.generation);
      check(actual.state==='running'&&actual.pid===handle.pid);identities.push({id:recorded.id,pid:actual.pid,generation:actual.generation});
    }
    return identities;
  }
  private async verifyRegisteredParents(parents:Parameters<ServiceSuccession['verifyParents']>[0],signal:AbortSignal){
    signal.throwIfAborted();for(const parent of parents){
      const matches=[...this.parentHandles.entries()].filter(([,value])=>value.id===parent.id);check(matches.length===1);
      const [name,recorded]=matches[0]!;this.requireCurrentHandle(name,recorded.handle);check(recorded.handle.state()==='running'&&recorded.handle.generation===recorded.generation);
      const actual=await this.descendants!.inspect(parent.id,parent.generation);
      check(actual.state==='running'&&actual.pid===parent.pid);this.requireCurrentHandle(name,recorded.handle);check(recorded.handle.state()==='running'&&recorded.handle.generation===recorded.generation);
    }
  }
  private async observeRegisteredServices(signal:AbortSignal){
    signal.throwIfAborted();if(this.descendants&&!this.runtimeRemoved){
      const root=this.configurationRoot,stamp=this.stamps.get(root),before=await this.files.lstat(root);
      check(stamp&&!before.isSymbolicLink()&&before.isDirectory()&&await this.files.realpath(root)===root&&stamp.dev===before.dev&&stamp.ino===before.ino);
      const executable=this.profile==='legacy-real-opencode'?this.config.realBinaries.opencode!.executable:this.config.pinnedOpenCodeBinary;
      await readRegisteredHostServices(root,this.config.fleetBinary,executable,this.descendants,this.retainedServiceRegistrations,{
        currentIds:this.serviceIds,terminated:this.terminatedServices,pending:this.pendingSuccessors,nextId:()=>this.uuid(),
        parents:()=>this.registeredParents(signal),verifyParents:parents=>this.verifyRegisteredParents(parents,signal),
        argvSha256:hash(Buffer.from([executable,'serve','--service'].join('\0')+'\0'))});
      const after=await this.files.lstat(root);check(!after.isSymbolicLink()&&before.dev===after.dev&&before.ino===after.ino);
    }
  }
  async prepareObserve(signal:AbortSignal){return this.withServiceOperation('registered-services',()=>this.observeRegisteredServices(signal));}
  async prepareCleanup(signal:AbortSignal){
    this.requireWorkerRegistrationIdle();
    return this.withServiceOperation('cleanup-preparation',async()=>{await this.drainCleanups();await this.prepareObserve(signal);});
  }
  async inspectOwnedProcess(id: string, generation: string, signal: AbortSignal) {
    signal.throwIfAborted();if(this.descendants?.has(id)){const identity=await this.descendants.inspect(id,generation);return {id,generation:identity.generation,pid:identity.pid,state:identity.state};}
    const handle = this.handles.get(id);
    check(handle && handle.generation === generation, 'identity-mismatch');
    return { id, generation: handle!.generation, pid: handle!.pid, state: handle!.state() };
  }
  async launchOwnedCli(argv: readonly string[], envOverrides: Readonly<Record<string, string>>, stdin: string,
    waitForExit: boolean, signal: AbortSignal): Promise<{ id: string; generation: string; pid: number; completion: CliCompletion }> {
    signal.throwIfAborted();this.requireWorkerRegistrationIdle(); check(this.record && this.processes.launch, 'unsupported-capability');
    check(!['registered-services','cleanup-preparation','cleanup-resource','http-mutation'].some(id=>this.activeOperations.has(id)),'identity-mismatch');
    return this.withServiceOperation('cli-launch',async()=>{
    check(argv.length > 0 && argv.length <= 128 && argv.every(arg => typeof arg === 'string' && arg.length <= 1024 * 1024 && !arg.includes('\0')));
    const seed = argv.length === 12 && argv[0] === 'daemon' && argv[1] === 'seed-worktree' && argv[2] === '--workspace' &&
      argv[4] === '--agent' && argv[6] === '--file' && argv[8] === '--content' && argv[9] === '-' && argv[10] === '--message' && this.profile === 'legacy-deterministic';
    check(seed || ['--workspace', 'usage', 'agent', 'workspace', 'config'].includes(argv[0]!), 'unsupported-capability');
    if(argv[0]==='--workspace'&&argv[2]==='--backend'&&argv[4]==='task'){
      const route=this.executionRouting;
      check(route.allowedTaskBackends.includes(argv[3]!)&&(route.evidenceClass==='deterministic'||route.externalProvider),'unsupported-capability');
    }
    check(Object.keys(envOverrides).every(key => ['LOOM_WORKSPACE_ID', 'LOOM_ASSIGNED_TASK_ID', 'LOOM_SOURCE_REPOS'].includes(key) ||
      seed && key === 'LOOM_TESTSUPPORT' && envOverrides[key] === '1'));
    check(Buffer.byteLength(stdin) <= 1024 * 1024);
    const id = `cli-${this.uuid()}`, generation = this.uuid();
    const command = { executable: this.config.loomBinary, argv: [...argv], cwd: this.workspaceRoot, env: { ...this.env(), ...envOverrides } };
    this.record!({ id, kind: 'process', generation });
    let handle;
    try { handle = this.processes.launch!(command, stdin, generation); }
    catch (error) { if (error instanceof LaunchNotStarted) this.unspawned.set(id, generation); throw error; }
    this.handles.set(id, handle);
    check(handle.generation === generation && handle.pid > 0); await handle.ready(signal);
    const completion = waitForExit ? await handle.completion(signal) : { exitCode: null, stdout: '', stderr: '', complete: false };
    return { id, generation, pid: handle.pid, completion };
    });
  }
  private async stopCapturedProcess(id:string,generation:string,signal:AbortSignal){
    if(this.descendants?.has(id)){await this.descendants.inspect(id,generation);signal.throwIfAborted();await this.descendants.stop(id,generation);return {beforeGeneration:generation,afterGeneration:null,affectedIds:[id],complete:true as const};}
    const handle=this.requireHandle(id,generation);
    await this.inspectOwnedProcess(id,generation,signal);
    signal.throwIfAborted();this.requireCurrentHandle(id,handle);
    await handle.stop();this.requireCurrentHandle(id,handle);
    check(handle.state()==='exited');
    return {beforeGeneration:generation,afterGeneration:null,affectedIds:[id],complete:true as const};
  }
  async stopOwnedProcess(id: string, generation: string, signal: AbortSignal) {
    this.requireWorkerRegistrationIdle();
    const stop=()=>this.stopCapturedProcess(id,generation,signal);
    return this.withServiceOperation(id,()=>this.descendants?.has(id)||id==='serve'||id==='daemon'?this.withServiceOperation('registered-services',stop):stop());
  }
  async terminateRegisteredNativeService(id:string,generation:string,signal:AbortSignal){
    this.requireWorkerRegistrationIdle();
    check(id===this.serviceIds.get('registered-opencode-service')&&this.descendants?.has(id),'unsupported-capability');
    return this.withServiceOperation('registered-services',async()=>{
      signal.throwIfAborted();await this.observeRegisteredServices(signal);check(id===this.serviceIds.get('registered-opencode-service')&&(await this.descendants!.inspect(id,generation)).state==='running');signal.throwIfAborted();
      await this.descendants!.terminateGracefully(id,generation);
      check((await this.descendants!.inspect(id,generation)).state==='exited');
      this.terminatedServices.add(id);
      return {beforeGeneration:generation,afterGeneration:null,affectedIds:[id],complete:true as const};
    });
  }
  async restartOwnedProcess(id: string, generation: string, signal: AbortSignal) {
    this.requireWorkerRegistrationIdle();
    const restart=async()=>{
      const saved=this.commands.get(id);check(saved&&this.record,'unsupported-capability');
      const handle=this.requireHandle(id,generation);
      await this.stopCapturedProcess(id,generation,signal);this.requireCurrentHandle(id,handle);
      await this.start(id,saved!.command,saved!.readiness,this.record!,signal,false);
      const after=this.handles.get(id)!.generation;check(after!==generation,'identity-mismatch');
      return {beforeGeneration:generation,afterGeneration:after,affectedIds:[id],complete:true as const};
    };
    const result=await this.withServiceOperation(id,()=>id==='serve'||id==='daemon'?this.withServiceOperation('registered-services',restart):restart());
    if(id==='fake-model'||id==='fake-github')await this.baselines.captureSuccessfulStart(id,result.afterGeneration,signal);
    return result;
  }
  async requestOwnedHttp(target: 'api' | 'fake-model' | 'fake-github', method: Parameters<Http>[1], relative: string, body: unknown, signal: AbortSignal, expectedGeneration?:string) {
    signal.throwIfAborted(); check(this.ports.length === 5);
    if(method!=='GET')this.requireWorkerRegistrationIdle();
    check(target === 'api' ? relative.startsWith('/api/') : /^\/__(script|reset|fixture|state|requests)(\?|$)/.test(relative));
    check(target !== 'fake-model' || this.profile === 'legacy-deterministic', 'unsupported-capability');
    check(target !== 'fake-github' || this.config.fakeGitHub, 'unsupported-capability');
    const service = { api: 'serve', 'fake-model': 'fake-model', 'fake-github': 'fake-github' }[target];
    const request=()=>this.withServiceOperation(service,async()=>{
      const handle=this.handles.get(service);check(handle&&handle.state()==='running'&&handle.pid>0,'ownership-mismatch');
      const generation=expectedGeneration??handle!.generation;check(handle!.generation===generation,'identity-mismatch');
      const index={api:0,'fake-model':2,'fake-github':4}[target],origin=`http://127.0.0.1:${this.ports[index]}`;
      await this.inspectOwnedProcess(service,generation,signal);
      signal.throwIfAborted();this.requireCurrentHandle(service,handle!);check(handle!.state()==='running','identity-mismatch');
      const response=await this.http(origin,method,relative,body,signal);
      signal.throwIfAborted();this.requireCurrentHandle(service,handle!);check(handle!.state()==='running','identity-mismatch');return response;
    });
    return method==='GET'?request():this.withServiceOperation('http-mutation',request);
  }
  private env(): Record<string, string> {
    const c = this.config; const runtime = path.join(this.root, 'runtime');
    const configRoot = path.join(this.workspaceRoot, '.loom-config');
    const backendConfig = c.realBinaries[this.backend as keyof HostConfig['realBinaries']];
    const real = this.profile !== 'legacy-deterministic';
    const farm = real ? `stubs-real-${this.backend}` : 'stubs';
    return { RUN_ID:this.provisionedRunId,HOME: real ? c.hostHome : path.join(runtime, 'home'),
      PATH: `${path.join(runtime, 'bin')}:${path.join(c.loom.source.root, 'e2e', farm)}:${c.toolPath}`,
      LOOM_CONFIG_DIR: configRoot, LOOM_DISABLE_H2C: '1', LOOM_ISSUE_BACKEND: 'fleetdb', LOOM_FLEET_DB_ACTOR: 'loom-e2e',
      FLEET_DB_BIN: c.fleetBinary, FLEET_RATE_LIMIT_ENABLED: 'false', FLEET_REDIS_POOL_SIZE: '200', FLEET_REDIS_MIN_IDLE_CONNS: '10',
      LOOM_SDK_ROOT: path.join(c.loom.source.root, 'sdk'), LOOM_LEAD_CONTROLLED: '1',
      ...(this.executionRouting.modelSelection.kind==='exact-model'?{LOOM_AGENT_MODEL:this.executionRouting.modelSelection.model,LOOM_OPENCODE_MODEL:this.executionRouting.modelSelection.model}:{}),
      LOOM_FRONTEND_DIR: this.renderer!.buildRoot,
      LOOM_MAX_BUDGET_USD: c.maxBudgetUsd, GIT_TERMINAL_PROMPT: '0', GIT_ASKPASS: '/usr/bin/false', SSH_ASKPASS: '/usr/bin/false',
      GIT_CONFIG_COUNT: '3', GIT_CONFIG_KEY_0: 'credential.helper', GIT_CONFIG_VALUE_0: '',
      GIT_CONFIG_KEY_1: 'protocol.allow', GIT_CONFIG_VALUE_1: 'never',
      GIT_CONFIG_KEY_2: 'protocol.file.allow', GIT_CONFIG_VALUE_2: 'always',
      ...(real ? {
        ...(this.backend === 'codex' ? { CODEX_HOME: backendConfig!.authRoot } : { CODEX_HOME: path.join(runtime, 'empty-auth', 'codex') }),
        ...(this.backend === 'claude' ? { CLAUDE_CONFIG_DIR: backendConfig!.authRoot } : { CLAUDE_CONFIG_DIR: path.join(runtime, 'empty-auth', 'claude') }),
      } : { OPENAI_API_KEY: 'stub-e2e', CODEX_HOME: path.join(runtime, 'empty-auth', 'codex'), CLAUDE_CONFIG_DIR: path.join(runtime, 'empty-auth', 'claude'),
        LOOM_OPENCODE_BIN: c.pinnedOpenCodeBinary, OPENCODE_DISABLE_MODELS_FETCH: '1', LOOM_AGENT_HISTORY_RETENTION: '60s',
        XDG_CONFIG_HOME: path.join(configRoot, 'agents-opencode', 'config'), XDG_DATA_HOME: path.join(configRoot, 'agents-opencode', 'data'),
        XDG_STATE_HOME: path.join(configRoot, 'agents-opencode', 'state'), XDG_CACHE_HOME: path.join(configRoot, 'agents-opencode', 'cache') }),
      ...(this.profile === 'legacy-real-opencode' ? { LOOM_OPENCODE_BIN: backendConfig!.executable } : {}),
      ...(this.config.fakeGitHub ? { LOOM_CONNECTOR_GITHUB_BASE_URL: `http://127.0.0.1:${this.ports[4]}` } : {}),
    };
  }
  async identity(plan: FixturePlan) {
    const driver = new ComposeFixtureDriver(this.config, async request => this.processes.run({ executable: request.binary === 'git' ? this.config.gitBinary : request.binary,
      argv: request.args, cwd: request.cwd, env: request.env }, request.signal));
    return driver.identity(plan);
  }
  async preflight(plan: FixturePlan, signal: AbortSignal): Promise<void> {
    check(legacyProfiles.includes(plan.profile as typeof legacyProfiles[number]), 'unsupported-capability');
    this.plan = structuredClone(plan); this.profile = plan.profile;
    check(this.config.fixtureRunId===undefined||/^[A-Za-z0-9_-]{1,128}$/.test(this.config.fixtureRunId),'identity-mismatch');
    const timestamp=this.clock();check(Number.isSafeInteger(timestamp)&&timestamp>=0,'identity-mismatch');
    this.provisionedRunId=this.config.fixtureRunId??String(Math.floor(timestamp/1000));
    fixtureRouting(plan);
    this.backend = plan.profile.replace('legacy-real-', '');
    check(this.profile === 'legacy-deterministic' ? plan.model === 'aft/m' : /^[A-Za-z0-9][A-Za-z0-9._/-]*$/.test(plan.model) && !plan.model.startsWith('aft/'), 'identity-mismatch');
    for (const directory of [this.config.tempParent, this.config.lockParent, this.config.hostHome]) {
      check(await this.files.realpath(directory) === directory && (await this.files.lstat(directory)).isDirectory());
    }
    check(await this.identity(plan), 'source-mismatch');
    this.renderer = await prepareRenderer(this.config.loom);
    for (const binary of [this.config.loomBinary, this.config.fleetBinary, this.config.nodeBinary, this.config.gitBinary]) {
      check(path.isAbsolute(binary) && await this.files.realpath(binary) === binary && (await this.files.lstat(binary)).isFile(), 'source-mismatch');
      await this.files.access(binary, constants.X_OK);
      check([this.config.loom, this.config.fleet, this.config.engine, this.config.adapter].some(build =>
        build.build.entries.some(entry => path.join(build.build.root, entry.relativePath) === binary)), 'source-mismatch');
    }
    if(this.config.registeredProcesses){
      const binary=this.config.registeredProcesses.pythonBinary,helper=path.join(this.config.adapter.build.root,'fixture/kernel-process.py');
      check(path.isAbsolute(binary)&&await this.files.realpath(binary)===binary,'source-mismatch');await this.files.access(binary,constants.X_OK);
      for(const filename of [binary,helper])check([this.config.loom,this.config.fleet,this.config.engine,this.config.adapter].some(build=>build.build.entries.some(entry=>
        path.join(build.build.root,entry.relativePath)===filename)), 'source-mismatch');
    }
    const real = this.profile !== 'legacy-deterministic';
    const farm = path.join(this.config.loom.source.root, 'e2e', real ? `stubs-real-${this.backend}` : 'stubs');
    const selected = this.backend === 'cursor' ? 'cursor-agent' : this.backend;
    const realConfig = this.config.realBinaries[this.backend as keyof HostConfig['realBinaries']];
    for (const tool of ['codex', 'claude', 'cursor-agent', 'opencode', 'gemini', 'gh']) {
      if (real && tool === selected) continue;
      const filename = path.join(farm, tool); await this.files.access(filename, constants.X_OK);
      check((await this.files.realpath(filename)).startsWith(farm + path.sep), 'identity-mismatch');
    }
    if (real) {
      check(realConfig && path.isAbsolute(realConfig.executable) && await this.files.realpath(realConfig.executable) === realConfig.executable &&
        !realConfig.executable.startsWith(path.join(this.config.loom.source.root, 'e2e', 'stubs')), 'identity-mismatch');
      check(hash(await this.files.readFile(realConfig!.executable)) === realConfig!.sha256, 'source-mismatch');
      check([this.config.loom, this.config.fleet, this.config.engine, this.config.adapter].some(build =>
        build.build.entries.some(entry => path.join(build.build.root, entry.relativePath) === realConfig!.executable && entry.sha256 === realConfig!.sha256)), 'source-mismatch');
      if (this.backend === 'codex' || this.backend === 'claude') {
        const auth = path.join(realConfig!.authRoot, this.backend === 'codex' ? 'auth.json' : '.credentials.json');
        check((await this.files.lstat(auth)).isFile() && !(await this.files.lstat(auth)).isSymbolicLink(), 'identity-mismatch');
      }
      if (this.backend === 'cursor') await this.processes.run({ executable: realConfig!.executable, argv: ['status'], cwd: this.config.loom.source.root,
        env: { PATH: this.config.toolPath, HOME: this.config.hostHome } }, signal);
    } else {
      check(await this.files.realpath(this.config.pinnedOpenCodeBinary) === this.config.pinnedOpenCodeBinary, 'source-mismatch');
      // Must be an attested output, not a merely present binary.
      check(this.config.loom.build.entries.some(entry => path.join(this.config.loom.build.root, entry.relativePath) === this.config.pinnedOpenCodeBinary), 'source-mismatch');
    }
    const storage = await this.files.statfs(this.config.tempParent);
    check(storage.bavail * storage.bsize >= this.config.minimumFreeBytes, 'observation-failed');
  }
  private async stamp(filename: string) {
    const stat = await this.files.lstat(filename); check(!stat.isSymbolicLink());
    this.stamps.set(filename, { dev: stat.dev, ino: stat.ino }); return `${stat.dev}:${stat.ino}`;
  }
  async allocate(leaseId: string, _runId: string, record: (resource: Resource) => void) {
    this.leaseId = leaseId;
    this.root = await this.files.mkdtemp(path.join(this.config.tempParent, 'loom-aft-host-'));
    record({ id: this.root, kind: 'directory', generation: await this.stamp(this.root) });
    await this.files.chmod(this.root, 0o700);
    for (const relative of ['evidence', 'runtime', 'runtime/bin', 'runtime/home', 'runtime/empty-auth/codex', 'runtime/empty-auth/claude',
      'runtime/e2e-workspace/.loom-config', 'runtime/e2e-workspace-2', 'runtime/e2e-workspace/.loom-config/agents-opencode/config/opencode']) {
      const directory = path.join(this.root, relative); await this.files.mkdir(directory, { recursive: true, mode: 0o700 }); await this.stamp(directory);
    }
    if (this.profile !== 'legacy-deterministic') {
      const filename = path.join(this.config.lockParent, `aft-live.${this.backend}.lock`);
      const handle = await this.files.open(filename, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
      this.lock = { path: filename, contents: this.uuid() };
      record({ id: filename, kind: 'lock', generation: await this.stamp(filename) });
      try { await handle.writeFile(this.lock.contents); } finally { await handle.close(); }
      const real = this.config.realBinaries[this.backend as keyof HostConfig['realBinaries']]!;
      await this.files.symlink(real.executable, path.join(this.root, 'runtime', 'bin', this.backend === 'cursor' ? 'cursor-agent' : this.backend));
    }
    // API, frontend, fake-model, OpenCode service, and optional GitHub fixture.
    for (let index = 0; index < 5; index++) {
      const socket = await this.reserve(); this.sockets.push(socket); this.ports.push(socket.port);
      if (index === 0) record({ id: `ports:${leaseId}`, kind: 'ports', generation: leaseId });
    }
  }
  private async closeSockets() {
    for (const socket of this.sockets) await socket.release();
    this.sockets = [];
  }
  private async seed(repo: string, signal: AbortSignal) {
    const command = (argv: string[]) => this.processes.run({ executable: this.config.gitBinary, argv, cwd: repo, env: { PATH: this.config.toolPath,
      HOME: path.join(this.root, 'runtime', 'home'), GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0' } }, signal);
    await command(['init', '-q']);
    await command(['-c', 'user.name=Loom E2E', '-c', 'user.email=loom-e2e@example.test', 'commit', '--allow-empty', '-m', 'e2e seed', '-q']);
  }
  private async start(id: string, command: HostCommand, readiness: string, record: (resource: Resource) => void, signal: AbortSignal,captureBaseline=true) {
    const generation = this.uuid(); record({ id, kind: 'process', generation });
    let handle;
    try { handle = this.processes.start(command, readiness, generation); }
    catch (error) { if (error instanceof LaunchNotStarted) this.unspawned.set(id, generation); throw error; }
    this.handles.set(id, handle); this.commands.set(id, { command, readiness });
    check(handle.generation === generation, 'identity-mismatch');
    await handle.ready(signal); check(handle.pid > 0 && handle.state() === 'running', 'observation-failed');
    if(captureBaseline&&(id==='fake-model'||id==='fake-github'))await this.baselines.captureSuccessfulStart(id,generation,signal);
  }
  async provision(_plan: FixturePlan, record: (resource: Resource) => void, signal: AbortSignal) {
    this.record = record;
    const processPort=this.registeredPort??(this.config.registeredProcesses?createRegisteredProcessPort(this.config.registeredProcesses.pythonBinary,path.join(this.config.adapter.build.root,'fixture/kernel-process.py')):undefined);
    if(processPort)this.descendants=new OwnedDescendants(processPort,record);
    if(this.descendants)this.workspaceRecords=new HostWorkspaceRecords({store:signal=>this.workspaceStoreIdentity(signal),
      read:(workspaceId,view,signal)=>this.requestOwnedHttp('api','GET',`/api/workspaces/${encodeURIComponent(workspaceId)}${view==='legacy-agents'?'/agents':''}`,null,signal),
      commonDir:(repo,signal)=>this.ownedCommonDir(repo,signal)});
    if (this.profile === 'legacy-real-codex') {
      const reservation = await this.reserve(); this.sockets.push(reservation);
      await reservation.release(); this.sockets.pop();
      const endpoint = `ws://127.0.0.1:${reservation.port}`;
      const bounded = AbortSignal.any([signal, AbortSignal.timeout(15000)]);
      await this.start('codex-preflight', { executable: this.config.realBinaries.codex!.executable,
        argv: ['app-server', '--listen', endpoint], cwd: path.join(this.root, 'runtime', 'home'), env: this.env() },
      'readyz:', record, bounded);
      const probe = this.handles.get('codex-preflight')!;
      try {
        check(probe.state() === 'running' && (await this.http(`http://127.0.0.1:${reservation.port}`, 'GET', '/readyz', null, bounded)).status === 200, 'observation-failed');
        await this.codexProtocol(endpoint, bounded);
      } finally { await probe.stop(); }
      check(probe.state() === 'exited', 'ownership-mismatch');
    }
    await this.seed(this.workspaceRoot, signal);
    const second = path.join(this.root, 'runtime', 'e2e-workspace-2'); await this.seed(second, signal);
    if (this.profile === 'legacy-deterministic') {
      const config = path.join(this.workspaceRoot, '.loom-config', 'agents-opencode', 'config', 'opencode');
      await this.files.writeFile(path.join(config, 'service.json'), JSON.stringify({ port: this.ports[3] }), { flag: 'wx', mode: 0o600 });
      await this.files.writeFile(path.join(config, 'opencode.json'), JSON.stringify({ provider: { aft: { name: 'AFT fake', npm: '@ai-sdk/openai-compatible',
        options: { baseURL: `http://127.0.0.1:${this.ports[2]}/v1`, apiKey: 'x' }, models: { m: { name: 'M', limit: { context: 100000, output: 4000 } } } } }, model: 'aft/m' }), { flag: 'wx', mode: 0o600 });
    }
    await this.closeSockets();
    if (this.profile === 'legacy-deterministic') await this.start('fake-model', { executable: this.config.nodeBinary,
      argv: [path.join(this.config.loom.source.root, 'tests', 'aft', 'fixtures', 'fake-model', 'server.mjs')], cwd: this.root,
      env: { PATH: this.config.toolPath, HOME: path.join(this.root, 'runtime', 'home'), FAKE_MODEL_PORT: String(this.ports[2]) } },
    'fake-model listening', record, signal);
    if (this.config.fakeGitHub) await this.start('fake-github', { executable: this.config.nodeBinary,
      argv: [path.join(this.config.loom.source.root, 'tests', 'aft', 'fixtures', 'fake-github', 'server.mjs')], cwd: this.root,
      env: { PATH: this.config.toolPath, HOME: path.join(this.root, 'runtime', 'home'), FAKE_GH_PORT: String(this.ports[4]) } },
    'fake-github listening', record, signal);
    const apiOrigin = `http://127.0.0.1:${this.ports[0]}`; const filesOrigin = `http://127.0.0.1:${this.ports[1]}`;
    await this.start('serve', { executable: this.config.loomBinary,
      argv: ['serve', '--bind', '127.0.0.1', '--port', String(this.ports[0]), '--frontend-url', filesOrigin, '--frontend-url', `http://localhost:${this.ports[1]}`],
      cwd: this.workspaceRoot, env: this.env() }, `Server starting on 127.0.0.1:${this.ports[0]}`, record, signal);
    check((await this.http(apiOrigin, 'GET', '/api/config', null, signal)).status === 200, 'observation-failed');
    await this.prepareObserve(signal);
    for (const [name, repo] of [['e2e-ws-2', second], ['e2e-ws', this.workspaceRoot]] as const) {
      const store=this.workspaceRecords?await this.workspaceStoreIdentity(signal):undefined;
      const created = await this.requestOwnedHttp('api', 'POST', '/api/workspaces', { name, type: 'empty', repos: [repo] }, signal);
      check(created.status === 200 || created.status === 201, 'identity-mismatch');
      if(this.workspaceRecords)await this.workspaceRecords.captureCreated(name.toUpperCase(),repo,created,store!,signal);
    }
    const workspaces = await this.http(apiOrigin, 'GET', '/api/workspaces/E2E-WS', null, signal);
    const data = (workspaces.body as { data?: { id: string; repos: { path: string }[] } }).data;
    check(workspaces.status === 200 && data?.id === 'E2E-WS' && data.repos.length===1&&data.repos[0]!.path.startsWith(path.join(this.root,'runtime')+path.sep), 'identity-mismatch');
    const frontend = path.join(this.config.loom.source.root, 'internal', 'webui', 'frontend');
    await this.start('frontend', { executable: this.config.nodeBinary,
      argv: [path.join(frontend, 'node_modules', 'vite', 'bin', 'vite.js'), 'preview', '--outDir', this.renderer!.buildRoot, '--port', String(this.ports[1]), '--strictPort', '--host', '127.0.0.1'],
      cwd: frontend, env: { PATH: this.config.toolPath, HOME: path.join(this.root, 'runtime', 'home'), E2E_API_URL: apiOrigin } },
    `http://127.0.0.1:${this.ports[1]}`, record, signal);
    check((await this.http(filesOrigin, 'GET', '/api/config', null, signal)).status === 200, 'observation-failed');
    if (this.config.daemon) await this.start('daemon', { executable: this.config.loomBinary, argv: ['daemon'], cwd: this.workspaceRoot,
      env: { ...this.env(), LOOM_WORKSPACE: 'E2E-WS', LOOM_SERVER_URL: apiOrigin, LOOM_FLEET_DB_ACTOR: 'loom-aft-daemon' } },
    'Loom Agent Supervisor', record, signal);
    return { apiOrigin, filesOrigin, workspaceId: 'E2E-WS', repo: data!.repos[0]!.path };
  }
  async inspect(resource: Resource): Promise<Inventory> {
    if (resource.kind === 'process') {
      if(this.descendants?.has(resource.id)){const identity=await this.descendants.inspect(resource.id,resource.generation);return {complete:true,owned:true,services:[{id:resource.id,pid:identity.pid,generation:identity.generation,state:identity.state}]};}
      if (this.unspawned.get(resource.id) === resource.generation) return { complete: true, owned: true, services: [] };
      const handle = this.handles.get(resource.id); check(handle && handle.generation === resource.generation);
      return { complete: true, owned: true, services: [{ id: resource.id, pid: handle!.pid, generation: handle!.generation, state: handle!.state() }] };
    }
    if (resource.kind === 'ports') return { complete: true, owned: resource.generation === this.leaseId && this.ports.length > 0, services: [] };
    const stamp = this.stamps.get(resource.id); const stat = await this.files.lstat(resource.id);
    check(stamp && !stat.isSymbolicLink() && stamp.dev === stat.dev && stamp.ino === stat.ino && resource.generation === `${stat.dev}:${stat.ino}`);
    if (resource.kind === 'lock') check(resource.id === this.lock?.path && await this.files.readFile(resource.id, 'utf8') === this.lock.contents);
    return { complete: true, owned: true, services: [] };
  }
  async remove(resource: Resource) {
    this.requireWorkerRegistrationIdle();
    return this.withServiceOperation('cleanup-resource',async()=>{
    await this.inspect(resource);
    await this.drainCleanups();
    if (resource.kind === 'process') { if (!this.unspawned.has(resource.id)) await this.stopOwnedProcess(resource.id,resource.generation,new AbortController().signal); return; }
    if (resource.kind === 'ports') { await this.closeSockets(); return; }
    if (resource.kind === 'lock') { await this.files.unlink(resource.id); return; }
    check([...this.handles.values()].every(handle => handle.state() === 'exited'));
    const runtime = path.join(this.root, 'runtime'); const stamp = this.stamps.get(runtime); const stat = await this.files.lstat(runtime);
    check(stamp && !stat.isSymbolicLink() && stat.dev === stamp.dev && stat.ino === stamp.ino);
    await this.files.rm(runtime, { recursive: true });this.runtimeRemoved=true;
    });
  }
  async artifact(kind: 'acquire' | 'release' | 'observe' | 'failure', value: unknown): Promise<Artifact> {
    const directory = path.join(this.root, 'evidence'); const stamp = this.stamps.get(directory); const stat = await this.files.lstat(directory);
    check(stamp && !stat.isSymbolicLink() && stat.dev === stamp.dev && stat.ino === stamp.ino && await this.files.realpath(directory) === directory);
    const bytes = Buffer.from(JSON.stringify({ kind, value })); const id = path.join(directory, `${kind}-${this.uuid()}.json`);
    await this.files.writeFile(id, bytes, { mode: 0o600, flag: 'wx' });
    return { id, bytes: bytes.length, sha256: hash(bytes), mediaType: 'application/json', redaction: 'sanitized' };
  }
}
