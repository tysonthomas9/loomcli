import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, writeFile, readFile, readdir, realpath, rm, lstat } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { z } from 'zod';
import { CapabilityRegistry, calculateImplementationPin, getRegisteredResource, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createCoreProviders, defineOperation, createSyntheticProbe, createEvidenceStore, putEvidenceStore,
  putFixture, getFixture, disposeFixtures, type OwnedFixture, AgentRow, AgentRef, type HttpResponse } from './index.js';
import { SavedCaptureInput, rereadSavedCapture } from './saved-capture.js';
import { SavedEventsOutput } from './events.js';
import type { RunnerOptions } from '@tysonthomas9/aft/runner';
import { createFixtureOperationAuthority } from './authority.js';
import { NativeOperationEffects } from './native-operation-effects.js';

const literal = (value: unknown) => ({ literal: value });
const ref = (binding: string, pointer?: string) => ({ ref: { binding, ...(pointer ? { pointer } : {}) } });
const suffix = 'Q7mR2pK9xT4vN8cY6bL5fS3dH1jW0';

// This is an owned process/transport double, never a browser or native runtime proof.
test('actual runner keeps suite authority separate from probe data and transports the exact concatenated token', async t => {
  const root = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-adapter-runner-')));
  const oldPath = process.env.PATH; const oldHome = process.env.AFT_HOME;
  t.after(async () => {
    if (oldPath === undefined) delete process.env.PATH; else process.env.PATH = oldPath;
    if (oldHome === undefined) delete process.env.AFT_HOME; else process.env.AFT_HOME = oldHome;
    await rm(root, { recursive: true });
  });
  const bin = path.join(root, 'bin'); await mkdir(bin); process.env.PATH = `${bin}:${oldPath ?? ''}`;
  process.env.AFT_HOME = path.join(root, 'aft-home');
  const state = path.join(root, 'editable.json'); const commands = path.join(root, 'commands.jsonl');
  await writeFile(state, JSON.stringify(''));
  await writeFile(path.join(bin, 'agent-browser'), `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2); fs.appendFileSync(${JSON.stringify(commands)}, JSON.stringify(args)+'\\n');
const at = args.indexOf('--session'); const command = args.slice(at + 2);
const state = ${JSON.stringify(state)};
if (command[0] === 'keyboard' && command[1] === 'inserttext') fs.writeFileSync(state, JSON.stringify(command[2]));
if (command[0] === 'eval') {
  const expression = command[1];
  if (expression.includes('const c=') && expression.includes('editable-text')) console.log(JSON.stringify(fs.readFileSync(state,'utf8')));
  else if (expression.includes('data-aft-target')) console.log(JSON.stringify({count:1}));
  else if (expression.includes('__aftAxe')) console.log(JSON.stringify({violations:[]}));
  else console.log('null');
} else if (command[0] === 'get') console.log(command[1] === 'url' ? 'about:blank' : 'Owned double');
else if (command[0] === 'network') console.log(JSON.stringify({requests:[]}));
`, { mode: 0o700 });
  const { runFiles } = await import('@tysonthomas9/aft/runner');
  const sourceRoot = fileURLToPath(new URL('.', import.meta.url));
  const sourceFiles = (await readdir(sourceRoot)).filter(file => file.endsWith('.ts') && !file.endsWith('.test.ts'));
  const pin = calculateImplementationPin(sourceRoot, sourceFiles, 'index.ts', 'createCoreProviders');
  const testPin = calculateImplementationPin(sourceRoot, ['runner-contracts.test.ts'], 'runner-contracts.test.ts', 'test');
  const registry = new CapabilityRegistry(); const providers = createCoreProviders(pin);
  const evidence = await createEvidenceStore(await realpath(await mkdir(path.join(root, 'evidence')).then(() => path.join(root, 'evidence'))));
  let cleanup = 0; let agentReads = 0; const nativeInputs: string[] = []; const owners: CapabilityContext[] = [];
  let expectedToken = '';
  const fixtureRunId = 'owned-fixture-run';
  const filesRoot = path.join(root, 'fixture-files'); await mkdir(filesRoot);
  await writeFile(path.join(filesRoot, 'marker-' + fixtureRunId), 'actual fixture bytes');
  const filesStamp = await lstat(filesRoot);
  const acquire = defineOperation({ id: 'test.ownedFixture', implementation: testPin, implementationSha256: testPin.sha256,
    inputSchema: z.object({}).strict(), outputSchema: z.object({ leaseId: z.string(), otherLease: z.string(),
      probeHandle: z.string(), otherProbe: z.string(), runToken: z.string(), fixtureRunId: z.string(), agent: AgentRef, otherAgent: AgentRef }).strict(),
    effects: ['write-fixture'], retry: 'never', cleanup: 'release-lease', evidenceClasses: ['deterministic'],
    async run(_input, context) {
      owners.push(context); putEvidenceStore(context, evidence);
      const fixtures = ['lease-A', 'lease-B'].map(leaseId => {
        const row = AgentRow.parse({ agent_id: 'agt_owned', workspace_id: 'workspace', repo: '/owned/repo', worktree_path: '/owned/tree',
          branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '', parent_agent_id: null,
          root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead', revision: 1, state: 'idle', running_turn_id: null,
          deleted_at: null, history_purged_at: null });
        const probe = createSyntheticProbe(context.runId, leaseId);
        const fixture: OwnedFixture = { leaseId, runId: context.runId, caseId: context.caseId, suiteId: context.suiteId, scope: context.scope,
          workspaceId: 'workspace', repo: row.repo, profile: 'deterministic', evidenceClass: 'deterministic', expiresAtUtcMs: Date.now() + 100000,
          roots: new Map([['fixture-files', {path:filesRoot,device:filesStamp.dev,inode:filesStamp.ino}]]), agents: new Map(), syntheticProbe: probe, secrets: [], readApi: async route => {
            assert.match(route,/\/events\?/);
            return {status:200,body:{events:[{agent_id:row.agent_id,seq:1,event_id:'saved_event',kind:'item.completed',turn_id:'saved_turn',
              payload:{text:'independent saved value'},created_at:'2026-10-09T00:00:00Z'}],snapshot_seq:1,next:1,more:false}};
          },
          readFiles: async () => ({status:404,body:{}}), resolveAgent: async (agentId) => {
            assert.equal(agentId, 'agt_bound'); agentReads++;
            return {row: AgentRow.parse({...row,agent_id:agentId}),commonDir:'/owned/repo/.git'};
          }, verify: async () => {},
          dispose: async () => { cleanup++; } };
        fixture.agents.set(row.agent_id, { row, commonDir: '/owned/repo/.git', native: {
          pinnedExecutable: '/owned/opencode',
          registration: async () => ({url:'http://127.0.0.1:4123/',password:'private',pid:42,generation:'generation',endpointId:'endpoint'}),
          process: async () => ({pid:42,generation:'generation',executable:'/owned/opencode',argv:['/owned/opencode','serve','--service']}),
          agent: async () => row, sessions: async () => [{agent_id:row.agent_id,harness:'opencode',native_root:'',native_id:'ses_owned'}],
          read: async (route): Promise<HttpResponse> => {
            const typed = JSON.parse(await readFile(state, 'utf8')) as string;
            if (route.includes('/message?')) nativeInputs.push(typed);
            return {status:200,body:route === '/api/info' ? {pid:42} : route.includes('/message?') ? {data:[{id:'msg_1',sessionID:'ses_owned',
              type:'assistant',time:{completed:1},content:[{type:'tool',id:'call_1',name:'bash',state:{status:'completed',input:{command:typed},content:{text:'sanitized'}}}]}]} :
              {data:{id:'ses_owned',metadata:{agent_id:row.agent_id},location:{directory:row.worktree_path}}}};
          },
        } });
        // Explicit injected TEST route; no production profile authority.
        fixture.operationAuthority=createFixtureOperationAuthority(fixture,Object.fromEntries(
          Object.entries(NativeOperationEffects).map(([id,effects])=>[id,{evidenceClass:'deterministic',effects:[...effects]}])));
        putFixture(context, fixture); return fixture;
      });
      const a = fixtures[0]!; const b = fixtures[1]!; expectedToken = a.syntheticProbe!.value;
      return {value:{leaseId:a.leaseId,otherLease:b.leaseId,probeHandle:a.syntheticProbe!.handle,otherProbe:b.syntheticProbe!.handle,runToken:context.runId,fixtureRunId,agent:{fixtureLeaseId:a.leaseId,workspaceId:'workspace',agentId:'agt_owned'},otherAgent:{fixtureLeaseId:b.leaseId,workspaceId:'workspace',agentId:'agt_owned'}},
        evidenceClass:'deterministic',identity:{fixtureLeaseId:a.leaseId}};
    }, async dispose(context) {
      for (const lease of ['lease-A','lease-B']) assert.ok(getRegisteredResource(context, `@loom/aft-adapter/fixtures/v1:${lease}`, lease));
      await disposeFixtures(context);
    },
  });
  const foreignRun = defineOperation({ id: 'test.foreignRun', implementation:testPin, implementationSha256:testPin.sha256,
    inputSchema:z.object({leaseId:z.string()}).strict(), outputSchema:z.object({ok:z.boolean()}).strict(),
    effects:['read-api'], retry:'never', cleanup:'none', evidenceClasses:['deterministic'],
    async run(input, context) { context.runId = 'foreign-run'; await getFixture(context, input.leaseId); return {value:{ok:true},evidenceClass:'deterministic'}; },
  });
  let priorCaseCapture: SavedCaptureInput | undefined;
  const reread = defineOperation({id:'test.rereadCapture',implementation:testPin,implementationSha256:testPin.sha256,
    inputSchema:SavedCaptureInput,outputSchema:SavedEventsOutput,effects:['read-api','read-filesystem'],retry:'never',cleanup:'none',
    evidenceClasses:['deterministic'],async run(input,context) {
      const observed=await rereadSavedCapture(context,input,pin.sha256);
      return {value:observed.data,evidenceClass:'deterministic',identity:{fixtureLeaseId:input.agent.fixtureLeaseId}};
    }});
  const remember = defineOperation({id:'test.rememberCapture',implementation:testPin,implementationSha256:testPin.sha256,
    inputSchema:SavedCaptureInput,outputSchema:z.object({remembered:z.boolean()}).strict(),effects:['read-api','read-filesystem'],
    retry:'never',cleanup:'none',evidenceClasses:['deterministic'],async run(input,context) {
      await rereadSavedCapture(context,input,pin.sha256);priorCaseCapture=input;
      return {value:{remembered:true},evidenceClass:'deterministic',identity:{fixtureLeaseId:input.agent.fixtureLeaseId}};
    }});
  const prior = defineOperation({id:'test.priorCapture',implementation:testPin,implementationSha256:testPin.sha256,
    inputSchema:z.object({agent:AgentRef}).strict(),outputSchema:SavedEventsOutput,effects:['read-api','read-filesystem'],
    retry:'never',cleanup:'none',evidenceClasses:['deterministic'],async run(input,context) {
      assert.ok(priorCaseCapture);
      const observed=await rereadSavedCapture(context,{...priorCaseCapture,agent:input.agent},pin.sha256);
      return {value:observed.data,evidenceClass:'deterministic',identity:{fixtureLeaseId:input.agent.fixtureLeaseId}};
    }});
  providers.push(acquire,foreignRun,reread,remember,prior); for (const provider of providers) registry.register(provider);
  await writeFile(path.join(root,'aft.policy.json'),JSON.stringify({requiredProfile:'declarative',registry:providers.map(provider =>
    ({id:provider.id,version:provider.version,implementationSha256:provider.implementationSha256}))}));
  const native = (agent: unknown, probe: unknown, registration?: string) => ({capability:{request:{id:'loom.native.observe',version:1,input:{
    agent,view:literal('tools'),nativeSessionId:registration ? ref(registration,'/currentNativeSessionId') : literal('ses_owned'),
    nativeRoot:registration ? ref(registration,'/currentNativeRoot') : literal(''),
    expectedGeneration:registration ? ref(registration,'/serviceGeneration') : literal('generation'),
    ...(registration ? {expectedServicePid:ref(registration,'/servicePid'),expectedEndpointId:ref(registration,'/registeredEndpointId')} : {}),
    maxMessages:literal(200),probeHandle:probe}},as:'tools'}});
  const positive = (name: string, outputOccurrences: number) => ({name,steps:[
    {bind:{as:'sentinel',value:{op:'concat',args:[literal('ghp_AFTONLY'),ref('runToken'),literal(suffix)]}}},
    {replace:{locator:{selector:'#editor'},value:ref('sentinel')}}, native(ref('agent'),ref('probe')),
    {assert:{op:'eq',args:[ref('tools','/records/0/probe/inputOccurrences'),literal(1)]}},
    {assert:{op:'eq',args:[ref('tools','/records/0/probe/outputOccurrences'),literal(outputOccurrences)]}},
  ]});
  const fileCase = (name: string, prefix: string, exists: boolean, rootId = 'fixture-files') => ({name,steps:[
    {capability:{request:{id:'loom.filesystem.observe',version:1,input:{leaseId:ref('lease'),rootId:literal(rootId),
      relativePath:{op:'concat',args:[literal(prefix),ref('fixtureRunId')]},view:literal('presence'),maxBytes:literal(100),maxEntries:literal(1)}},as:'file'}},
    {assert:{op:'eq',args:[ref('file','/entries/0/relativePath'),{op:'concat',args:[literal(prefix),ref('fixtureRunId')]}]}},
    {assert:{op:'eq',args:[ref('file','/entries/0/exists'),literal(exists)]}},
  ]});
  const suiteFile = path.join(root,'adapter.test.yaml');
  const saved = (agent:unknown,as:string) => ({capability:{request:{id:'loom.api.savedEvents',version:1,input:{agent,
    after:literal(0),pageSize:literal(2),maxPages:literal(2),maxRecords:literal(10),kinds:literal([])}},as}});
  const rereadStep=(agent:unknown,receipt:unknown,as='reread')=>({capability:{request:{id:reread.id,version:1,input:{agent,captureReceipt:receipt}},as}});
  await writeFile(suiteFile,JSON.stringify({suite:'Adapter runner contracts',setup:[{capability:{request:{id:acquire.id,version:1,input:{}},as:'fixture'}},
    saved(ref('fixture','/agent'),'setupCapture')],
    exports:{agent:{data:{binding:'fixture',pointer:'/agent'}},otherAgent:{data:{binding:'fixture',pointer:'/otherAgent'}},lease:{resource:{binding:'fixture',pointer:'/leaseId'}},probe:{data:{binding:'fixture',pointer:'/probeHandle'}},
      otherLease:{data:{binding:'fixture',pointer:'/otherLease'}},otherProbe:{data:{binding:'fixture',pointer:'/otherProbe'}},runToken:{data:{binding:'fixture',pointer:'/runToken'}},fixtureRunId:{data:{binding:'fixture',pointer:'/fixtureRunId'}},
      savedReceipt:{data:{binding:'setupCapture',pointer:'/captureReceipt'}}},
    tests:[{name:'suite saved capture',steps:[rereadStep(ref('agent'),ref('savedReceipt')),
      {assert:{op:'eq',args:[ref('reread','/events/0/payload/text'),literal('independent saved value')]}}]},
      {name:'saved wrong expectation',steps:[rereadStep(ref('agent'),ref('savedReceipt')),
        {assert:{op:'eq',args:[ref('reread','/events/0/payload/text'),literal('incorrect expectation')]}}]},
      {name:'receipt data without lease authority',steps:[rereadStep(ref('otherAgent'),ref('savedReceipt'))]},
      {name:'case saved capture',steps:[saved(ref('agent'),'caseCapture'),rereadStep(ref('agent'),ref('caseCapture','/captureReceipt')),
        {capability:{request:{id:remember.id,version:1,input:{agent:ref('agent'),captureReceipt:ref('caseCapture','/captureReceipt')}},as:'remembered'}}]},
      {name:'copied prior case capture',steps:[{capability:{request:{id:prior.id,version:1,input:{agent:ref('agent')}},as:'prior'}}]},
      fileCase('scalar file present','marker-',true),fileCase('scalar file missing','absent-',false),
      fileCase('scalar wrong expectation','marker-',false),fileCase('scalar foreign root','marker-',true,'foreign-root'),positive('first',0),positive('second',0),positive('independent wrong expectation',1),
      {name:'bind reference chain',steps:[
        {capability:{request:{id:'loom.agent.bind',version:1,input:{leaseId:ref('lease'),workspaceId:literal('workspace'),agentId:literal('agt_bound')}},as:'boundAgent'}},
        {capability:{request:{id:'loom.agent.observe',version:1,input:{agent:ref('boundAgent','/agentRef')}},as:'observedAgent'}},
        {assert:{op:'eq',args:[ref('observedAgent','/agentId'),literal('agt_bound')]}},
        {assert:{op:'eq',args:[ref('boundAgent','/agentRef/fixtureLeaseId'),ref('lease')]}},
      ]},
      {name:'native registration chain',steps:[
        {capability:{request:{id:'loom.native.registration',version:1,input:{agent:ref('agent'),maxRegistrations:literal(10)}},as:'registered'}},
        native(ref('agent'),ref('probe'),'registered'),
        {assert:{op:'eq',args:[ref('tools','/serviceGeneration'),ref('registered','/serviceGeneration')]}},
      ]},
      {name:'missing reference projection',steps:[{capability:{request:{id:'loom.agent.observe',version:1,input:{agent:ref('agent','/agentRef')}},as:'missing'}}]},
      {name:'foreign reference identity',steps:[{capability:{request:{id:'loom.agent.observe',version:1,input:{agent:literal({fixtureLeaseId:'lease-A',workspaceId:'foreign',agentId:'agt_bound'})}},as:'foreign'}}]},
      {name:'unexported reference scope',steps:[{capability:{request:{id:'loom.agent.bind',version:1,input:{leaseId:ref('otherLease'),workspaceId:literal('workspace'),agentId:literal('agt_bound')}},as:'unowned'}}]},
      {name:'cross fixture probe',steps:[native(ref('agent'),ref('otherProbe'))]},
      {name:'data grants no authority',steps:[native(ref('otherAgent'),ref('otherProbe'))]},
      {name:'cross run',steps:[{capability:{request:{id:foreignRun.id,version:1,input:{leaseId:ref('lease')}},as:'foreign'}}]},
    ]}));
  const options: RunnerOptions = {registry,requiredProfile:'declarative',mode:'strict',caseHeaders:false,agent:false,headed:false,record:false,recordAll:false,
    screenshots:false,retries:0,stepTimeoutMs:500,pollIntervalMs:1,budgetWarn:0,budgets:{},reportDir:path.join(root,'reports'),a11y:false,
    a11yBaselines:path.join(root,'baselines'),a11yImpact:'serious',testidAttribute:'data-testid'};
  const result = await runFiles([suiteFile], options);
  for (const name of ['first','second','bind reference chain','native registration chain','scalar file present','scalar file missing','suite saved capture','case saved capture']) assert.equal(result.tests.find(item=>item.name===name)?.status,'passed',name);
  for (const name of ['independent wrong expectation','cross fixture probe','data grants no authority','cross run',
    'missing reference projection','foreign reference identity','unexported reference scope','scalar wrong expectation','scalar foreign root',
    'saved wrong expectation','receipt data without lease authority','copied prior case capture'])
    assert.equal(result.tests.find(item=>item.name===name)?.status,'failed',name);
  assert.deepEqual(nativeInputs,[expectedToken,expectedToken,expectedToken,expectedToken]);
  const calls = (await readFile(commands,'utf8')).trim().split('\n').map(line=>JSON.parse(line) as string[]);
  assert.deepEqual(calls.filter(call=>call.includes('inserttext')).map(call=>call.at(-1)),[expectedToken,expectedToken,expectedToken]);
  assert.equal(agentReads,2); assert.equal(cleanup,2); assert.equal(owners.length,1);
  assert.equal(owners[0]!.resources.has('@loom/aft-adapter/fixtures/v1:lease-A'),false);
});
