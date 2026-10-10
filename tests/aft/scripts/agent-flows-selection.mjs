#!/usr/bin/env node
// Load only committed, named Agent AFT batches through the installed AFT loader.
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, realpathSync, readdirSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [testsDir, loaderPath, batchName = 'default'] = process.argv.slice(2);
const fail = message => { throw new Error(message); };
const regular = path => {
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink() || realpathSync(path) !== resolve(path)) fail(`unsafe file: ${path}`);
};
const directory = path => {
  const stat = lstatSync(path);
  if (!stat.isDirectory() || stat.isSymbolicLink() || realpathSync(path) !== resolve(path)) fail(`unsafe directory: ${path}`);
};
const namePattern = /^[a-z][a-z0-9-]*$/;
const agentPattern = /^[a-z][a-z0-9-]*\$\{RUN_ID\}[a-z0-9-]*$/;
const exactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
  Object.keys(value).sort().join(',') === keys.sort().join(',');
const unique = list => new Set(list).size === list.length;

directory(testsDir);
regular(loaderPath);
if (batchName !== 'default' && !namePattern.test(batchName)) fail('invalid coverage batch name');
const catalogPath = join(testsDir, 'agent-flow-batches.json');
regular(catalogPath);
const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
if (!exactKeys(catalog, ['version', 'batches']) || catalog.version !== 1 ||
    !catalog.batches || typeof catalog.batches !== 'object' || Array.isArray(catalog.batches)) fail('invalid batch catalog');
const coverageDir = join(testsDir, 'live-agent-coverage-suites');
const originalDir = join(testsDir, 'live-agent-flow-suites');
let files, agents;
if (batchName === 'default') {
  directory(originalDir);
  files = readdirSync(originalDir).filter(file => file.endsWith('.test.yaml')).sort();
  if (files.length !== 3 || files.join(',') !== 'children.test.yaml,lead-chat.test.yaml,recovery-lifecycle.test.yaml')
    fail('default selection must remain the original three suite files');
  files = files.map(file => join(originalDir, file));
  agents = null;
} else {
  const batch = catalog.batches[batchName];
  const lifecycleDelete = { files: ['lifecycle-delete.test.yaml'], expected_cases: [
    { suite: 'coverage-lifecycle-delete', name: 'stale-delete' },
    { suite: 'coverage-lifecycle-delete', name: 'native-cascade' }],
  agents: { leads: [
    { name: 'cov-delete-control-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true },
    { name: 'cov-delete-target-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true, end_state: 'deleted' },
    { name: 'cov-delete-parent-${RUN_ID}', suite: 'coverage-lifecycle-delete', model_required: true, end_state: 'deleted' }],
  children: [{ name: 'cov-delete-child-${RUN_ID}', parent: 'cov-delete-parent-${RUN_ID}',
    suite: 'coverage-lifecycle-delete', end_state: 'deleted' }] } };
  const queueCase = { suite: 'coverage-children-queue',
    name: 'live task child keeps user and parent FIFO slots then hands a later user interrupt first' };
  const queueAgents = { leads: [{ name: 'cov-child-queue-lead-${RUN_ID}',
    suite: 'coverage-children-queue', model_required: true }],
  children: [{ name: 'cov-child-queue-task-${RUN_ID}', parent: 'cov-child-queue-lead-${RUN_ID}',
    suite: 'coverage-children-queue' }] };
  const visualSkip = { files: ['chat-visual-skip.test.yaml'], expected_cases: [
    { suite: 'live-chat-visual-skip', name: 'normal-motion skip link stays hidden after mouse focus' }],
  agents: { leads: [{ name: 'cov-visual-skip-${RUN_ID}', suite: 'live-chat-visual-skip', model_required: false }],
    children: [] } };
  const receiptsEdges = { files: ['receipts-stream-edges.test.yaml'], expected_cases: [
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE 1.5d interrupt with message cancels running turn and delivers replacement once' },
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE 1.6c 1.7b queued UTF-8 body limit and rejected over-cap receipt' },
    { suite: 'live-receipts-stream-edges',
      name: 'LIVE CR1 2.0n rejected Create inputs leave no Agent or worktree then same-name retry succeeds' }],
  agents: { leads: [
    { name: 'coverage-rs-edges-interrupt-${RUN_ID}', suite: 'live-receipts-stream-edges', model_required: true },
    { name: 'coverage-rs-edges-create-${RUN_ID}', suite: 'live-receipts-stream-edges',
      model_required: true, model_proof: 'api_post_create' }],
  reviewers: [{ name: 'coverage-rs-edges-large-${RUN_ID}', suite: 'live-receipts-stream-edges' }],
  children: [] } };
  const chatStop = { files: ['chat-controls-stop.test.yaml'], expected_cases: [
    { suite: 'live-chat-controls-stop',
      name: 'OC1 bare Chat Stop loses the native ask without executing it and permits a real next turn' }],
  agents: { leads: [], reviewers: [
    { name: 'cov-controls-stop-${RUN_ID}', suite: 'live-chat-controls-stop' }], children: [] } };
  if (!(exactKeys(batch, ['files', 'agents']) || exactKeys(batch, ['files', 'agents', 'expected_cases'])) ||
      !Array.isArray(batch.files) ||
      (batchName === 'lifecycle-delete' && JSON.stringify(batch) !== JSON.stringify(lifecycleDelete)) ||
      (batchName === 'children-queue' && (batch.files.join(',') !== 'children-queue.test.yaml' ||
        JSON.stringify(batch.expected_cases) !== JSON.stringify([queueCase]) ||
        JSON.stringify(batch.agents) !== JSON.stringify(queueAgents))) ||
      (batchName === 'chat-visual-skip' && JSON.stringify(batch) !== JSON.stringify(visualSkip)) ||
      (batchName === 'receipts-edges' && JSON.stringify(batch) !== JSON.stringify(receiptsEdges)) ||
      (batchName === 'chat-stop' && JSON.stringify(batch) !== JSON.stringify(chatStop)) ||
      (batchName === 'tool-policy' && (!Array.isArray(batch.expected_cases) ||
        batch.expected_cases.length !== 3 || batch.files.join(',') !== 'tool-policy.test.yaml')) ||
      (batch.expected_cases !== undefined && (!Array.isArray(batch.expected_cases) || !batch.expected_cases.length ||
        !batch.expected_cases.every(test => exactKeys(test, ['suite', 'name']) &&
          typeof test.suite === 'string' && namePattern.test(test.suite) &&
          typeof test.name === 'string' && test.name.length > 0) ||
        !unique(batch.expected_cases.map(test => `${test.suite}\0${test.name}`)))) ||
      batch.files.length < 1 || !unique(batch.files) ||
      !(exactKeys(batch.agents, ['leads', 'children']) || exactKeys(batch.agents, ['leads', 'children', 'reviewers'])) ||
      !Array.isArray(batch.agents.leads) || !Array.isArray(batch.agents.children) ||
      (batch.agents.reviewers !== undefined && !Array.isArray(batch.agents.reviewers))) fail('unknown or invalid coverage batch');
  directory(coverageDir);
  if (!(batch.agents.leads.length || batch.agents.reviewers?.length) ||
      !batch.agents.leads.every(lead => (exactKeys(lead, ['name', 'suite', 'model_required']) ||
        exactKeys(lead, ['name', 'suite', 'model_required', 'model_exception']) ||
        exactKeys(lead, ['name', 'suite', 'model_required', 'model_proof']) ||
        (batchName === 'lifecycle-delete' && batch.files.join(',') === 'lifecycle-delete.test.yaml' &&
          lead.suite === 'coverage-lifecycle-delete' && exactKeys(lead, ['name', 'suite', 'model_required', 'end_state']))) &&
        typeof lead.name === 'string' && agentPattern.test(lead.name) && typeof lead.suite === 'string' &&
        typeof lead.model_required === 'boolean' &&
        (lead.end_state === undefined || (lead.end_state === 'deleted' && lead.model_required)) &&
        (lead.model_exception === undefined || typeof lead.model_exception === 'boolean') &&
        (lead.model_proof === undefined || (lead.model_required && lead.model_proof === 'api_post_create')) &&
        !(lead.model_required && lead.model_exception)))
    fail('invalid declared Lead names');
  const leadNames = batch.agents.leads.map(lead => lead.name);
  if (!unique(leadNames)) fail('duplicate declared Lead');
  const reviewers = batch.agents.reviewers ?? [];
  if (!reviewers.every(reviewer => exactKeys(reviewer, ['name', 'suite']) &&
      typeof reviewer.name === 'string' && agentPattern.test(reviewer.name) && typeof reviewer.suite === 'string'))
    fail('invalid declared reviewer');
  const reviewerNames = reviewers.map(reviewer => reviewer.name);
  if (!unique(reviewerNames) || reviewerNames.some(name => leadNames.includes(name))) fail('duplicate declared root agent');
  const childNames = [];
  for (const child of batch.agents.children) {
    if (!(exactKeys(child, ['name', 'parent', 'suite']) ||
        (batchName === 'lifecycle-delete' && batch.files.join(',') === 'lifecycle-delete.test.yaml' &&
          child.suite === 'coverage-lifecycle-delete' && exactKeys(child, ['name', 'parent', 'suite', 'end_state']) &&
          child.end_state === 'deleted')) ||
        typeof child.name !== 'string' ||
        !agentPattern.test(child.name) || typeof child.suite !== 'string' ||
        !batch.agents.leads.some(lead => lead.name === child.parent && lead.suite === child.suite))
      fail('invalid declared child or parent');
    childNames.push(child.name);
  }
  if (!unique(childNames) || childNames.some(name => leadNames.includes(name) || reviewerNames.includes(name))) fail('duplicate declared agent');
  for (const file of batch.files) {
    if (typeof file !== 'string' || !/^[a-z][a-z0-9-]*\.test\.yaml$/.test(file) || basename(file) !== file)
      fail('unsafe or foreign suite selection');
  }
  files = batch.files.map(file => join(coverageDir, file));
  agents = batch.agents;
}

const { loadSuite } = await import(pathToFileURL(loaderPath).href);
const cases = [], suites = [];
for (const file of files) {
  regular(file);
  const parsed = loadSuite(file);
  if (!Array.isArray(parsed.tests) || !parsed.tests.length) fail(`empty parsed suite: ${file}`);
  if (batchName === 'chat-visual-skip') {
    const steps = parsed.tests.flatMap(test => test.steps ?? []);
    const createdNames = steps.filter(step => step.fill?.testid === 'create-agent-name').map(step => step.fill.value);
    if (createdNames.length !== 1 || createdNames[0] !== `cov-visual-skip-${process.env.RUN_ID}` ||
        !steps.some(step => step.select?.testid === 'create-agent-backend' && step.select.value === 'opencode') ||
        steps.some(step => step.fill?.label === 'Message'))
      fail('visual skip suite must create only its declared OpenCode Lead without a Chat message');
  }
  if (batchName === 'default' && parsed.tests.length !== 3) fail(`default suite must contain exactly three cases: ${file}`);
  if (typeof parsed.suite !== 'string' || !namePattern.test(parsed.suite) || suites.some(suite => suite.name === parsed.suite))
    fail(`duplicate or unsafe suite name: ${file}`);
  const names = parsed.tests.map(test => test.name);
  if (!unique(names)) fail(`duplicate cases in ${file}`);
  suites.push({ path: file, name: parsed.suite,
    sha256: createHash('sha256').update(readFileSync(file)).digest('hex'), cases: names });
  for (const name of names) cases.push({ suite: parsed.suite, name });
}
if (!unique(cases.map(test => `${test.suite}\0${test.name}`))) fail('duplicate selected cases');
if (batchName !== 'default' && catalog.batches[batchName].expected_cases &&
    (cases.length !== catalog.batches[batchName].expected_cases.length ||
      cases.some((test, i) => test.suite !== catalog.batches[batchName].expected_cases[i].suite ||
        test.name !== catalog.batches[batchName].expected_cases[i].name)))
  fail('selected cases differ from the reviewed batch');
if (batchName === 'default' && cases.length !== 9) fail('default selection must contain exactly nine cases');
if (cases.length > 10) fail('selected cases exceed absolute ceiling of 10');
if (agents && [...agents.leads, ...agents.children, ...(agents.reviewers ?? [])].some(agent => !suites.some(suite => suite.name === agent.suite)))
  fail('declared agent is not bound to a selected suite');
process.stdout.write(JSON.stringify({ batch: batchName, count: cases.length, suites, cases, agents }) + '\n');
