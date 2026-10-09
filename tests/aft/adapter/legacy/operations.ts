import { z } from 'zod';
import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { isAbsolute } from 'node:path';
import { Id, Json, RelativePath, HttpResponse, redact } from '../protocol.js';
import { FixtureId, FixtureParameters, ScenarioId, scenario } from './catalog.js';
import { redactionFacts } from '../redaction.js';
import type { LoomAuthorizedOperation } from '../authority.js';

export type LegacyAuthorizedOperation = Exclude<LoomAuthorizedOperation, 'loom.runtime.detachTerminal'>;

const Arg = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/);
export const LegacyEvidenceClasses = ['deterministic', 'persisted-public-api', 'real-native', 'live-provider'] as const satisfies readonly EvidenceClass[];
const LeaseWorkspace = { leaseId: Id, workspaceId: Arg };
export const RuntimeInput = z.object({ leaseId: Id, targetId: Id,
  operation: z.enum(['serve-restart', 'harness-restart', 'worker-stop', 'terminal-close']),
  expectedGeneration: Id }).strict();
export const RoleInput = z.discriminatedUnion('operation', [
  z.object({ ...LeaseWorkspace, operation: z.literal('list'), name: z.null() }).strict(),
  z.object({ ...LeaseWorkspace, operation: z.literal('show'), name: Arg }).strict(),
]);
export const UsageInput = z.object({ agent: z.object({ fixtureLeaseId: Id, workspaceId: Arg, agentId: Arg }).strict() }).strict();
export const TaskInput = z.object({ ...LeaseWorkspace, agentName: Arg,
  backend: z.enum(['codex', 'claude', 'opencode', 'cursor']), mode: z.enum(['once', 'auto', 'daemon']),
  issueId: Arg.nullable(), repoName: Arg.nullable().default(null),
}).strict().superRefine((input, ctx) => {
  if ((input.mode === 'daemon') !== (input.issueId !== null)) ctx.addIssue({ code: 'custom', message: 'Only daemon mode takes an assigned issue' });
});
export const SeedInput = z.object({ ...LeaseWorkspace, agentName: Arg,
  relativePath: RelativePath.refine(path => !path.split('/').some(part => part.startsWith('-') || part.toLowerCase() === '.git') &&
    !/[:\x00-\x1f\x7f]/.test(path), 'Unsafe seed path'),
  content: z.string().max(1024 * 1024), commitMessage: z.string().min(1).max(1024).refine(s => !/[\x00-\x1f\x7f]/.test(s)),
}).strict();
export const ConfigureInput = z.discriminatedUnion('setting', [
  z.object({ leaseId: Id, setting: z.literal('provider-default'), model: z.literal('aft/m'), harness: z.literal('opencode') }).strict(),
  z.object({ ...LeaseWorkspace, setting: z.literal('workspace-seed'),
    fixtureId: z.enum(['slack-clone', 'legacy-e2e-repo', 'agent-api-source-repo']), name: Arg }).strict(),
  z.object({ leaseId: Id, setting: z.enum(['fake-model-scenario', 'fake-github-scenario', 'scripted-backend-scenario']),
    fixtureId: FixtureId, scenarioId: ScenarioId, parameters: FixtureParameters, agentId: Arg.nullable().default(null) }).strict(),
]);

export class LegacyError extends Error {
  constructor(readonly code: 'invalid-input' | 'ownership-mismatch' | 'source-mismatch' | 'stale-generation' | 'process-failed' |
    'response-invalid' | 'unsupported-capability' | 'mutation-repeated' | 'cleanup-failed', message: string) { super(message); }
}
function fact(condition: unknown, code: LegacyError['code'], message: string): asserts condition {
  if (!condition) throw new LegacyError(code, message);
}
function parse<T>(schema: z.ZodType<T, z.ZodTypeDef, unknown>, input: unknown): T {
  const result = schema.safeParse(input);
  fact(result.success, 'invalid-input', 'Legacy operation input is invalid');
  return result.data;
}

export interface OwnedProcess {
  id: string; generation: string; kind: 'serve' | 'harness' | 'worker' | 'terminal';
  workspaceId: string | null; agentName: string | null; sessionName: string | null;
}
export interface LegacyLease {
  id: string; runId: string; active: boolean; evidence: EvidenceClass;
  secrets: readonly string[]; binary: string; cwd: string; env: Readonly<Record<string, string>>;
  workspaces: readonly string[];
  agents: readonly { workspaceId: string; id: string; name: string; generation: string }[];
  roles: readonly { workspaceId: string; name: string }[];
  issues: readonly { workspaceId: string; id: string }[];
  repos: readonly { workspaceId: string; name: string; sourcePath: string }[];
  processes: readonly OwnedProcess[];
  fixtures: readonly string[];
}
export interface Invocation { runId: string; invocationId: string; signal: AbortSignal }
export interface CliCommand {
  binary: string; cwd: string; argv: string[]; env: Record<string, string>; stdin: string;
}
const ProcessResult = z.object({ processId: Id, generation: Id, exitCode: z.number().int().min(0).max(255).nullable(),
  stdout: z.string(), stderr: z.string(), complete: z.boolean() }).strict();
export type ProcessResult = z.infer<typeof ProcessResult>;
const Transition = z.object({ beforeGeneration: Id, afterGeneration: Id.nullable(),
  affectedIds: z.array(Id).min(1), complete: z.literal(true) }).strict();
export interface RuntimeRequest { method: 'POST' | 'DELETE'; path: string; body: Json }
export interface ConfigurationSnapshot { complete: true; previous: Json; restoreState: Json }

// Internal injected transports are supplied by the lease owner, never authored
// suite values. Mutating drivers must atomically compare the passed generation
// with the owned process immediately before their side effect.
export interface LegacyAccess {
  lease(id: string, signal: AbortSignal, operation?: LegacyAuthorizedOperation): Promise<LegacyLease>;
  // execute enrolls the process under the lease BEFORE spawn/return, including
  // failed/incomplete responses; registerProcess attests that existing binding.
  execute(leaseId: string, command: CliCommand, signal: AbortSignal): Promise<ProcessResult>;
  registerProcess(leaseId: string, result: ProcessResult, signal: AbortSignal): Promise<void>;
  stimulate(leaseId: string, process: OwnedProcess, operation: z.infer<typeof RuntimeInput>['operation'], request: RuntimeRequest | null, signal: AbortSignal): Promise<{ transition: z.infer<typeof Transition>; response: z.infer<typeof HttpResponse> | null }>;
  request(leaseId: string, target: string, method: 'POST' | 'DELETE', path: string, body: Json, signal: AbortSignal): Promise<z.infer<typeof HttpResponse>>;
  // This check must reject symlinks/escapes against the product-resolved owned
  // worktree; lexical validation alone cannot make the legacy CLI seam safe.
  validateSeedPath(leaseId: string, workspaceId: string, agentName: string, path: string, signal: AbortSignal): Promise<void>;
  seedCommit(leaseId: string, workspaceId: string, agentName: string, signal: AbortSignal): Promise<string>;
  snapshot(leaseId: string, target: string, signal: AbortSignal): Promise<ConfigurationSnapshot>;
  restore(leaseId: string, target: string, state: Json, signal: AbortSignal): Promise<void>;
  writeConfiguration(leaseId: string, target: string, value: Json, signal: AbortSignal): Promise<void>;
  enrollCleanup(leaseId: string, cleanup: () => Promise<void>): void;
}
export interface LegacyReceipt {
  operation: string; leaseId: string; runId: string; invocationId: string;
  evidence: LegacyLease['evidence']; actor: 'loom-cli' | 'runtime' | 'fixture';
  facts: Json;
  factsRedaction: ReturnType<typeof redactionFacts>;
}
export function createLegacyOperations(access: LegacyAccess, expectedEvidence?: EvidenceClass | ((operation: LegacyAuthorizedOperation) => EvidenceClass)) {
  const claimed = new Set<string>();
  const configurations = new Map<string, ConfigurationSnapshot>();
  const configurationLocks = new Set<string>();
  const receipt = (op: string, lease: LegacyLease, call: Invocation, actor: LegacyReceipt['actor'], facts: Json): LegacyReceipt =>
    ({ operation: op, leaseId: lease.id, runId: call.runId, invocationId: call.invocationId, evidence: lease.evidence,
      actor, facts: redact(facts, lease.secrets), factsRedaction: redactionFacts(facts, lease.secrets) });
  async function owned(id: string, call: Invocation, operation: LegacyAuthorizedOperation) {
    call.signal.throwIfAborted();
    const lease = await access.lease(id, call.signal, operation);
    fact(lease.active && lease.id === id && lease.runId === call.runId, 'ownership-mismatch', 'Fixture lease is inactive or foreign');
    const expected = typeof expectedEvidence === 'function' ? expectedEvidence(operation) : expectedEvidence;
    fact(z.enum(LegacyEvidenceClasses).safeParse(lease.evidence).success && (expected === undefined || lease.evidence === expected),
      'source-mismatch', 'Legacy transport evidence differs from canonical fixture');
    fact(isAbsolute(lease.binary) && isAbsolute(lease.cwd), 'ownership-mismatch', 'CLI registration is not absolute');
    return structuredClone(lease);
  }
  function once(lease: LegacyLease, call: Invocation) {
    fact(call.invocationId.length > 0, 'invalid-input', 'Invocation identity is missing');
    const key = `${lease.id}\0${call.invocationId}`;
    fact(!claimed.has(key), 'mutation-repeated', 'Mutation invocation was already attempted');
    claimed.add(key);
  }
  function workspace(lease: LegacyLease, id: string) {
    fact(lease.workspaces.includes(id), 'ownership-mismatch', 'Workspace is foreign');
  }
  function agent(lease: LegacyLease, workspaceId: string, nameOrId: string, byId = false) {
    workspace(lease, workspaceId);
    const rows = lease.agents.filter(row => row.workspaceId === workspaceId && (byId ? row.id : row.name) === nameOrId);
    fact(rows.length === 1, 'ownership-mismatch', 'Agent identity is unavailable or ambiguous');
    return rows[0]!;
  }
  async function execute(lease: LegacyLease, call: Invocation, argv: string[], env: Record<string, string> = {}, stdin = '', register = false) {
    call.signal.throwIfAborted();
    let raw: ProcessResult;
    try { raw = await access.execute(lease.id, { binary: lease.binary, cwd: lease.cwd, argv,
      env: { ...lease.env, ...env }, stdin }, call.signal); }
    catch { throw new LegacyError('process-failed', 'Owned CLI transport failed'); }
    const result = ProcessResult.safeParse(raw);
    fact(result.success, 'response-invalid', 'CLI process response is malformed');
    if (register) await access.registerProcess(lease.id, result.data, call.signal);
    return result.data;
  }
  function cliBody(result: ProcessResult): Json {
    fact(result.complete && result.exitCode === 0, 'process-failed', 'CLI did not complete successfully');
    try { return Json.parse(JSON.parse(result.stdout)); }
    catch { throw new LegacyError('response-invalid', 'CLI JSON response is malformed'); }
  }
  async function response(lease: LegacyLease, call: Invocation, target: string, method: 'POST' | 'DELETE', path: string, body: Json) {
    let raw: z.infer<typeof HttpResponse>;
    try { raw = await access.request(lease.id, target, method, path, body, call.signal); }
    catch { throw new LegacyError('response-invalid', 'Owned fixture response is unavailable'); }
    const parsed = HttpResponse.safeParse(raw);
    fact(parsed.success && parsed.data.status >= 200 && parsed.data.status < 300, 'response-invalid', 'Owned fixture response is invalid');
    return parsed.data;
  }
  return {
    async role(raw: unknown, call: Invocation) {
      const input = parse(RoleInput, raw); const lease = await owned(input.leaseId, call, 'loom.cli.role'); workspace(lease, input.workspaceId);
      if (input.operation === 'show') fact(lease.roles.some(row => row.workspaceId === input.workspaceId && row.name === input.name), 'ownership-mismatch', 'Role is foreign');
      const argv = ['--workspace', input.workspaceId, 'role', input.operation, ...(input.operation === 'show' ? [input.name] : []), '--json'];
      const result = await execute(lease, call, argv); const body = cliBody(result);
      return { exitCode: result.exitCode, body, receipt: receipt('loom.cli.role', lease, call, 'loom-cli', { argv, body }) };
    },
    async usage(raw: unknown, call: Invocation) {
      const input = parse(UsageInput, raw); const lease = await owned(input.agent.fixtureLeaseId, call, 'loom.cli.usage');
      const row = agent(lease, input.agent.workspaceId, input.agent.agentId, true);
      const argv = ['usage', '--format', 'json', '--agent', row.name];
      const result = await execute(lease, call, argv, { LOOM_WORKSPACE_ID: input.agent.workspaceId }); const body = cliBody(result);
      return { exitCode: result.exitCode, body, receipt: receipt('loom.cli.usage', lease, call, 'loom-cli', { argv, body }) };
    },
    async task(raw: unknown, call: Invocation) {
      const input = parse(TaskInput, raw); const lease = await owned(input.leaseId, call, 'loom.cli.task'); agent(lease, input.workspaceId, input.agentName);
      const env: Record<string, string> = { LOOM_WORKSPACE_ID: input.workspaceId };
      // Erase inherited selection: an earlier invocation must not retarget this task.
      env.LOOM_ASSIGNED_TASK_ID = ''; env.LOOM_SOURCE_REPOS = '';
      if (input.issueId !== null) {
        fact(lease.issues.some(row => row.workspaceId === input.workspaceId && row.id === input.issueId), 'ownership-mismatch', 'Assigned issue is foreign');
        env.LOOM_ASSIGNED_TASK_ID = input.issueId;
      }
      if (input.repoName !== null) {
        const repos = lease.repos.filter(row => row.workspaceId === input.workspaceId && row.name === input.repoName);
        fact(repos.length === 1 && isAbsolute(repos[0]!.sourcePath), 'ownership-mismatch', 'Task repo scope is foreign');
        env.LOOM_SOURCE_REPOS = repos[0]!.sourcePath;
      }
      const argv = ['--workspace', input.workspaceId, '--backend', input.backend, 'task', input.agentName,
        ...(input.mode === 'once' ? [] : ['--auto']), ...(input.mode === 'daemon' ? ['--daemon-mode'] : [])];
      once(lease, call); const result = await execute(lease, call, argv, env, '', true);
      fact((result.complete && result.exitCode !== null) || (!result.complete && result.exitCode === null), 'response-invalid', 'Task process completion contradicts exit status');
      return { ownedProcessId: result.processId, generation: result.generation, exitCode: result.exitCode,
        stdout: result.stdout, stderr: result.stderr, complete: result.complete,
        receipt: receipt('loom.cli.task', lease, call, 'loom-cli', { argv, backend: input.backend, processId: result.processId, generation: result.generation }) };
    },
    async seedWorktree(raw: unknown, call: Invocation) {
      const input = parse(SeedInput, raw); const lease = await owned(input.leaseId, call, 'loom.fixture.seedWorktree'); agent(lease, input.workspaceId, input.agentName);
      fact(lease.evidence === 'deterministic', 'unsupported-capability', 'Worktree seeding is fixture-only');
      await access.validateSeedPath(lease.id, input.workspaceId, input.agentName, input.relativePath, call.signal);
      const argv = ['daemon', 'seed-worktree', '--workspace', input.workspaceId, '--agent', input.agentName,
        '--file', input.relativePath, '--content', '-', '--message', input.commitMessage];
      once(lease, call); const result = await execute(lease, call, argv, { LOOM_TESTSUPPORT: '1' }, input.content);
      fact(result.complete && result.exitCode === 0, 'process-failed', 'Fixture seed CLI did not complete');
      fact(result.stdout.startsWith(`seeded worktree: ws=${input.workspaceId} agent=${input.agentName} repos=`), 'response-invalid', 'Fixture seed CLI identity differs');
      const commit = await access.seedCommit(lease.id, input.workspaceId, input.agentName, call.signal);
      fact(/^[a-f0-9]{40}$/.test(commit), 'response-invalid', 'Fixture seed commit observation is invalid');
      return { commit, receipt: receipt('loom.fixture.seedWorktree', lease, call, 'fixture', { commit, actorActivity: false }) };
    },
    async stimulate(raw: unknown, call: Invocation) {
      const input = parse(RuntimeInput, raw);
      fact(input.operation !== 'terminal-close', 'unsupported-capability', 'Tab metadata deletion cannot establish a process generation transition');
      const lease = await owned(input.leaseId, call, 'loom.runtime.stimulate');
      const targets = lease.processes.filter(row => row.id === input.targetId);
      fact(targets.length === 1, 'ownership-mismatch', 'Runtime target is foreign or ambiguous'); const target = targets[0]!;
      fact(target.generation === input.expectedGeneration, 'stale-generation', 'Runtime target generation changed');
      const kind = { 'serve-restart': 'serve', 'harness-restart': 'harness', 'worker-stop': 'worker', 'terminal-close': 'terminal' }[input.operation];
      fact(target.kind === kind, 'ownership-mismatch', 'Runtime target kind differs');
      if (target.workspaceId !== null) workspace(lease, target.workspaceId);
      let request: RuntimeRequest | null = null;
      if (input.operation === 'worker-stop') {
        fact(target.workspaceId !== null && target.agentName !== null, 'ownership-mismatch', 'Lifecycle target lacks agent identity');
        agent(lease, target.workspaceId, target.agentName);
        const prefix = `/api/workspaces/${encodeURIComponent(target.workspaceId)}`;
        request = { method: 'POST', path: `${prefix}/agents/${encodeURIComponent(target.agentName)}/stop`, body: null };
      }
      once(lease, call);
      let observed: Awaited<ReturnType<LegacyAccess['stimulate']>>;
      try { observed = await access.stimulate(lease.id, target, input.operation, request, call.signal); }
      catch (error) { if (error instanceof LegacyError) throw error; throw new LegacyError('process-failed', 'Owned runtime transition failed'); }
      const wire = observed?.response;
      if (request !== null) fact(HttpResponse.safeParse(wire).success && wire!.status >= 200 && wire!.status < 300, 'response-invalid', 'Lifecycle API response is unavailable');
      else fact(wire === null, 'response-invalid', 'Unexpected runtime API response');
      const result = Transition.safeParse(observed?.transition);
      fact(result.success, 'response-invalid', 'Runtime transition observation is incomplete');
      fact(result.data.beforeGeneration === input.expectedGeneration && result.data.affectedIds.includes(target.id) &&
        result.data.affectedIds.every(id => lease.processes.some(row => row.id === id)), 'response-invalid', 'Runtime transition identities differ');
      fact(result.data.afterGeneration !== input.expectedGeneration, 'response-invalid', 'Runtime target did not transition');
      if (input.operation === 'serve-restart') fact(result.data.afterGeneration !== null, 'response-invalid', 'Serve restart has no replacement');
      if (input.operation === 'worker-stop') fact(result.data.afterGeneration === null, 'response-invalid', 'Stopped target still has a generation');
      return { ...result.data, response: wire, receipt: receipt('loom.runtime.stimulate', lease, call, 'runtime', { ...result.data, response: wire }) };
    },
    async configure(raw: unknown, call: Invocation) {
      const input = parse(ConfigureInput, raw); const lease = await owned(input.leaseId, call, 'loom.fixture.configure');
      let target: string; let value: Json; let path: '/__reset' | '/__script' | '/__fixture' | null = null;
      if (input.setting === 'provider-default') {
        target = 'provider-default'; value = { model: input.model, harness: input.harness };
      } else if (input.setting === 'workspace-seed') {
        workspace(lease, input.workspaceId); target = `workspace:${input.workspaceId}`;
        fact(lease.fixtures.includes(input.fixtureId), 'ownership-mismatch', 'Workspace fixture is foreign');
        value = { workspaceId: input.workspaceId, fixtureId: input.fixtureId, name: input.name };
      } else {
        fact(lease.evidence === 'deterministic', 'unsupported-capability', 'Scripted protocol is fixture-only');
        fact(input.setting === `${input.fixtureId}-scenario` && lease.fixtures.includes(input.fixtureId), 'ownership-mismatch', 'Fixture setting or target differs');
        let stimulus: ReturnType<typeof scenario>;
        try { stimulus = scenario(input.fixtureId, input.scenarioId, input.parameters, lease.runId); }
        catch { throw new LegacyError('invalid-input', 'Unknown or mismatched protocol stimulus'); }
        target = input.fixtureId; value = Json.parse(stimulus.payload);
        if (input.fixtureId === 'scripted-backend') {
          fact(input.agentId !== null && lease.agents.filter(row => row.id === input.agentId).length === 1, 'ownership-mismatch', 'Scripted backend agent is foreign');
          value = { agentId: input.agentId, turns: stimulus.reset ? [] : Array.isArray(value) ? value : [value] };
        } else {
          fact(input.agentId === null, 'invalid-input', 'HTTP model fixture does not select an agent');
          path = stimulus.reset ? '/__reset' : input.fixtureId === 'fake-model' ? '/__script' : '/__fixture';
        }
      }
      fact(lease.fixtures.includes(target), 'ownership-mismatch', 'Configuration target is not lease-owned');
      const key = `${lease.id}\0${target}`;
      fact(!configurationLocks.has(key), 'mutation-repeated', 'Configuration mutation is already in flight');
      configurationLocks.add(key);
      try {
        once(lease, call);
        const previous = await access.snapshot(lease.id, target, call.signal);
        fact(previous.complete === true && Json.safeParse(previous.previous).success && Json.safeParse(previous.restoreState).success,
          'response-invalid', 'Configuration snapshot is incomplete');
        if (!configurations.has(key)) {
          const saved = structuredClone(previous);
          access.enrollCleanup(lease.id, async () => {
            const current = await access.lease(lease.id, new AbortController().signal);
            fact(current.id === lease.id && current.runId === lease.runId && current.fixtures.includes(target), 'cleanup-failed', 'Configuration cleanup ownership changed');
            try { await access.restore(lease.id, target, saved.restoreState, new AbortController().signal); }
            catch { throw new LegacyError('cleanup-failed', 'Owned configuration restoration failed'); }
            configurations.delete(key);
          });
          configurations.set(key, saved);
        }
        let wire: z.infer<typeof HttpResponse> | null = null;
        if (path !== null) {
          wire = await response(lease, call, target, 'POST', path, value);
          fact(wire.body && typeof wire.body === 'object' && !Array.isArray(wire.body), 'response-invalid', 'Fixture response has no object body');
          if (path === '/__script') {
            const steps = (value as { steps: Json[] }).steps;
            fact(Number.isInteger(wire.body.queued) && Number(wire.body.queued) >= steps.length, 'response-invalid', 'Model queue response is malformed');
          } else {
            fact(wire.body.ok === true, 'response-invalid', 'Fixture response has no acknowledgment');
            if (path === '/__fixture') {
              const expected = value as { pr: { head: { sha: string }; base: { sha: string } }; files: Json[] };
              const actual = wire.body.pr as { head?: { sha?: string }; base?: { sha?: string } } | undefined;
              fact(actual?.head?.sha === expected.pr.head.sha && actual?.base?.sha === expected.pr.base.sha &&
                wire.body.files === expected.files.length, 'response-invalid', 'Forge fixture acknowledgment differs from the requested refs or files');
            }
          }
        } else {
          try { await access.writeConfiguration(lease.id, target, value, call.signal); }
          catch { throw new LegacyError('process-failed', 'Owned configuration write failed'); }
        }
        return { previous: previous.previous, response: wire,
          receipt: receipt('loom.fixture.configure', lease, call, 'fixture', { setting: input.setting, target, response: wire }) };
      } finally { configurationLocks.delete(key); }
    },
  };
}
