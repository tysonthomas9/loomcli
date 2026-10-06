#!/usr/bin/env node
// The lifecycle batch may delete declared agents. Verify their saved identity
// before Delete and the durable tombstone by exact public Agent API ID after it.
import { readFileSync } from 'node:fs';

const [evidenceDir, declaredText, runId, apiUrl, repo, target] = process.argv.slice(2);
const fail = message => { throw new Error(`deleted Agent proof: ${message}`); };
const readLines = name => {
  const content = readFileSync(`${evidenceDir}/${name}`, 'utf8');
  if (!content.trim()) fail(`${name} is empty`);
  return content.trim().split('\n').map(line => JSON.parse(line));
};
const declared = JSON.parse(declaredText);
const deleted = [...declared.leads, ...declared.children].filter(agent => agent.end_state === 'deleted');
if (!deleted.length || deleted.some(agent => agent.suite !== 'live-lifecycle-delete')) fail('unexpected deleted declaration');
const receipts = readLines('lifecycle-delete-preflight.jsonl');
const nativeRows = readLines('native-sessions.jsonl');
const models = readLines('model-selections.jsonl');
const survivors = JSON.parse(readFileSync(`${evidenceDir}/actual-agent-models.json`, 'utf8'));
if (receipts.length !== deleted.length || !Array.isArray(survivors)) fail('missing or extra pre-Delete receipts');
const unique = rows => new Set(rows).size === rows.length;
if (!unique(receipts.map(row => row.api?.agent_id)) || !unique(receipts.map(row => row.api?.name)))
  fail('duplicate pre-Delete ID or name');
if (!unique(models.map(row => row.agent_id))) fail('duplicate model selection ID');
const fields = ['agent_id', 'name', 'preset', 'created_by_kind', 'parent_agent_id',
  'root_agent_id', 'repo', 'harness', 'model', 'model_unverified'];
const result = [];
for (const agent of deleted) {
  const matches = receipts.filter(row => row.api?.name === agent.name);
  if (matches.length !== 1) fail(`missing exact pre-Delete receipt for ${agent.name}`);
  const receipt = matches[0];
  const before = receipt.api;
  const native = receipt.native;
  const sessionPrefix = `aft-${agent.suite}-`;
  if (receipt.run_id !== runId || receipt.suite !== agent.suite ||
      typeof receipt.session !== 'string' || !receipt.session.startsWith(sessionPrefix) ||
      !/^[0-9]+$/.test(receipt.session.slice(sessionPrefix.length)) ||
      typeof receipt.captured_at !== 'string' ||
      !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(receipt.captured_at) ||
      !Number.isFinite(Date.parse(receipt.captured_at)) ||
      !/^[A-Za-z0-9_-]+$/.test(before.agent_id ?? '') ||
      before.workspace_id !== 'LOCALMODE' || before.repo !== repo || before.harness !== 'opencode' ||
      before.state === 'deleted' || before.deleted_at !== null ||
      before.model_unverified !== false ||
      survivors.some(row => row.agent_id === before.agent_id || row.name === before.name))
    fail(`pre-Delete run, session, identity, or model mismatch for ${agent.name}`);
  const isLead = declared.leads.some(lead => lead.name === agent.name);
  if (isLead) {
    if (!agent.model_required || before.model !== target || before.preset !== 'lead' || before.created_by_kind !== 'user' ||
        before.parent_agent_id !== null || before.root_agent_id !== null) fail('deleted Lead ownership mismatch');
    const selection = models.filter(row => row.agent_id === before.agent_id && row.name === agent.name &&
      row.run_id === runId && row.session === receipt.session && row.ui_selected_model === target &&
      row.observed_saved_model === target);
    if (selection.length !== 1 || !Number.isFinite(Date.parse(selection[0].time)) ||
        Date.parse(selection[0].time) > Date.parse(receipt.captured_at))
      fail('deleted Lead has no prior exact-ID UI saved-model receipt');
  } else {
    const parent = receipts.map(row => row.api).concat(survivors).find(row => row.name === agent.parent);
    if (!parent || before.preset !== 'task' || before.created_by_kind !== 'agent' ||
        before.parent_agent_id !== parent.agent_id || before.root_agent_id !== parent.agent_id)
      fail('deleted child parent mismatch');
  }
  if (!native || native.agent_id !== before.agent_id || native.harness !== 'opencode' ||
      typeof native.native_id !== 'string' || !native.native_id || typeof native.native_root !== 'string' ||
      !nativeRows.some(row => row.agent_id === native.agent_id && row.harness === native.harness &&
        row.native_id === native.native_id && row.native_root === native.native_root))
    fail('pre-Delete owned native session receipt mismatch');
  const response = await fetch(`${apiUrl}/api/workspaces/LOCALMODE/v1/agents/${before.agent_id}`, {
    signal: AbortSignal.timeout(30000), redirect: 'error'
  });
  if (response.status !== 200) fail(`exact-ID GET did not return 200 for ${agent.name}`);
  const after = await response.json();
  if (after.state !== 'deleted' || after.workspace_id !== before.workspace_id ||
      !Number.isFinite(Date.parse(after.deleted_at)) ||
      Date.parse(receipt.captured_at) > Date.parse(after.deleted_at) ||
      fields.some(field => after[field] !== before[field]))
    fail(`exact-ID tombstone or saved identity mismatch for ${agent.name}`);
  result.push(Object.fromEntries([...fields, 'state'].map(field => [field, after[field]])));
}
process.stdout.write(`${JSON.stringify(result)}\n`);
