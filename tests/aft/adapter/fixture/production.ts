import { spawn } from 'node:child_process';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { FixtureError, type Artifact, type FixtureDriver, type FixturePlan, type Inventory, type Resource, type Revision } from './lifecycle.js';
import { reservePort, type PortReservation } from './process.js';
import type { ContainerRead } from './container-read.js';
import { readHttp, type Http } from './host.js';
import { Json } from '../protocol.js';

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
}
export type ComposeRead = (origin: string, relative: string, signal: AbortSignal) => Promise<unknown>;
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
export async function verifyManifest(manifest: FileManifest, expected: string): Promise<void> {
  check(await fs.realpath(manifest.root) === manifest.root, 'source-mismatch');
  check(manifest.entries.length > 0 && new Set(manifest.entries.map(entry => entry.relativePath)).size === manifest.entries.length, 'source-mismatch');
  const entries = [...manifest.entries].sort((a, b) => a.relativePath < b.relativePath ? -1 : a.relativePath > b.relativePath ? 1 : 0);
  for (const entry of entries) {
    check(entry.relativePath.split('/').every(part => part && part !== '.' && part !== '..') && !path.isAbsolute(entry.relativePath) &&
      !entry.relativePath.includes('\\') && /^[a-f0-9]{64}$/.test(entry.sha256), 'source-mismatch');
    check(hash(await regularBytes(path.join(manifest.root, entry.relativePath))) === entry.sha256, 'source-mismatch');
  }
  check(hash(entries.map(entry => `${entry.sha256}  ${entry.relativePath}\n`).join('')) === expected, 'source-mismatch');
}
interface ObjectRecord { id: string; kind: 'container' | 'volume' | 'network'; generation: string; service: string; pid: number; state: string; healthy?: boolean; workVolume?: string }
const SERVICES = ['redis', 'fleet-db', 'loom-local', 'ui-local'];
const CLOUD_SERVICES = ['redis', 'fleet-auth-seed', 'fleet-db', 'loom-serve', 'worker', 'stub-upstream'];

export class ComposeFixtureDriver implements FixtureDriver {
  private root = '';
  private leaseId = '';
  private project = '';
  private profile = '';
  private ports: number[] = [];
  private sockets: PortReservation[] = [];
  private readonly stamps = new Map<string, { dev: number; ino: number }>();
  private readonly locks = new Map<string, string>();
  private objects: ObjectRecord[] = [];
  private attempted = false;
  private removed = false;
  private plan?: FixturePlan;
  private images = { loom: '', fleet: '' };
  private stackImages: Record<string, string> = {};
  private readonly cloudSecrets: Record<string, string> = {};
  private readonly config: ProductionConfig;
  private readonly cleanups: (() => Promise<void>)[] = [];
  constructor(config: ProductionConfig, private readonly run: ProcessRunner = runProcess,
    private readonly files: typeof fs = fs, private readonly uuid: () => string = randomUUID,
    private readonly reserve: () => Promise<PortReservation> = reservePort,
    private readonly http: ComposeRead = fetchRead, private readonly requestHttp: Http = readHttp) { this.config = structuredClone(config); }
  get runtimeRoot() { return this.root; }
  get workspaceRoot() { return '/root/.loom/workspaces/LOCALMODE'; }
  get fixtureSecrets() { return Object.values(this.cloudSecrets); }
  private get cloud() { return this.profile === 'legacy-real-codex-podman'; }
  private get serviceNames() { return this.cloud ? CLOUD_SERVICES : SERVICES; }
  enrollCleanup(cleanup: () => Promise<void>) { check(this.root); this.cleanups.push(cleanup); }
  async readOwnedConfiguration(target: 'opencode' | 'emu-scenarios', signal: AbortSignal) {
    return this.nativeRead({ operation: 'configuration-read', target }, signal) as Promise<{ bytes: string | null; complete: true }>;
  }
  async writeOwnedConfiguration(target: 'opencode' | 'emu-scenarios', bytes: string | null, signal: AbortSignal) {
    check(bytes === null || Buffer.byteLength(bytes) <= 65536);
    await this.nativeRead({ operation: 'configuration-write', target, bytes }, signal);
  }
  async requestOwnedHttp(target: 'api' | 'fake-model' | 'fake-github', method: Parameters<Http>[1], relativePath: string, body: unknown, signal: AbortSignal) {
    signal.throwIfAborted(); check(this.ports.length === 3);
    await this.inspect({ id: this.project, kind: 'compose', generation: this.leaseId });
    check(this.objects.some(object => object.kind === 'container' && object.service === (this.cloud ? 'loom-serve' : 'loom-local') && object.state === 'running'));
    check(target !== 'fake-github', 'unsupported-capability');
    if (target === 'fake-model') {
      check(!this.cloud, 'unsupported-capability');
      return this.nativeRead({ operation: 'fixture-http', method, relativePath, body: Json.parse(body) }, signal) as Promise<{ status: number; body: unknown }>;
    }
    check(relativePath.startsWith('/api/'));
    return this.requestHttp(`http://127.0.0.1:${this.ports[this.cloud ? 0 : 1]}`, method, relativePath, body, signal);
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
    return this.run({ binary, args, cwd: this.config.loom.source.root, env: this.env(), signal });
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
  async identity(plan: FixturePlan): Promise<boolean> {
    try {
      for (const [build, expected] of [[this.config.loom, plan.loomRevision], [this.config.fleet, plan.fleetRevision],
        [this.config.engine, plan.engineRevision], [this.config.adapter, plan.adapterRevision]] as const) {
        check(JSON.stringify(build.revision) === JSON.stringify(expected), 'source-mismatch');
        const head = (await this.run({ binary: 'git', args: ['rev-parse', 'HEAD'], cwd: build.source.root, env: { PATH: this.config.toolPath }, })).trim();
        const tree = (await this.run({ binary: 'git', args: ['rev-parse', 'HEAD^{tree}'], cwd: build.source.root, env: { PATH: this.config.toolPath }, })).trim();
        const dirty = await this.run({ binary: 'git', args: ['status', '--porcelain', '--untracked-files=no'], cwd: build.source.root, env: { PATH: this.config.toolPath }, });
        check(head === expected.commit && tree === expected.tree && !dirty.trim(), 'source-mismatch');
        const tracked = (await this.run({ binary: 'git', args: ['ls-files', '-z'], cwd: build.source.root, env: { PATH: this.config.toolPath } })).split('\0').filter(Boolean).sort();
        check(JSON.stringify(tracked) === JSON.stringify(build.source.entries.map(entry => entry.relativePath).sort()), 'source-mismatch');
        await verifyManifest(build.source, expected.sourceManifestSha256);
        await verifyManifest(build.build, expected.buildManifestSha256);
      }
      return true;
    } catch { return false; }
  }
  async preflight(plan: FixturePlan, signal: AbortSignal): Promise<void> {
    check(['agents-real-opencode', 'agents-emulator', 'legacy-real-codex-podman'].includes(plan.profile), 'unsupported-capability');
    this.plan = structuredClone(plan); this.profile = plan.profile;
    check(safe(plan.model) && (plan.profile === 'agents-emulator' ? plan.model === 'aft/m' : !plan.model.startsWith('aft/')), 'identity-mismatch');
    check(safe(this.config.connection) && /^[a-f0-9]{64}$/.test(this.config.connectionFingerprint));
    for (const directory of [this.config.tempParent, this.config.lockParent, this.config.hostHome]) {
      check(await this.files.realpath(directory) === directory && (await this.files.lstat(directory)).isDirectory());
    }
    const storage = await this.files.statfs(this.config.tempParent);
    check(storage.bavail * storage.bsize >= Math.max(9 * 1024 ** 3, this.config.minimumFreeBytes), 'observation-failed');
    check(await this.identity(plan), 'source-mismatch');
    check(this.config.attestedImages, 'source-mismatch');
    if (this.cloud) {
      const config = this.config.modecloud; check(config, 'source-mismatch');
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
      check(Array.isArray(observed) && observed.length === 1 && observed[0].Id === imageId, 'source-mismatch');
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
    this.project = `loom-aft-${this.uuid().replace(/-/g, '')}`;
    check(/^[a-z0-9-]+$/.test(this.project));
    this.root = await this.files.mkdtemp(path.join(this.config.tempParent, 'loom-aft-fixture-'));
    record({ id: this.root, kind: 'directory', generation: await this.stamp(this.root) });
    await this.files.chmod(this.root, 0o700);
    await this.files.mkdir(path.join(this.root, 'evidence'), { mode: 0o700 });
    await this.stamp(path.join(this.root, 'evidence'));
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
      ...(service === 'loom-local' ? { volumes: [`${this.config.adapter.build.root}:/opt/aft:ro`] } : {}),
      ...(service === 'loom-local' && this.profile === 'agents-emulator' ? {
        environment: { LOOM_OPENCODE_BIN: '/opt/fixture/loom-harness-emu', LOOM_HARNESS_EMU: '1',
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
        FLUE_REPO: '/opt/flue', AFT_FIXTURE_MODE: 'modecloud', LOOM_DRIVER_TASK_RUNNER_CMD_JSON: null, LOOM_FLUE_AGENT_MODEL: this.plan!.model,
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
        const labels = kind === 'container' ? value.Config?.Labels : value.Labels;
        check(labels?.['com.docker.compose.project'] === this.project && labels?.['io.loom.aft.lease'] === this.leaseId);
        const service = kind === 'container' ? labels['com.docker.compose.service'] : kind;
        check(kind !== 'container' || this.serviceNames.includes(service));
        const ports: Record<string, number> = this.cloud ? { 'loom-serve': 0, 'fleet-db': 1, 'stub-upstream': 2 } : { 'fleet-db': 0, 'loom-local': 1, 'ui-local': 2 };
        if (kind === 'container' && service in ports) {
          const portIndex = ports[service]!;
          const mappings = value.NetworkSettings?.Ports?.['8080/tcp'];
          check(Array.isArray(mappings) && mappings.length > 0 && mappings.every((mapping: { HostPort: string }) => Number(mapping.HostPort) === this.ports[portIndex]));
        }
        if (kind === 'container' && service === 'fleet-db') check(value.Image === this.images.fleet, 'source-mismatch');
        if (kind === 'container' && service === 'loom-local') check(value.Image === this.images.loom, 'source-mismatch');
        if (kind === 'container' && this.cloud && this.stackImages[service]) check(value.Image === this.stackImages[service], 'source-mismatch');
        let workVolume: string | undefined;
        if (kind === 'container' && this.cloud && service === 'loom-serve') {
          const mounts = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/work');
          check(Array.isArray(mounts) && mounts.length === 1 && mounts[0].Type === 'volume' && typeof mounts[0].Name === 'string'); workVolume = mounts[0].Name;
          const credentials = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/home/node/.codex');
          const frontend = value.Mounts?.filter((mount: { Destination: string }) => mount.Destination === '/opt/webui');
          check(credentials?.length === 1 && credentials[0].RW === false && credentials[0].Source === this.config.modecloud!.codexAuthRoot);
          check(frontend?.length === 1 && frontend[0].RW === false && frontend[0].Source === this.config.modecloud!.frontendDist);
        }
        if (kind === 'container' && service === 'fleet-auth-seed') check(value.State?.Status !== 'exited' || value.State.ExitCode === 0, 'observation-failed');
        records.push({ id: kind === 'volume' ? value.Name : value.Id ?? value.ID, kind, service,
          generation: kind === 'container' ? `${value.Id}:${value.State?.StartedAt}` : `${value.Name ?? value.Id ?? value.ID}:${value.CreatedAt ?? value.Created}`,
          pid: kind === 'container' ? value.State?.Pid ?? 0 : 0, state: kind === 'container' ? value.State?.Status ?? 'unknown' : 'allocated',
          ...(kind === 'container' ? { healthy: value.State?.Health?.Status === 'healthy' } : {}), ...(workVolume ? { workVolume } : {}) });
      }
    }
    check(new Set(records.map(record => `${record.kind}:${record.id}`)).size === records.length);
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
    return { apiOrigin, filesOrigin, workspaceId: 'LOCALMODE', repo: data!.repos[0]!.path };
  }
  private async provisionCloud(signal: AbortSignal) {
    const container = this.objects.find(object => object.kind === 'container' && object.service === 'loom-serve')!;
    const logs = await this.command('podman', ['--connection', this.config.connection, 'logs', '--tail', '5000', container.id], signal);
    check(logs.includes('opened cloud fleet-db client') && !logs.includes('embedded fleet-db started'), 'identity-mismatch');
    const apiOrigin = `http://127.0.0.1:${this.ports[0]}`;
    await this.read(apiOrigin, '/api/health', signal);
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
    await this.inspect({ id: this.project, kind: 'compose', generation: this.leaseId });
    signal.throwIfAborted();
    const container = this.objects.filter(object => object.kind === 'container' && object.service === (this.cloud ? 'loom-serve' : 'loom-local'));
    check(container.length === 1 && container[0]!.state === 'running');
    return JSON.parse(await this.command('podman', ['--connection', this.config.connection, 'exec', container[0]!.id,
      'node', '/opt/aft/fixture/container-read.js', JSON.stringify(request)], signal));
  }
  async inspect(resource: Resource): Promise<Inventory> {
    if (resource.kind === 'ports') return { complete: true, owned: resource.generation === this.leaseId && this.ports.length > 0, services: [] };
    if (resource.kind === 'compose') {
      check(resource.id === this.project && resource.generation === this.leaseId);
      const records = await this.inventory();
      if (this.objects.length) check(records.every(record => this.objects.some(owned => owned.kind === record.kind && owned.id === record.id && owned.generation === record.generation)) &&
        this.objects.every(owned => records.some(record => record.id === owned.id && record.kind === owned.kind)));
      if (this.objects.length) this.objects = records;
      return { complete: true, owned: true, services: records.filter(record => record.kind === 'container').map(record => ({
        id: record.id, pid: record.pid, generation: record.generation, state: record.state,
      })) };
    }
    const stamp = this.stamps.get(resource.id); const current = await this.files.lstat(resource.id);
    check(stamp && !current.isSymbolicLink() && stamp.dev === current.dev && stamp.ino === current.ino && resource.generation === `${current.dev}:${current.ino}`);
    if (resource.kind === 'lock') check((await this.files.readFile(resource.id, 'utf8')) === this.locks.get(resource.id));
    return { complete: true, owned: true, services: [] };
  }
  async remove(resource: Resource): Promise<void> {
    await this.inspect(resource);
    while (this.cleanups.length) { await this.cleanups[this.cleanups.length - 1]!(); this.cleanups.pop(); }
    if (resource.kind === 'compose') {
      await this.command('podman', [...this.composeArgs(), 'down', '-v', '--remove-orphans']);
      check((await this.inventory()).length === 0);
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
    return { id, sha256: hash(bytes), bytes: bytes.length, mediaType: 'application/json', redaction: 'sanitized' };
  }
}
