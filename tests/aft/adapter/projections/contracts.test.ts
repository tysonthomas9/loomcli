import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { comparePromptArgv, probePromptArgvDouble, type PromptExpectation } from './prompt-argv.js';
import { compareReceiptFields, receiptSourceVersions, type ReceiptComparisonInput } from './receipt-replay.js';
import { compareText, projectPlainSource, projectRendererTree, reasoningPreview, utf16Length, words, javascriptWords, type RendererNode } from './text.js';

test('preview retains both original Python and product JS length contracts explicitly', () => {
  assert.equal(reasoningPreview('  ## **First _line_**\nSecond', 'utf16'), 'First line');
  assert.equal(reasoningPreview('  ## **First _line_**\nSecond', 'python-codepoints'), 'First line');
  assert.equal(reasoningPreview('x'.repeat(121), 'utf16'), 'x'.repeat(119) + '…');
  assert.equal(reasoningPreview('x'.repeat(118) + '😀😀', 'utf16'), 'x'.repeat(118) + '\ud83d…');
  assert.equal(reasoningPreview('x'.repeat(118) + '😀😀', 'python-codepoints'), 'x'.repeat(118) + '😀😀');
  assert.equal(utf16Length('A😀'), 3);
  assert.deepEqual(words('one\u001ctwo\u0085three\ufefffour'), ['one', 'two', 'three\ufefffour']);
  assert.deepEqual(javascriptWords('one\u001ctwo\u0085three\ufefffour'), ['one\u001ctwo\u0085three', 'four']);
  assert.equal(reasoningPreview('**x\r y**', 'python-codepoints'), 'x\r y');
  assert.equal(reasoningPreview('**x\r y**', 'utf16'), '**x\r y**');
});

test('text comparisons require complete data and preserve exact versus Python word equality', () => {
  assert.equal(compareText('A\tB', { complete: true, text: 'A B' }, 'python-words').equal, true);
  assert.equal(compareText('A\tB', { complete: true, text: 'A B' }, 'exact').equal, false);
  assert.equal(compareText('A B', { complete: true, text: 'B A' }, 'python-words').equal, false);
  assert.throws(() => compareText('', { complete: false, text: '' }, 'exact'), /incomplete/);
});

test('plain source projection rejects Markdown and preserves motion UTF16 facts', () => {
  const result = projectPlainSource({ saved: 'A 😀', final: 'A 😀', complete: true,
    frames: [{ text: 'A', streaming: true }, { text: 'A 😀', streaming: false }], arrivals: ['A ', '😀'] });
  assert.deepEqual(result.frames.map(row => row.minSourceUtf16), [1, 4]);
  assert.deepEqual(result.arrivals.map(row => row.sourceUtf16), [2, 4]);
  const input = { saved: 'text', final: 'text', complete: true, frames: [], arrivals: [] };
  assert.throws(() => projectPlainSource({ ...input, saved: '**text**' }), /independent parser/);
  assert.throws(() => projectPlainSource({ ...input, complete: false }), /incomplete/);
  assert.throws(() => projectPlainSource({ ...input, frames: [{ text: 'text', streaming: true }, { text: 't', streaming: true }] }), /backward/);
  assert.throws(() => projectPlainSource({ ...input, final: 'truncated' }), /Final text/);
});

test('renderer tree is data-only and cannot credit incomplete, cyclic or unknown nodes', () => {
  const tree: RendererNode = { kind: 'element', tag: 'P', children: [
    { kind: 'element', tag: 'SPAN', children: [{ kind: 'text', text: 'one' }] },
    { kind: 'text', text: 'two' }, { kind: 'element', tag: 'BR', children: [] }, { kind: 'text', text: 'three' },
  ] };
  assert.deepEqual(projectRendererTree({ complete: true, streaming: false, tree }), {
    raw: 'onetwothree', visible: 'onetwo\nthree', content: 'onetwo\nthree', visibleWords: 2, contentWords: 2,
  });
  assert.throws(() => projectRendererTree({ complete: false, streaming: false, tree }), /incomplete/);
  const cycle: RendererNode = { kind: 'element', tag: 'DIV', children: [] }; cycle.children.push(cycle);
  assert.throws(() => projectRendererTree({ complete: true, streaming: false, tree: cycle }), /renderer tree/);
  assert.throws(() => projectRendererTree({ complete: true, streaming: false, tree: { ...tree, evaluate: 'code' } as RendererNode }), /semantics/);
});

const flags = ['exec', '--json', '--dangerously-bypass-approvals-and-sandbox'];
const safety = '\n\n### Multi-Agent Safety Rules\nDo not touch another agent worktree.';
test('loom-lead-prompt-argv-precedence: injected records are comparator evidence only', async () => {
  const prompt = 'Literal {{ .AgentName }} {{.Role}}' + safety;
  const expectation: PromptExpectation = { kind: 'literal-template', agentName: 'cust-tmpl-owned', fixtureText: 'Literal {{ .AgentName }} {{.Role}}' };
  let observations = 0;
  const result = await probePromptArgvDouble({ async observe() {
    observations++;
    return { complete: true, records: [['--version'], [...flags, prompt], [...flags, prompt]] };
  } }, expectation);
  assert.equal(observations, 1);
  assert.equal(result.execRecords, 2);
  assert.equal(result.prompt, prompt);
  assert.equal(comparePromptArgv({ kind: 'inline-precedence', inlineText: 'AFT-INLINE-owned' },
    { complete: true, records: [[...flags, 'AFT-INLINE-owned' + safety]] }).prompt, 'AFT-INLINE-owned' + safety);
  assert.equal(comparePromptArgv({ kind: 'builtin-lead' }, { complete: true,
    records: [[...flags, '## INTERACTIVE MODE: Project Lead' + safety]] }).execRecords, 1);
});

test('argv negatives detect moved prompt, expanded template, duplicate safety and retries', () => {
  const expectation: PromptExpectation = { kind: 'inline-precedence', inlineText: 'custom' };
  for (const records of [[], [['--version']], [['exec', '--json', 'custom' + safety]],
    [[...flags, 'custom' + safety, '--later']], [[...flags, 'custom builtin:pr-review' + safety]],
    [[...flags, 'custom' + safety + safety]], [[...flags, 'custom' + safety], [...flags, 'custom changed' + safety]],
    [[...flags, 'custom ## INTERACTIVE MODE: Project Lead' + safety]]]) {
    assert.throws(() => comparePromptArgv(expectation, { complete: true, records }));
  }
  assert.throws(() => comparePromptArgv(expectation, { complete: false, records: [[...flags, 'custom' + safety]] }), /incomplete/);
  assert.throws(() => comparePromptArgv({ kind: 'literal-template', agentName: 'owned', fixtureText: 'Literal' },
    { complete: true, records: [[...flags, 'Literal owned' + safety]] }), /template/);
});

function replay(source: ReceiptComparisonInput['source']): ReceiptComparisonInput {
  const record = { requestId: 'request-owned', agentId: 'agt_owned', result: { message_id: 'message-owned', state: 'waiting' as const, replaced: false, interrupted: true, turn_id: 'turn-owned' } };
  return { source, sourceSha256: receiptSourceVersions[source], complete: true, text: 'exact U3 text',
    original: structuredClone(record), retry: structuredClone(record) };
}
function handover(input: ReceiptComparisonInput) {
  const key = 'msg_' + createHash('sha256').update('agt_owned\0request-owned').digest('hex').slice(0, 26);
  input.retry.result.state = 'handed';
  input.handover = { complete: true, delivered: [{ event_id: 'delivery-owned', agent_id: 'agt_owned', text: 'exact U3 text', inputKey: key }],
    delivery: [{ event_id: 'delivery-owned', text: 'exact U3 text', input_key: key }],
    native: [{ agent_id: 'agt_owned', input_key: key, native_user_message_count: 1 }] };
  if (input.source === 'children-u3') {
    input.handover.delivery.push({ event_id: 'parent-delivery', text: 'parent text', input_key: 'parent-key' });
    input.handover.native.push({ agent_id: 'agt_owned', input_key: 'parent-key', native_user_message_count: 1 });
  }
  return input;
}

test('loom-agent-message-receipt-replay: pinned source variants preserve stable receipt fields', () => {
  for (const source of ['receipts-stream', 'children-u3'] as const) {
    assert.equal(compareReceiptFields(replay(source)).progressed, false);
    assert.equal(compareReceiptFields(handover(replay(source))).progressed, true);
    const missingInterrupt = replay(source);
    delete missingInterrupt.original.result.interrupted; delete missingInterrupt.retry.result.interrupted;
    if (source === 'receipts-stream') assert.equal(compareReceiptFields(missingInterrupt).progressed, false);
    else assert.throws(() => compareReceiptFields(missingInterrupt), /send receipt/);
  }
});

test('receipt identity, source, public fields and backwards state cannot normalize across variants', () => {
  for (const corrupt of [
    (input: ReceiptComparisonInput) => { input.sourceSha256 = receiptSourceVersions['children-u3']; },
    (input: ReceiptComparisonInput) => { input.retry.requestId = 'foreign-request'; },
    (input: ReceiptComparisonInput) => { input.retry.agentId = 'agt_foreign'; },
    (input: ReceiptComparisonInput) => { input.retry.result.message_id = 'foreign-message'; },
    (input: ReceiptComparisonInput) => { input.retry.result.replaced = true; },
    (input: ReceiptComparisonInput) => { input.retry.result.interrupted = false; },
    (input: ReceiptComparisonInput) => { delete input.retry.result.turn_id; },
    (input: ReceiptComparisonInput) => { input.original.result.state = 'handed'; },
    (input: ReceiptComparisonInput) => { input.retry.result.state = 'handed'; },
    (input: ReceiptComparisonInput) => { input.complete = false; },
    (input: ReceiptComparisonInput) => { Object.assign(input.retry.result, { foreign: true }); },
  ]) { const input = replay('receipts-stream'); corrupt(input); assert.throws(() => compareReceiptFields(input)); }
  const inherited = replay('receipts-stream');
  inherited.original.result = Object.create(inherited.original.result);
  inherited.retry.result = Object.create(inherited.retry.result);
  assert.throws(() => compareReceiptFields(inherited), /public send receipt/);
});

test('waiting-to-handed requires exact complete delivered/native source-specific proof', () => {
  for (const source of ['receipts-stream', 'children-u3'] as const) {
    for (const corrupt of [
      (input: ReceiptComparisonInput) => { input.handover!.complete = false; },
      (input: ReceiptComparisonInput) => { input.handover!.delivered[0]!.inputKey = 'wrong'; },
      (input: ReceiptComparisonInput) => { input.handover!.delivered[0]!.text = 'foreign'; },
      (input: ReceiptComparisonInput) => { input.handover!.delivered.push(structuredClone(input.handover!.delivered[0]!)); },
      (input: ReceiptComparisonInput) => { input.handover!.delivery[0]!.event_id = 'wrong'; },
      (input: ReceiptComparisonInput) => { input.handover!.native[0]!.agent_id = 'agt_foreign'; },
      (input: ReceiptComparisonInput) => { input.handover!.native[0]!.native_user_message_count = 2; },
    ]) { const input = handover(replay(source)); corrupt(input); assert.throws(() => compareReceiptFields(input)); }
    const wrongCount = handover(replay(source));
    if (source === 'children-u3') wrongCount.handover!.native.pop();
    else wrongCount.handover!.native.push(structuredClone(wrongCount.handover!.native[0]!));
    assert.throws(() => compareReceiptFields(wrongCount));
  }
});
