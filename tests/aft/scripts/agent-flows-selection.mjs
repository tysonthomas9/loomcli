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
  if (!exactKeys(batch, ['files', 'agents']) || !Array.isArray(batch.files) ||
      batch.files.length < 1 || !unique(batch.files) ||
      !(exactKeys(batch.agents, ['leads', 'children']) || exactKeys(batch.agents, ['leads', 'children', 'reviewers'])) ||
      !Array.isArray(batch.agents.leads) || !Array.isArray(batch.agents.children) ||
      (batch.agents.reviewers !== undefined && !Array.isArray(batch.agents.reviewers))) fail('unknown or invalid coverage batch');
  directory(coverageDir);
  if (!(batch.agents.leads.length || batch.agents.reviewers?.length) ||
      !batch.agents.leads.every(lead => (exactKeys(lead, ['name', 'suite', 'model_required']) ||
        exactKeys(lead, ['name', 'suite', 'model_required', 'model_exception']) ||
        exactKeys(lead, ['name', 'suite', 'model_required', 'model_proof']) ||
        (batchName === 'lifecycle-delete' && exactKeys(lead, ['name', 'suite', 'model_required', 'end_state']))) &&
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
        (batchName === 'lifecycle-delete' && exactKeys(child, ['name', 'parent', 'suite', 'end_state']) && child.end_state === 'deleted')) ||
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
if (batchName === 'default' && cases.length !== 9) fail('default selection must contain exactly nine cases');
if (cases.length > 10) fail('selected cases exceed absolute ceiling of 10');
if (agents && [...agents.leads, ...agents.children, ...(agents.reviewers ?? [])].some(agent => !suites.some(suite => suite.name === agent.suite)))
  fail('declared agent is not bound to a selected suite');
process.stdout.write(JSON.stringify({ batch: batchName, count: cases.length, suites, cases, agents }) + '\n');
