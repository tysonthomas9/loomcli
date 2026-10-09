import { spawn } from 'node:child_process';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { FixtureError, type Artifact, type FixtureDriver, type FixturePlan, type Inventory, type Resource, type Revision } from './lifecycle.js';
import { reservePort, type PortReservation } from './process.js';
import type { ContainerRead } from './container-read.js';
import { readHttp, type Http } from './host.js';
import { Json, redact } from '../protocol.js';
import { redactionFacts } from '../redaction.js';
import { prepareRenderer, type PreparedRenderer } from './renderer.js';
import { fixtureRouting, fixtureOperationAuthority } from './routing.js';
import type { FixtureAuthorityOwner } from '../authority.js';
import { IssuedArtifactReader } from './issued-artifacts.js';
import { ArchiveAgentRequest,ArchiveAgentFacts,assertArchiveAgentTarget } from '../agent-archive.js';
import { createNativeArchiveHttp,type NativeArchiveHttp } from './native-archive-http.js';

const hash = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
const check = (condition: unknown, code: FixtureError['code'] = 'ownership-mismatch') => { if (!condition) throw new FixtureError(code); };
const safe = (value: string) => /^[A-Za-z0-9][A-Za-z0-9._/-]*$/.test(value);
export interface ProcessRequest { binary: 'git' | 'podman' | 'bash'; args: string[]; cwd: string; env: Record<string, string>; signal?: AbortSignal }
export type ProcessRunner = (request: ProcessRequest) => Promise<string>;
// stdout is consumed privately as protocol data. stderr and raw subprocess
// exceptions are deliberately discarded: they can contain credentials.
export const runProcess: ProcessRunner = request => new Promise((resolve, reject) => {
  const child = spawn(request.binary, request.args, { cwd: request.cwd, env: request.env, signal: request.signal,
    stdio: ['ignore', 'pipe', 'ignore'], shell: false });
  const chunks: Buffer[] = [];
  let bytes = 0;
  child.stdout.on('data', (chunk: Buffer) => {
    bytes += chunk.length;
    if (bytes > 4 * 1024 * 1024) { child.kill(); return; }
    chunks.push(chunk);
  });
  child.on('error', () => reject(new FixtureError('observation-failed')));
  child.on('close', code => code === 0 && bytes <= 4 * 1024 * 1024 ? resolve(Buffer.concat(chunks).toString('utf8')) : reject(new FixtureError('observation-failed')));
});
export interface FileManifest { root: string; entries: { relativePath: string; sha256: string }[] }
export interface RegisteredBuild {
  revision: Revision; source: FileManifest; build: FileManifest;
}
export interface ProductionConfig {
  loom: RegisteredBuild; fleet: RegisteredBuild; engine: RegisteredBuild; adapter: RegisteredBuild;
  tempParent: string; lockParent: string; hostHome: string; toolPath: string;
  connection: string; connectionFingerprint: string;
  // Linux binary supplied by an attested runner build, never by YAML.
  emulatorBinary?: { path: string; sha256: string };
  // Each container-image.json is itself covered by the corresponding build
  // manifest and contains { imageId, sourceManifestSha256 }. Acquisition never
  // rebuilds an unproven image or silently uses a mutable shared tag.
  attestedImages: boolean;
  minimumFreeBytes: number;
  // Separate ModeCloud topology, backed by the reviewed supplemental overlay.
  modecloud?: { codexAuthRoot: string; frontendDist: string };
  // Existing source-launcher RUN_ID, supplied only by trusted provisioning.
  fixtureRunId?:string;
}
export type ComposeRead = (origin: string, relative: string, signal: AbortSignal) => Promise<unknown>;
// Fixed internal readiness port: no caller path, predicate or executable.
export type ComposeReadiness = (origin:string,signal:AbortSignal)=>Promise<{status:number;complete:true}>;
export interface ReadinessClock { now():number; monotonicNow?():number; nextAttempt(signal:AbortSignal):Promise<void>; }
const readinessClock:ReadinessClock={now:()=>Date.now(),monotonicNow:()=>performance.now(),nextAttempt:signal=>new Promise<void>((resolve,reject)=>{
  signal.throwIfAborted();
  const abort=()=>{clearTimeout(timer);reject(new FixtureError('observation-failed'));};
  const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},1000);
  signal.addEventListener('abort',abort,{once:true});
})};
const fetchReadiness:ComposeReadiness=async(origin,signal)=>{
  const response=await fetch(new URL('/api/config',origin),{signal,redirect:'manual'});
  // curl -fsS consumes the body, but does not require JSON or follow redirects.
  const reader=response.body?.getReader();let bytes=0;
  try{if(reader)for(;;){const chunk=await reader.read();if(chunk.done)break;bytes+=chunk.value.byteLength;
    check(bytes<=4*1024*1024,'observation-failed');}}
  finally{if(reader){await reader.cancel();reader.releaseLock();}}
  signal.throwIfAborted();return {status:response.status,complete:true};
};
const fetchRead: ComposeRead = async (origin, relative, signal) => {
  check(relative.startsWith('/api/') && !relative.startsWith('//') && !relative.includes('\\'));
  const response = await fetch(new URL(relative, origin), { signal, redirect: 'error' });
  check(response.ok, 'observation-failed');
  const text = await response.text(); check(Buffer.byteLength(text) <= 4 * 1024 * 1024, 'observation-failed');
  return JSON.parse(text);
};

async function regularBytes(filename: string): Promise<Buffer> {
  check(path.isAbsolute(filename));
  let ancestor = path.parse(filename).root;
  for (const component of filename.slice(ancestor.length).split('/')) {
    ancestor = path.join(ancestor, component);
    check(!(await fs.lstat(ancestor)).isSymbolicLink());
  }
  const file = await fs.open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const before = await file.stat();
    check(before.isFile() && before.size <= 64 * 1024 * 1024, 'source-mismatch');
    const bytes = await file.readFile();
    const after = await file.stat();
    const named = await fs.lstat(filename);
    check(bytes.length === before.size && before.size === after.size && before.mtimeMs === after.mtimeMs && before.ctimeMs === after.ctimeMs &&
      before.ino === named.ino && before.dev === named.dev, 'source-mismatch');
    return bytes;
  } finally { await file.close(); }
}
export async function verifyManifest(manifest: FileManifest, expected: string, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  check(await fs.realpath(manifest.root) === manifest.root, 'source-mismatch');
  signal?.throwIfAborted();
  check(manifest.entries.length > 0 && new Set(manifest.entries.map(entry => entry.relativePath)).size === manifest.entries.length, 'source-mismatch');
  const entries = [...manifest.entries].sort((a, b) => a.relativePath < b.relativePath ? -1 : a.relativePath > b.relativePath ? 1 : 0);
  for (const entry of entries) {
    signal?.throwIfAborted();
    check(entry.relativePath.split('/').every(part => part && part !== '.' && part !== '..') && !path.isAbsolute(entry.relativePath) &&
      !entry.relativePath.includes('\\') && /^[a-f0-9]{64}$/.test(entry.sha256), 'source-mismatch');
    check(hash(await regularBytes(path.join(manifest.root, entry.relativePath))) === entry.sha256, 'source-mismatch');
    signal?.throwIfAborted();
  }
  check(hash(entries.map(entry => `${entry.sha256}  ${entry.relativePath}\n`).join('')) === expected, 'source-mismatch');
}
const sameImageId = (observed: unknown, expected: string) => typeof observed === 'string' && /^(?:sha256:)?[a-f0-9]{64}$/.test(observed) && observed.replace(/^sha256:/, '') === expected.replace(/^sha256:/, '');
interface ObjectRecord { id: string; kind: 'container' | 'volume' | 'network'; generation: string; service: string; pid: number; state: string; startedAt?:string; healthy?: boolean; workVolume?: string; namespaceSha256?:string;networkIds?:readonly string[] }
const SERVICES = ['redis', 'fleet-db', 'loom-local', 'ui-local'];
const CLOUD_SERVICES = ['redis', 'fleet-auth-seed', 'fleet-db', 'loom-serve', 'worker', 'stub-upstream'];

export class ComposeFixtureDriver implements FixtureDriver {
  private root = '';
  private leaseId = '';
  private provisionedRunId='';
  private project = '';
  private profile = '';
  private ports: number[] = [];
  private sockets: PortReservation[] = [];
  private readonly stamps = new Map<string, { dev: number; ino: number }>();
  private readonly locks = new Map<string, string>();
  private objects: ObjectRecord[] = [];
  private retryRestartAttestation?: (signal:AbortSignal)=>Promise<void>;
  private operationActive=false;
  private readonly restartAttempts=new Set<string>();
  private async withContainerOperation<T>(operation:()=>Promise<T>):Promise<T>{
    check(!this.operationActive,'identity-mismatch');this.operationActive=true;
    try{return await operation();}finally{this.operationActive=false;}
  }
  private async ownedContainer(signal:AbortSignal,expectedGeneration?:string){
    signal.throwIfAborted();await this.inspect({id:this.project,kind:'compose',generation:this.leaseId});signal.throwIfAborted();
    const containers=this.objects.filter(object=>object.kind==='container'&&object.service===(this.cloud?'loom-serve':'loom-local'));
    check(containers.length===1&&containers[0]!.state==='running');const container=containers[0]!;
    check(expectedGeneration===undefined||container.generation===expectedGeneration,'identity-mismatch');return {...container};
  }
  private async verifyContainer(container:ObjectRecord,signal:AbortSignal){
    const current=await this.ownedContainer(signal,container.generation);
    check(current.id===container.id&&current.pid===container.pid&&current.namespaceSha256===container.namespaceSha256,'identity-mismatch');
  }
  private attempted = false;
  private removed = false;
  private plan?: FixturePlan;
  private discoveredWorkspace?:Readonly<{id:string;repo:string}>;
  private images = { loom: '', fleet: '' };
  private stackImages: Record<string, string> = {};
  private readonly cloudSecrets: Record<string, string> = {};
  private renderer?: PreparedRenderer;
  private readonly config: ProductionConfig;
  private readonly cleanups: (() => Promise<void>)[] = [];
  private issuedArtifacts?:IssuedArtifactReader;
  constructor(config: ProductionConfig, private readonly run: ProcessRunner = runProcess,
    private readonly files: typeof fs = fs, private readonly uuid: () => string = randomUUID,
    private readonly reserve: () => Promise<PortReservation> = reservePort,
    private readonly http: ComposeRead = fetchRead, private readonly requestHttp: Http = readHttp,
    private readonly readiness:ComposeReadiness=fetchReadiness,private readonly readinessTime:ReadinessClock=readinessClock,
    private readonly archiveTimeout:(milliseconds:number)=>AbortSignal=milliseconds=>AbortSignal.timeout(milliseconds),
    private readonly archiveHttp:NativeArchiveHttp=createNativeArchiveHttp(fetch,archiveTimeout)) { this.config = structuredClone(config); }
  get runtimeRoot() { return this.root; }
  get workspaceRoot() { return '/root/.loom/workspaces/LOCALMODE'; }
  get fixtureSecrets() { return Object.values(this.cloudSecrets); }
  get executionRouting() { check(this.plan);return fixtureRouting(this.plan!); }
  createOperationAuthority(owner:FixtureAuthorityOwner) {check(this.plan);return fixtureOperationAuthority(owner,this.plan!);}
  async rendererRuntimeTarget(signal:AbortSignal) {
    signal.throwIfAborted(); await this.inspect({id:this.project,kind:'compose',generation:this.leaseId});
    const target=this.objects.find(object=>object.kind==='container'&&object.service===(this.cloud?'loom-serve':'ui-local'));
    check(target?.state==='running'&&this.renderer,'identity-mismatch');
    return {targetId:target!.id,generation:target!.generation,buildRoot:this.renderer!.buildRoot};
  }
  private get cloud() { return this.profile === 'legacy-real-codex-podman'; }
  private get serviceNames() { return this.cloud ? CLOUD_SERVICES : SERVICES; }
  enrollCleanup(cleanup: () => Promise<void>) { check(this.root); this.cleanups.push(cleanup); }
  async runtimeIdentity(signal:AbortSignal){
    const value=await this.nativeRead({operation:'runtime-identity'},signal) as {runId?:unknown;leaseId?:unknown};
    check(value.runId===this.provisionedRunId&&value.leaseId===this.leaseId,'identity-mismatch');
    return {fixtureRunId:this.provisionedRunId};
  }
  async readOwnedConfiguration(target: 'opencode' | 'emu-scenarios', signal: AbortSignal) {
    return this.nativeRead({ operation: 'configuration-read', target }, signal) as Promise<{ bytes: string | null; complete: true }>;
  }
  async writeOwnedConfiguration(target: 'opencode' | 'emu-scenarios', bytes: string | null, signal: AbortSignal) {
    check(bytes === null || Buffer.byteLength(bytes) <= 65536);
    await this.nativeRead({ operation: 'configuration-write', target, bytes }, signal);
  }
  async requestOwnedHttp(target: 'api' | 'fake-model' | 'fake-github', method: Parameters<Http>[1], relativePath: string, body: unknown, signal: AbortSignal,expectedGeneration?:string) {
    signal.throwIfAborted();check(this.ports.length===3);check(target!=='fake-github','unsupported-capability');
    if(target==='fake-model'){
      // Container identity alone cannot attest the separately restartable model
      // process. Until its product registration port is bound, retained model
      // generations must fail before any HTTP mutation.
      check(!this.cloud&&expectedGeneration===undefined&&method==='GET'&&/^\/__requests(\?|$)/.test(relativePath),'unsupported-capability');
      return this.nativeRead({operation:'fixture-http',method:'GET',relativePath,body:Json.parse(body)},signal) as Promise<{status:number;body:unknown}>;
    }
    check(relativePath.startsWith('/api/'));
    return this.withContainerOperation(async()=>{
      const container=await this.ownedContainer(signal,expectedGeneration);
      const origin=`http://127.0.0.1:${this.ports[this.cloud?0:1]}`;
      const response=await this.requestHttp(origin,method,relativePath,body,signal);
      await this.verifyContainer(container,signal);return response;
    });
  }
  /** GF1's public cleanup actor: current GET and fixed archive POST only.
   * Acquisition discovery is not an agent-creation or native-store receipt.
   * Exact workspace/repo/harness and managed-origin checks are stronger than
   * the source cleanup's current-name prefix guard. Redirects are denied by
   * the fixed transports; direct-route parity still requires the paired run. */
  async archiveAgent(input:ArchiveAgentRequest,signal:AbortSignal):Promise<ArchiveAgentFacts>{
    signal.throwIfAborted();check(this.profile==='agents-real-opencode','unsupported-capability');
    const request=ArchiveAgentRequest.parse(input),workspace=this.discoveredWorkspace;
    check(workspace&&request.agent.fixtureLeaseId===this.leaseId&&request.agent.workspaceId===workspace.id&&
      request.expectedRepo===workspace.repo,'identity-mismatch');
    return this.withContainerOperation(async()=>{
      const container=await this.ownedContainer(signal);
      const origin=`http://127.0.0.1:${this.ports[1]}`;
      const bounded=AbortSignal.any([signal,this.archiveTimeout(15000)]);bounded.throwIfAborted();
      const route=`/api/workspaces/${encodeURIComponent(workspace!.id)}/v1/agents/${encodeURIComponent(request.agent.agentId)}`;
      const readback=await this.requestHttp(origin,'GET',route,undefined,bounded);
      bounded.throwIfAborted();check(readback.status>=200&&readback.status<300,'observation-failed');
      const observed=assertArchiveAgentTarget(request,readback.body);
      await this.verifyContainer(container,signal);bounded.throwIfAborted();
      const status=await this.archiveHttp(origin,request.agent,request.idempotencyKey,signal);
      await this.verifyContainer(container,signal);
      return ArchiveAgentFacts.parse({agent:request.agent,observed,status,idempotencyKey:request.idempotencyKey,
        body:{cancel:true},requestTimeoutMs:15000,responseJsonParsed:false});
    });
  }
  private env(): Record<string, string> {
    const c = this.config;
    if (this.cloud) return { PATH: c.toolPath, HOME: c.hostHome, CONTAINER_CONNECTION: c.connection,
      LOOM_STACK_PROJECT: this.project, LOOM_STACK_SERVE_PORT: String(this.ports[0]), LOOM_STACK_FLEET_DB_PORT: String(this.ports[1]),
      LOOM_STACK_STUB_PORT: String(this.ports[2]), LOOM_STACK_WORKSPACE: 'E2E-WS', FLEET_SEED_ACTOR: 'loom-serve@podman-stack.local',
      LOOM_STACK_CODEX_HOST_DIR: c.modecloud!.codexAuthRoot, LOOM_AFT_FRONTEND_DIST: c.modecloud!.frontendDist,
      LOOM_AFT_FLUE_AGENT_MODEL: this.plan!.model, ...this.cloudSecrets };
    return { PATH: c.toolPath, HOME: c.hostHome, CONTAINER_CONNECTION: c.connection,
      LOCAL_MODE_COMPOSE_PROJECT: this.project, LOCAL_MODE_FLEETDB_PORT: String(this.ports[0]),
      LOCAL_MODE_API_PORT: String(this.ports[1]), LOCAL_MODE_UI_PORT: String(this.ports[2]),
      LOCAL_MODE_STATE_DIR: path.join(this.root, 'state'),
      LOCAL_MODE_OPENCODE_COPY: path.join(this.root, 'state', this.project, 'opencode.db'),
      LOCAL_MODE_CODEX_AUTH: '/dev/null', LOCAL_MODE_CLAUDE_AUTH: '/dev/null', LOCAL_MODE_CLAUDE_TOKEN_FILE: '/dev/null',
      ...(this.profile === 'agents-real-opencode' ? { LOCAL_MODE_AGENTS_REAL: '1', LOCAL_MODE_AGENTS_MODEL: this.plan!.model } : {}),
      LOCAL_MODE_LOOM_AGENTS_IMAGE: this.images.loom, LOCAL_MODE_FLEETDB_IMAGE: this.images.fleet };
  }
  private async command(binary: ProcessRequest['binary'], args: string[], signal?: AbortSignal) {
    signal?.throwIfAborted();
    const result = await this.run({ binary, args, cwd: this.config.loom.source.root, env: this.env(), signal });
    signal?.throwIfAborted(); return result;
  }
  private async connection(signal?: AbortSignal) {
    const records = JSON.parse(await this.command('podman', ['system', 'connection', 'list', '--format', 'json'], signal)) as { Name: string; URI: string; Identity: string }[];
    const selected = records.filter(entry => entry.Name === this.config.connection);
    check(selected.length === 1);
    const selectedRecord = selected[0]!;
    // Same canonical field ordering as the pinned ownership helper's jq -cS.
    check(hash(JSON.stringify([{ Identity: selectedRecord.Identity, Name: selectedRecord.Name, URI: selectedRecord.URI }])) === this.config.connectionFingerprint);
  }
  private composeArgs(): string[] {
    if (this.cloud) return ['--connection', this.config.connection, 'compose', '-p', this.project,
      '-f', 'deploy/podman-stack/compose.yaml', '-f', path.join(this.root, 'compose.json')];
    return ['--connection', this.config.connection, 'compose', '-p', this.project,
      '-f', 'test/local-mode/docker-compose.yml', '-f', 'test/local-mode/docker-compose.agents.yml',
      ...(this.profile === 'agents-real-opencode' ? ['-f', 'test/local-mode/docker-compose.agents-real.yml'] : []),
      '-f', path.join(this.root, 'compose.json')];
  }
  async identity(plan: FixturePlan, signal?: AbortSignal): Promise<boolean> {
    try {
      signal?.throwIfAborted();
      for (const [build, expected] of [[this.config.loom, plan.loomRevision], [this.config.fleet, plan.fleetRevision],
        [this.config.engine, plan.engineRevision], [this.config.adapter, plan.adapterRevision]] as const) {
        check(JSON.stringify(build.revision) === JSON.stringify(expected), 'source-mismatch');
        const git = async (args: string[]) => {
          signal?.throwIfAborted();
          const value = await this.run({ binary: 'git', args, cwd: build.source.root, env: { PATH: this.config.toolPath }, signal });
          signal?.throwIfAborted(); return value;
        };
        const head = (await git(['rev-parse', 'HEAD'])).trim();
        const tree = (await git(['rev-parse', 'HEAD^{tree}'])).trim();
        const dirty = await git(['status', '--porcelain', '--untracked-files=no']);
        check(head === expected.commit && tree === expected.tree && !dirty.trim(), 'source-mismatch');
        const tracked: string[] = [];
        for (const record of (await git(['ls-files', '-s', '-z'])).split('\0').filter(Boolean)) {
          const parsed = /^(100644|100755|120000) ([a-f0-9]{40}) 0\t(.+)$/.exec(record);
          check(parsed, 'source-mismatch');
          const [, mode, oid, relativePath] = parsed!;
          check(relativePath!.split('/').every(part => part && part !== '.' && part !== '..') && !path.isAbsolute(relativePath!), 'source-mismatch');
          if (mode === '120000') {
            const filename = path.join(build.source.root, relativePath!);
            check((await this.files.lstat(filename)).isSymbolicLink(), 'source-mismatch');
            const target = await this.files.readlink(filename);
            check(target === await git(['cat-file', 'blob', oid!]), 'source-mismatch');
          } else tracked.push(relativePath!);
        }
        check(JSON.stringify(tracked.sort()) === JSON.stringify(build.source.entries.map(entry => entry.relativePath).sort()), 'source-mismatch');
        await verifyManifest(build.source, expected.sourceManifestSha256, signal);
        await verifyManifest(build.build, expected.buildManifestSha256, signal);
      }
      return true;
    } catch { return false; }
  }
  async preflight(plan: FixturePlan, signal: AbortSignal): Promise<void> {
    check(this.config.fixtureRunId===undefined||/^[A-Za-z0-9_-]{1,128}$/.test(this.config.fixtureRunId)&&(!plan.profile.startsWith('agents-')||/^af[a-z0-9]{8}$/.test(this.config.fixtureRunId)),'identity-mismatch');
    check(['agents-real-opencode', 'agents-emulator', 'legacy-real-codex-podman'].includes(plan.profile), 'unsupported-capability');
    this.plan = structuredClone(plan); this.profile = plan.profile;
    fixtureRouting(plan);
    check(safe(plan.model) && (plan.profile === 'agents-emulator' ? plan.model === 'aft/m' : !plan.model.startsWith('aft/')), 'identity-mismatch');
    check(safe(this.config.connection) && /^[a-f0-9]{64}$/.test(this.config.connectionFingerprint));
    for (const directory of [this.config.tempParent, this.config.lockParent, this.config.hostHome]) {
      check(await this.files.realpath(directory) === directory && (await this.files.lstat(directory)).isDirectory());
    }
    const storage = await this.files.statfs(this.config.tempParent);
    check(storage.bavail * storage.bsize >= Math.max(9 * 1024 ** 3, this.config.minimumFreeBytes), 'observation-failed');
    check(await this.identity(plan), 'source-mismatch');
    this.renderer=await prepareRenderer(this.config.loom);
    check(this.config.attestedImages, 'source-mismatch');
    if (this.cloud) {
      const config = this.config.modecloud; check(config, 'source-mismatch');
      check(config!.frontendDist===this.renderer.buildRoot,'source-mismatch');
      for (const dir of [config!.codexAuthRoot, config!.frontendDist]) check(await this.files.realpath(dir) === dir && (await this.files.lstat(dir)).isDirectory());
      check((await this.files.lstat(path.join(config!.codexAuthRoot, 'auth.json'))).isFile() && !(await this.files.lstat(path.join(config!.codexAuthRoot, 'auth.json'))).isSymbolicLink());
      const prefix = path.relative(this.config.loom.build.root, config!.frontendDist);
      check(prefix && !prefix.startsWith('..') && !path.isAbsolute(prefix) && this.config.loom.build.entries.some(entry => entry.relativePath === `${prefix}/index.html`), 'source-mismatch');
      check(this.config.loom.build.entries.some(entry => entry.relativePath === 'modecloud-images.json'), 'source-mismatch');
      const receipt = JSON.parse((await regularBytes(path.join(this.config.loom.build.root, 'modecloud-images.json'))).toString('utf8'));
      check(receipt.sourceManifestSha256 === plan.loomRevision.sourceManifestSha256 && receipt.fleetSourceManifestSha256 === plan.fleetRevision.sourceManifestSha256, 'source-mismatch');
      for (const service of ['fleet-db', 'loom-serve', 'worker', 'stub-upstream']) {
        check(/^sha256:[a-f0-9]{64}$/.test(receipt.images?.[service]), 'source-mismatch'); this.stackImages[service] = receipt.images[service];
      }
      this.images = { loom: this.stackImages['loom-serve']!, fleet: this.stackImages['fleet-db']! };
    } else for (const [name, build] of [['loom', this.config.loom], ['fleet', this.config.fleet]] as const) {
      check(build.build.entries.some(entry => entry.relativePath === 'container-image.json'), 'source-mismatch');
      const receipt = JSON.parse((await regularBytes(path.join(build.build.root, 'container-image.json'))).toString('utf8'));
      check(/^sha256:[a-f0-9]{64}$/.test(receipt.imageId) && receipt.sourceManifestSha256 === build.revision.sourceManifestSha256, 'source-mismatch');
      this.images[name] = receipt.imageId;
    }
    if (plan.profile === 'agents-emulator') {
      const binary = this.config.emulatorBinary;
      check(binary && hash(await regularBytes(binary.path)) === binary.sha256, 'source-mismatch');
      const elf = await regularBytes(binary!.path);
      check(elf.subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])), 'source-mismatch');
    }
    await this.connection(signal);
    await this.command('podman', ['--connection', this.config.connection, 'info', '--format', 'json'], signal);
    for (const imageId of Object.values(this.cloud ? this.stackImages : this.images)) {
      const observed = JSON.parse(await this.command('podman', ['--connection', this.config.connection, 'image', 'inspect', imageId], signal));
      check(Array.isArray(observed) && observed.length === 1 && sameImageId(observed[0].Id, imageId), 'source-mismatch');
    }
  }
  private async stamp(filename: string) {
    const stat = await this.files.lstat(filename);
    check(!stat.isSymbolicLink());
    this.stamps.set(filename, { dev: stat.dev, ino: stat.ino });
    return `${stat.dev}:${stat.ino}`;
  }
  async allocate(leaseId: string, _runId: string, record: (resource: Resource) => void): Promise<void> {
    this.leaseId = leaseId;
    this.provisionedRunId=this.config.fixtureRunId??`af${randomBytes(4).toString('hex')}`;
    check(/^[A-Za-z0-9_-]{1,128}$/.test(this.provisionedRunId),'identity-mismatch');
    check(!this.profile.startsWith('agents-')||/^af[a-z0-9]{8}$/.test(this.provisionedRunId),'identity-mismatch');
    this.project = `loom-aft-${this.uuid().replace(/-/g, '')}`;
    check(/^[a-z0-9-]+$/.test(this.project));
    this.root = await this.files.mkdtemp(path.join(this.config.tempParent, 'loom-aft-fixture-'));
    record({ id: this.root, kind: 'directory', generation: await this.stamp(this.root) });
    await this.files.chmod(this.root, 0o700);
    await this.files.mkdir(path.join(this.root, 'evidence'), { mode: 0o700 });
    await this.stamp(path.join(this.root, 'evidence'));
    const rootStamp=this.stamps.get(this.root)!,evidenceStamp=this.stamps.get(path.join(this.root,'evidence'))!;
    this.issuedArtifacts=new IssuedArtifactReader({path:this.root,device:rootStamp.dev,inode:rootStamp.ino},
      {path:path.join(this.root,'evidence'),device:evidenceStamp.dev,inode:evidenceStamp.ino},
      cleanup=>this.enrollCleanup(cleanup),this.files,()=>this.fixtureSecrets);
    // Account and build locks are exclusive and never steal stale locks.
    const names = [ ...(this.profile === 'agents-real-opencode' ? ['aft-live.opencode.lock'] : this.cloud ? ['aft-live.codex.lock'] : []), 'aft-fixture-build.lock' ];
    for (const name of names) {
      const filename = path.join(this.config.lockParent, name);
      const handle = await this.files.open(filename, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
      const content = this.uuid(); this.locks.set(filename, content);
      record({ id: filename, kind: 'lock', generation: await this.stamp(filename) });
      try { await handle.writeFile(content); } finally { await handle.close(); }
    }
    for (let index = 0; index < 3; index++) {
      const socket = await this.reserve(); this.sockets.push(socket); this.ports.push(socket.port);
      if (index === 0) record({ id: `ports:${this.leaseId}`, kind: 'ports', generation: this.leaseId });
    }
    const labels = { 'io.loom.aft.lease': this.leaseId };
    const override = this.cloud ? this.cloudOverride(labels) : { services: Object.fromEntries(SERVICES.map(service => [service, {
      labels,
      ...(service === 'fleet-db' ? { build: { context: this.config.fleet.source.root }, environment: { FLEET_RATE_LIMIT_ENABLED: 'false' } } : {}),
      ...(service === 'loom-local' ? { environment: {RUN_ID:this.provisionedRunId,AFT_FIXTURE_NAMESPACE:'owned-container',AFT_FIXTURE_LEASE_ID:this.leaseId}, volumes: [`${this.config.adapter.build.root}:/opt/aft:ro`] } : {}),
      ...(service === 'ui-local' ? { volumes: [`${this.renderer!.buildRoot}:/srv:ro`] } : {}),
      ...(service === 'loom-local' && this.profile === 'agents-emulator' ? {
        environment: {RUN_ID:this.provisionedRunId,AFT_FIXTURE_NAMESPACE:'owned-container',AFT_FIXTURE_LEASE_ID:this.leaseId, LOOM_OPENCODE_BIN: '/opt/fixture/loom-harness-emu', LOOM_HARNESS_EMU: '1',
          LOOM_HARNESS_EMU_MODEL: 'http://127.0.0.1:4010/v1', LOOM_HARNESS_EMU_SCENARIOS: '/root/.loom/agents-opencode/emu-scenarios.json' },
        volumes: [`${this.config.adapter.build.root}:/opt/aft:ro`, `${this.config.emulatorBinary!.path}:/opt/fixture/loom-harness-emu:ro`],
      } : {}),
    }])), volumes: Object.fromEntries(['redis-data', 'loom-data', 'loom-workspace'].map(name => [name, { labels }])),
    networks: { 'local-mode': { labels } } };
    const filename = path.join(this.root, 'compose.json');
    await this.files.writeFile(filename, JSON.stringify(override), { mode: 0o600, flag: 'wx' });
    await this.stamp(filename);
  }
  private cloudOverride(labels: Record<string, string>) {
    for (const key of ['LOOM_FLEET_DB_API_KEY', 'LOOM_RUN_TOKEN_SIGNING_KEY', 'LOOM_CONNECTOR_VAULT_KEY', 'LOOM_FLEET_API_KEY', 'LOOM_WORKER_TOKEN', 'LOOM_STACK_STUB_SECRET'])
      this.cloudSecrets[key] = (key === 'LOOM_FLEET_DB_API_KEY' ? 'fldb_' : '') + randomBytes(32).toString(key === 'LOOM_CONNECTOR_VAULT_KEY' ? 'base64' : 'hex');
    return { services: Object.fromEntries(CLOUD_SERVICES.map(service => [service, { labels,
      ...(this.stackImages[service] ? { image: this.stackImages[service] } : {}),
      ...(service === 'fleet-db' ? { environment: { FLEET_AUTH_DEV_MODE: 'true', FLEET_AUTHZ_ENABLED: 'false' } } : {}),
      ...(service === 'loom-serve' ? { environment: { CODEX_HOME: '/home/node/.codex-rw', LOOM_STACK_CODEX_RW_DIR: '/home/node/.codex-rw',
        FLUE_REPO: '/opt/flue', RUN_ID:this.provisionedRunId, AFT_FIXTURE_NAMESPACE:'owned-container', AFT_FIXTURE_LEASE_ID:this.leaseId, AFT_FIXTURE_MODE: 'modecloud', LOOM_DRIVER_TASK_RUNNER_CMD_JSON: null, LOOM_FLUE_AGENT_MODEL: this.plan!.model,
        LOOM_FRONTEND_DIR: '/opt/webui', LOOM_FRONTEND_URL: `http://localhost:${this.ports[0]}` },
        volumes: [`${this.config.modecloud!.codexAuthRoot}:/home/node/.codex:ro`, `${this.config.modecloud!.frontendDist}:/opt/webui:ro`,
          `${this.config.adapter.build.root}:/opt/aft:ro`] } : {}),
    }])), volumes: Object.fromEntries(['redis-data', 'loom-data', 'loom-work'].map(name => [name, { labels }])), networks: { 'loom-stack': { labels } } };
  }
  private async closeSockets() {
    for (const socket of this.sockets) await socket.release();
    this.sockets = [];
  }
  private async inventory(signal?: AbortSignal): Promise<ObjectRecord[]> {
    await this.connection(signal);
    const records: ObjectRecord[] = [];
    for (const kind of ['container', 'volume', 'network'] as const) {
      const listArgs = kind === 'container' ? ['ps', '-a', '--filter', `label=com.docker.compose.project=${this.project}`, '--format', '{{.ID}}'] :
        [kind, 'ls', '--filter', `label=com.docker.compose.project=${this.project}`, '--format', kind === 'volume' ? '{{.Name}}' : '{{.ID}}'];
      const ids = (await this.command('podman', ['--connection', this.config.connection, ...listArgs], signal)).trim().split(/\s+/).filter(Boolean);
      for (const id of ids) {
        check(/^[A-Za-z0-9._-]+$/.test(id));
        const values = JSON.parse(await this.command('podman', ['--connection', this.config.connection, ...(kind === 'container' ? [] : [kind]), 'inspect', id], signal));
        check(Array.isArray(values) && values.length === 1);
        const value = values[0];
        const network = kind === 'network' ? {
          labels: Object.hasOwn(value, 'labels') ? value.labels : value.Labels,
          id: Object.hasOwn(value, 'id') ? value.id : value.Id ?? value.ID,
          name: Object.hasOwn(value, 'name') ? value.name : value.Name,
          created: Object.hasOwn(value, 'created') ? value.created : value.CreatedAt ?? value.Created,
        } : undefined;
        if (network) check([network.id, network.name, network.created].every(field => typeof field === 'string' && field.length > 0));
        const labels = kind === 'container' ? value.Config?.Labels : network ? network.labels : value.Labels;
        check(labels?.['com.docker.compose.project'] === this.project && labels?.['io.loom.aft.lease'] === this.leaseId);
        const service = kind === 'container' ? labels['com.docker.compose.service'] : kind;
        check(kind !== 'container' || this.serviceNames.includes(service));
        const ports: Record<string, number> = this.cloud ? { 'loom-serve': 0, 'fleet-db': 1, 'stub-upstream': 2 } : { 'fleet-db': 0, 'loom-local': 1, 'ui-local': 2 };
        if (kind === 'container' && service in ports) {
          const portIndex = ports[service]!;
          const mappings = value.NetworkSettings?.Ports?.['8080/tcp'];
          check(Array.isArray(mappings) && mappings.length > 0 && mappings.every((mapping: { HostPort: string }) => Number(mapping.HostPort) === this.ports[portIndex]));
        }
        if (kind === 'container' && service === 'fleet-db') check(sameImageId(value.Image, this.images.fleet), 'source-mismatch');
        if (kind === 'container' && service === 'loom-local') check(sameImageId(value.Image, this.images.loom), 'source-mismatch');
        let namespaceSha256:string|undefined,networkIds:string[]|undefined;
        if(kind==='container'&&service==='loom-local'){
          const adapter=value.Mounts?.filter((mount:{Destination:string})=>mount.Destination==='/opt/aft');
          check(adapter?.length===1&&adapter[0].RW===false&&adapter[0].Source===this.config.adapter.build.root,'source-mismatch');
          const mounts=value.Mounts.map((mount:{Destination:string;Type:string;Source?:string;Name?:string;RW:boolean})=>{
            check(typeof mount.Destination==='string'&&typeof mount.Type==='string'&&typeof mount.RW==='boolean');
            return {destination:mount.Destination,type:mount.Type,source:mount.Source??null,name:mount.Name??null,writable:mount.RW};
          }).sort((a:{destination:string},b:{destination:string})=>a.destination.localeCompare(b.destination));
          check(new Set(mounts.map((mount:{destination:string})=>mount.destination)).size===mounts.length);
          const networks=value.NetworkSettings?.Networks;
          check(networks&&typeof networks==='object'&&!Array.isArray(networks));
          networkIds=Object.values(networks).map(network=>{const id=(network as {NetworkID?:unknown}).NetworkID;check(typeof id==='string'&&id.length>0);return id as string;}).sort();
          check(networkIds.length>0&&networkIds.length<=16&&new Set(networkIds).size===networkIds.length);
          namespaceSha256=hash(JSON.stringify({mounts,networkIds}));
        }
        if (kind === 'container' && this.cloud && this.stackImages[service]) check(sameImageId(value.Image, this.stackImages[service]!), 'source-mismatch');
        let workVolume: string | undefined;
        if(kind==='container'&&service==='ui-local'){
          const frontend=value.Mounts?.filter((mount:{Destination:string})=>mount.Destination==='/srv');
          check(frontend?.length===1&&frontend[0].RW===false&&frontend[0].Source===this.renderer!.buildRoot);
        }
        if (kind === 'container' && this.cloud && service === 'loom-serve') {
          const mounts = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/work');
          check(Array.isArray(mounts) && mounts.length === 1 && mounts[0].Type === 'volume' && typeof mounts[0].Name === 'string'); workVolume = mounts[0].Name;
          const credentials = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/home/node/.codex');
          const frontend = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/opt/webui');
          check(credentials?.length === 1 && credentials[0].RW === false && credentials[0].Source === this.config.modecloud!.codexAuthRoot);
          check(frontend?.length === 1 && frontend[0].RW === false && frontend[0].Source === this.config.modecloud!.frontendDist);
        }
        if (kind === 'container' && service === 'fleet-auth-seed') check(value.State?.Status !== 'exited' || value.State.ExitCode === 0, 'observation-failed');
        records.push({ id: kind === 'volume' ? value.Name : network ? network.id : value.Id ?? value.ID, kind, service,
          generation: kind === 'container' ? `${value.Id}:${value.State?.StartedAt}` : network ? `${network.name}:${network.created}` : `${value.Name ?? value.Id ?? value.ID}:${value.CreatedAt ?? value.Created}`,
          pid: kind === 'container' ? value.State?.Pid ?? 0 : 0, state: kind === 'container' ? value.State?.Status ?? 'unknown' : 'allocated',
          ...(kind === 'container' ? {startedAt:value.State?.StartedAt, healthy: value.State?.Health?.Status === 'healthy' } : {}), ...(workVolume ? { workVolume } : {}),...(namespaceSha256?{namespaceSha256,networkIds}:{}) });
      }
    }
    check(new Set(records.map(record => `${record.kind}:${record.id}`)).size === records.length);
    for(const container of records.filter(record=>record.networkIds))check(container.networkIds!.every(id=>records.some(record=>record.kind==='network'&&record.id===id)),'ownership-mismatch');
    if (this.cloud) for (const record of records.filter(record => record.workVolume)) check(records.some(volume => volume.kind === 'volume' && volume.id === record.workVolume));
    return records;
  }
  async provision(plan: FixturePlan, record: (resource: Resource) => void, signal: AbortSignal) {
    check((await this.inventory(signal)).length === 0);
    // Project ownership is recorded before the first auth/build/start effect so
    // a failed auth copy or partially failed compose up is still cleaned up.
    record({ id: this.project, kind: 'compose', generation: this.leaseId });
    this.attempted = true;
    if (this.profile === 'agents-real-opencode') {
      await this.command('bash', ['test/local-mode/real-opencode-copy.sh', 'make', this.env().LOCAL_MODE_OPENCODE_COPY!], signal);
    }
    await this.closeSockets();
    await this.command('podman', [...this.composeArgs(), 'up', '--no-build', '-d'], signal);
    this.objects = await this.inventory(signal);
    check(this.serviceNames.every(service => this.objects.filter(object => object.kind === 'container' && object.service === service &&
      (object.state === 'running' || service === 'fleet-auth-seed' && object.state === 'exited')).length === 1), 'observation-failed');
    for (const service of this.cloud ? ['fleet-db', 'loom-serve'] : ['loom-local']) {
      const container = this.objects.find(object => object.kind === 'container' && object.service === service)!;
      await this.command('podman', ['--connection', this.config.connection, 'wait', '--condition=healthy', '--condition=unhealthy', '--condition=exited', container.id], signal);
      const after = await this.inventory(signal); const ready = after.find(object => object.id === container.id);
      check(ready && ready.generation === container.generation && ready.state === 'running' && ready.healthy, 'observation-failed');
    }
    if (this.cloud) return this.provisionCloud(signal);
    const apiOrigin = `http://127.0.0.1:${this.ports[1]}`;
    const filesOrigin = `http://127.0.0.1:${this.ports[2]}`;
    await this.read(apiOrigin, '/api/config', signal);
    await this.read(filesOrigin, '/api/config', signal);
    const workspace = await this.read(apiOrigin, '/api/workspaces/LOCALMODE', signal) as { data?: { id: string; path: string; repos: { name: string; path: string }[] } };
    const data = workspace.data;
    check(data?.id === 'LOCALMODE' && data.path === '/root/.loom/workspaces/LOCALMODE' && data.repos.length === 1 &&
      data.repos[0]?.name === 'source-repo' && data.repos[0]?.path === `${data.path}/source-repo`, 'identity-mismatch');
    const catalog = await this.read(apiOrigin, '/api/workspaces/LOCALMODE/v1/harnesses/opencode/models', signal) as { providers?: { models: { id: string; is_default?: boolean }[] }[] };
    const models = catalog.providers?.flatMap(provider => provider.models) ?? [];
    check(models.some(model => model.id === plan.model), 'identity-mismatch');
    if (this.profile === 'agents-real-opencode') check(models.filter(model => !model.id.startsWith('aft/')).length > 1, 'identity-mismatch');
    this.discoveredWorkspace=Object.freeze({id:data!.id,repo:data!.repos[0]!.path});
    return { apiOrigin, filesOrigin, workspaceId: 'LOCALMODE', repo: this.discoveredWorkspace.repo };
  }
  private async provisionCloud(signal: AbortSignal) {
    const container = this.objects.find(object => object.kind === 'container' && object.service === 'loom-serve')!;
    const logs = await this.command('podman', ['--connection', this.config.connection, 'logs', '--tail', '5000', container.id], signal);
    check(logs.includes('opened cloud fleet-db client') && !logs.includes('embedded fleet-db started'), 'identity-mismatch');
    const apiOrigin = `http://127.0.0.1:${this.ports[0]}`;
    await this.read(apiOrigin, '/api/health', signal);
    const probe = await this.nativeRead({ operation: 'controlled-codex-preflight' }, signal) as { ready?: boolean; cleaned?: boolean; complete?: boolean };
    check(probe.ready === true && probe.cleaned === true && probe.complete === true, 'observation-failed');
    const seed = await this.nativeRead({ operation: 'seed-modecloud-repo' }, signal) as { sourceRepo: string };
    check(seed.sourceRepo === '/work/source-repos/aft-repo', 'identity-mismatch');
    const created = await this.requestOwnedHttp('api', 'POST', '/api/workspaces', { name: 'e2e-ws', type: 'empty', path: '/work/workspaces/E2E-WS', repos: [seed.sourceRepo] }, signal);
    check(created.status === 201, 'identity-mismatch');
    const repoList = await this.read(apiOrigin, '/api/workspaces/E2E-WS/repos', signal) as { success?: boolean; repos?: { name: string }[] };
    check(repoList.success === true && Array.isArray(repoList.repos) && repoList.repos.length === 1 && repoList.repos[0]!.name === 'aft-repo', 'identity-mismatch');
    return { apiOrigin, filesOrigin: apiOrigin, workspaceId: 'E2E-WS', repo: seed.sourceRepo };
  }
  async read(origin: string, relative: string, signal: AbortSignal): Promise<unknown> {
    check(relative.startsWith('/api/') && !relative.startsWith('//') && !relative.includes('\\'));
    return this.http(origin, relative, signal);
  }
  async nativeRead(request: ContainerRead, signal: AbortSignal): Promise<unknown> {
    check(request.operation!=='fixture-http'||request.method==='GET'&&/^\/__requests(\?|$)/.test(request.relativePath),'unsupported-capability');
    return this.withContainerOperation(async()=>{
      const container=await this.ownedContainer(signal);
      const response=JSON.parse(await this.command('podman',['--connection',this.config.connection,'exec',container.id,
        'node','/opt/aft/dist/fixture/container-read.js',JSON.stringify(request)],signal));
      await this.verifyContainer(container,signal);return response;
    });
  }
  /** Fixed selected SSE actor. This restarts serve and colocated OpenCode;
   * it is unrelated to the native registered-service SIGTERM actor. */
  async restartOwnedServe(expectedContainerId:string,expectedGeneration:string,signal:AbortSignal){
    signal.throwIfAborted();check(this.profile==='agents-real-opencode','unsupported-capability');
    const invocationStart=this.readinessTime.now();check(Number.isFinite(invocationStart),'observation-failed');
    let lastClock=invocationStart;
    const now=()=>{const value=this.readinessTime.now();check(Number.isFinite(value)&&value>=lastClock,'observation-failed');lastClock=value;return value;};
    return this.withContainerOperation(async()=>{
      check(this.plan&&!this.retryRestartAttestation,'identity-mismatch');
      await verifyManifest(this.config.loom.source,this.plan!.loomRevision.sourceManifestSha256);
      await verifyManifest(this.config.loom.build,this.plan!.loomRevision.buildManifestSha256);
      const before=await this.ownedContainer(signal,expectedGeneration);
      check(before.id===expectedContainerId&&Number.isInteger(before.pid)&&before.pid>0&&typeof before.startedAt==='string'&&before.startedAt.length>0,'identity-mismatch');
      const attempt=JSON.stringify([before.id,before.generation]);check(!this.restartAttempts.has(attempt),'identity-mismatch');
      const retained=this.objects.map(object=>({...object}));
      const requireRuntime=async(container:ObjectRecord,abort:AbortSignal)=>{
        const actual=JSON.parse(await this.command('podman',['--connection',this.config.connection,'exec',container.id,
          'node','/opt/aft/dist/fixture/container-read.js',JSON.stringify({operation:'runtime-identity'})],abort));
        check(actual?.runId===this.provisionedRunId&&actual?.leaseId===this.leaseId,'identity-mismatch');
      };
      await requireRuntime(before,signal);await this.verifyContainer(before,signal);
      const beforeTop=await this.command('podman',['--connection',this.config.connection,'top',before.id,'pid','comm'],signal);
      const intent=await this.artifact('observe',{operation:'compose-restart-intent',leaseId:this.leaseId,project:this.project,
        scope:'loom-local-plus-OpenCode',before:{containerId:before.id,initPid:before.pid,startedAt:before.startedAt,generation:before.generation},
        additionalRestrictions:{dispatchTimeoutMs:60000,maximumReadinessBodyBytes:4*1024*1024},
        processListing:{value:redact(beforeTop,this.fixtureSecrets),redaction:redactionFacts(beforeTop,this.fixtureSecrets)}});
      let candidate:ObjectRecord|undefined;
      const adopt=async(abort:AbortSignal,expectedTarget?:ObjectRecord,allowedTargets?:readonly ObjectRecord[])=>{
        const validate=(records:ObjectRecord[],expected?:ObjectRecord,allowed?:readonly ObjectRecord[])=>{
          check(records.length===retained.length,'identity-mismatch');
          const targets=records.filter(record=>record.kind==='container'&&record.service==='loom-local');check(targets.length===1,'identity-mismatch');
          const target=targets[0]!;check(typeof target.startedAt==='string'&&target.startedAt.length>0&&
            target.namespaceSha256===before.namespaceSha256&&before.namespaceSha256,'identity-mismatch');
          const matches=(expected:ObjectRecord)=>target.id===expected.id&&target.generation===expected.generation&&target.pid===expected.pid;
          if(allowed)check(allowed.some(matches),'identity-mismatch');else if(expected)check(matches(expected),'identity-mismatch');
          const neighbors=retained.filter(record=>record.kind!=='container'||record.id!==before.id);
          check(neighbors.every(previous=>records.some(record=>record.kind===previous.kind&&record.id===previous.id&&
            record.service===previous.service&&record.generation===previous.generation&&record.pid===previous.pid)),'identity-mismatch');
          return target;
        };
        abort.throwIfAborted();const first=await this.inventory(abort);abort.throwIfAborted();const target=validate(first,expectedTarget??(allowedTargets?undefined:candidate),allowedTargets);
        // Capture an immutable attempted identity BEFORE marker reads can yield.
        // This restricts retries; it does not grant resource ownership yet.
        candidate??=Object.freeze({...target});
        await requireRuntime(target,abort);
        const records=await this.inventory(abort);abort.throwIfAborted();validate(records,target);
        // Only this fixed dispatch can enroll one successor. Its actual image,
        // lease/run marker, persistent mounts and all neighboring resources are
        // revalidated before and after the read; a later change is never adopted.
        this.objects=records;return records.find(record=>record.kind==='container'&&record.id===target.id)!;
      };
      await this.verifyContainer(before,signal);signal.throwIfAborted();this.restartAttempts.add(attempt);
      let after:ObjectRecord|undefined;
      const readinessAttempts:{status?:number;complete:boolean;elapsedMs:number}[]=[];
      try{
        // A separate fixed dispatch guard is a safety bound, not part of the
        // frozen helper's post-restart readiness window.
        const dispatch=AbortSignal.any([signal,AbortSignal.timeout(60000)]);
        await this.command('podman',[...this.composeArgs(),'restart','loom-local'],dispatch);
        dispatch.throwIfAborted();
        // Frozen Bash SECONDS subtracts the invocation's integer epoch second.
        // Floor each absolute timestamp BEFORE subtraction, preserving its
        // subsecond phase. The deadline is checked ONLY after curl fails:
        // a request begun after the final sleep can still succeed. The timeout
        // guard is therefore failure-only, never a successful-response veto.
        const failureWindow=AbortSignal.timeout(180000),readinessStart=now();
        const invocationSecond=Math.floor(invocationStart/1000);
        const failureDeadlineSeconds=Math.floor(readinessStart/1000)-invocationSecond+180;
        after=await adopt(signal);
        check(after.state==='running'&&Number.isInteger(after.pid)&&after.pid>0&&after.pid!==before.pid&&after.startedAt!==before.startedAt,'identity-mismatch');
        let readyStatus:number|undefined,elapsedMs=0;
        for(let attempt=0;attempt<181;attempt++){
          signal.throwIfAborted();elapsedMs=now()-readinessStart;
          await adopt(signal,after);
          const requestNow=()=>this.readinessTime.monotonicNow?.()??this.readinessTime.now();
          const requestStart=requestNow();check(Number.isFinite(requestStart),'observation-failed');
          const request=AbortSignal.any([signal,AbortSignal.timeout(3000)]);let response:{status:number;complete:true}|undefined;
          try{request.throwIfAborted();response=await this.readiness(`http://127.0.0.1:${this.ports[1]}`,request);request.throwIfAborted();
            const requestElapsed=requestNow()-requestStart;
            check(Number.isFinite(requestElapsed)&&requestElapsed>=0&&requestElapsed<=3000,'observation-failed');
            check(response.complete===true&&Number.isInteger(response.status)&&response.status>=100&&response.status<=599,'observation-failed');}
          catch{response=undefined;}
          signal.throwIfAborted();elapsedMs=now()-readinessStart;
          await adopt(signal,after);
          readinessAttempts.push({...(response?{status:response.status}:{}),complete:!!response,elapsedMs});
          if(response&&response.status>=200&&response.status<400){readyStatus=response.status;break;}
          check(Math.floor((readinessStart+elapsedMs)/1000)-invocationSecond<failureDeadlineSeconds,'observation-failed');
          failureWindow.throwIfAborted();await this.readinessTime.nextAttempt(signal);
        }
        check(readyStatus!==undefined,'observation-failed');
        const afterTop=await this.command('podman',['--connection',this.config.connection,'top',after.id,'pid','comm'],signal);
        await this.verifyContainer(after,signal);
        await verifyManifest(this.config.loom.source,this.plan!.loomRevision.sourceManifestSha256);
        await verifyManifest(this.config.loom.build,this.plan!.loomRevision.buildManifestSha256);
        signal.throwIfAborted();
        const fact={scope:'loom-local-plus-OpenCode' as const,
          before:{containerId:before.id,initPid:before.pid,startedAt:before.startedAt,generation:before.generation},
          after:{containerId:after.id,initPid:after.pid,startedAt:after.startedAt!,generation:after.generation},
          readiness:{path:'/api/config' as const,status:readyStatus!,complete:true as const,attempts:readinessAttempts.length,elapsedMs,
            windowMs:180000 as const,requestTimeoutMs:3000 as const}};
        const receipt=await this.artifact('observe',{operation:'compose-restarted',intent,fact,
          readinessClock:{policy:'deadline-after-failed-request',invocationStartMs:invocationStart,readinessStartMs:readinessStart,failureDeadlineSeconds},
          successor:{namespaceSha256:after.namespaceSha256,predecessorContainerId:before.id,containerIdChanged:after.id!==before.id},readinessAttempts,
          processListing:{value:redact(afterTop,this.fixtureSecrets),redaction:redactionFacts(afterTop,this.fixtureSecrets)}});return {...fact,receipt};
      }catch{
        // Failed or cancelled dispatch may still have restarted this exact
        // owned container. Refresh only that recorded identity for cleanup;
        // never retry the mutation or replace the resource roster wholesale.
        let cleanupIdentityRetained=false;
        const cleanupTargets=[{...before},...(after||candidate?[{...(after??candidate)!}]:[])];
        const retry=async(abort:AbortSignal)=>{after=await adopt(abort,undefined,cleanupTargets);};
        this.retryRestartAttestation=retry;
        try{await retry(AbortSignal.timeout(15000));cleanupIdentityRetained=true;this.retryRestartAttestation=undefined;}
        catch{/* retain the SAME attempted identity for an owned cleanup retry */}
        const receipt=await this.artifact('failure',{operation:'compose-restart-uncertain',intent,cleanupIdentityRetained,readinessAttempts,
          ...(candidate?{attestationCandidate:{containerId:candidate.id,initPid:candidate.pid,startedAt:candidate.startedAt,generation:candidate.generation}}:{}),
          ...(after?{after:{containerId:after.id,initPid:after.pid,startedAt:after.startedAt,generation:after.generation}}:{})});
        throw new FixtureError('observation-failed',receipt);
      }
    });
  }
  async prepareCleanup(signal:AbortSignal){
    signal.throwIfAborted();
    await this.withContainerOperation(async()=>{
      const retry=this.retryRestartAttestation;
      if(retry){await retry(signal);check(this.retryRestartAttestation===retry,'identity-mismatch');this.retryRestartAttestation=undefined;}
    });
  }
  async inspect(resource: Resource, signal?: AbortSignal): Promise<Inventory> {
    signal?.throwIfAborted();
    if (resource.kind === 'ports') return { complete: true, owned: resource.generation === this.leaseId && this.ports.length > 0, services: [] };
    if (resource.kind === 'compose') {
      check(resource.id === this.project && resource.generation === this.leaseId);
      const records = await this.inventory(signal);
      signal?.throwIfAborted();
      if (this.objects.length) check(records.every(record => this.objects.some(owned => owned.kind === record.kind && owned.id === record.id && owned.generation === record.generation&&
        owned.namespaceSha256===record.namespaceSha256&&(owned.pid===record.pid||record.kind==='container'&&record.state==='exited'&&record.pid===0))) &&
        this.objects.every(owned => records.some(record => record.id === owned.id && record.kind === owned.kind)));
      if (this.objects.length) this.objects = records;
      return { complete: true, owned: true, services: records.filter(record => record.kind === 'container').map(record => ({
        id: record.id, pid: record.pid, generation: record.generation, state: record.state,
      })) };
    }
    const stamp = this.stamps.get(resource.id); const current = await this.files.lstat(resource.id);
    signal?.throwIfAborted();
    check(stamp && !current.isSymbolicLink() && stamp.dev === current.dev && stamp.ino === current.ino && resource.generation === `${current.dev}:${current.ino}`);
    if (resource.kind === 'lock') check((await this.files.readFile(resource.id, 'utf8')) === this.locks.get(resource.id));
    signal?.throwIfAborted();
    return { complete: true, owned: true, services: [] };
  }
  async remove(resource: Resource): Promise<void> {
    await this.inspect(resource);
    while (this.cleanups.length) { await this.cleanups[this.cleanups.length - 1]!(); this.cleanups.pop(); }
    if (resource.kind === 'compose') {
      await this.withContainerOperation(async()=>{
        await this.inspect(resource);
        await this.command('podman', [...this.composeArgs(), 'down', '-v', '--remove-orphans']);
        check((await this.inventory()).length === 0);
      });
      if (this.profile === 'agents-real-opencode') await this.command('bash', ['test/local-mode/real-opencode-copy.sh', 'remove', this.env().LOCAL_MODE_OPENCODE_COPY!]);
      this.removed = true; this.objects = []; return;
    }
    if (resource.kind === 'ports') { await this.closeSockets(); return; }
    if (resource.kind === 'lock') { await this.files.unlink(resource.id); return; }
    check(!this.attempted || this.removed);
    // Diagnostics are retained. Only exact run-owned transient state is removed;
    // never remove the root, parent, source tree or browser profile implicitly.
    const state = path.join(this.root, 'state');
    try {
      check(await this.files.realpath(state) === state && (await this.files.lstat(state)).isDirectory());
      await this.files.rm(state, { recursive: true });
    } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
  }
  async artifact(kind: 'acquire' | 'release' | 'observe' | 'failure', value: unknown): Promise<Artifact> {
    const bytes = Buffer.from(JSON.stringify({ kind, value, images: this.images }));
    const id = path.join(this.root, 'evidence', `${kind}-${this.uuid()}.json`);
    check(this.root && await this.files.realpath(this.root) === this.root);
    const directory = await this.files.lstat(this.root); const stamp = this.stamps.get(this.root);
    check(stamp && directory.ino === stamp.ino && directory.dev === stamp.dev);
    const evidence = await this.files.lstat(path.join(this.root, 'evidence'));
    const evidenceStamp = this.stamps.get(path.join(this.root, 'evidence'));
    check(evidenceStamp && !evidence.isSymbolicLink() && evidence.ino === evidenceStamp.ino && evidence.dev === evidenceStamp.dev);
    await this.files.writeFile(id, bytes, { mode: 0o600, flag: 'wx' });
    const receipt:Artifact={ id, sha256: hash(bytes), bytes: bytes.length, mediaType: 'application/json', redaction: 'sanitized' };
    check(this.issuedArtifacts);await this.issuedArtifacts!.remember(receipt,bytes);return receipt;
  }
  /** Private bridge only. Public Compose callbacks must parse/translate using
   * the canonical reviewed schema and retain facts in their existing store. */
  async readIssuedArtifact(receipt:Artifact,signal:AbortSignal):Promise<string>{
    check(this.issuedArtifacts,'unsupported-capability');return this.issuedArtifacts!.read(receipt,signal);
  }
}
