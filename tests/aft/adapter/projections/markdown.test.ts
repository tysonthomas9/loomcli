import assert from 'node:assert/strict';
import { test } from 'node:test';
import { projectMarkdown, partitionMarkdown, type MarkdownInput, type MotionProjection, type TerminalProjection } from './markdown.js';
import { expectedProjectionIdentity } from './identity.js';

const context = () => ({ identity: structuredClone(expectedProjectionIdentity) });
const motion = (answer: string, frames: MarkdownInput['frames'] = [], arrivals: string[] = []) =>
  projectMarkdown({ answer, frames, arrivals }, context()) as MotionProjection;
const full = (answer: string) => projectMarkdown({ answer, frames: [], arrivals: [], mode: 'terminal-full' }, context()) as TerminalProjection;

// These goldens are literal independent expectations from the original source
// contracts. No expectation is computed by ChatMarkdown or this projection.
const source = 'Start.\n\n| file | test |\n| --- | --- |\n| README.md | npm test |\n\n```json\n{"test":"npm test"}\n```\n\nEnd VISUAL_END_TEST';
const table = 'Start.\nfiletestREADME.mdnpm testExpandCopy as MarkdownCopy as CSV';
const code = table + 'jsonWrapCopy{"test":"npm test"}';
const terminal = code + 'End VISUAL_END_TEST';

test('loom-chat-markdown-projection-v1: literal full text, chrome and prefix ranges', () => {
  const cuts = [8, 33, 40, 64, 97, source.length];
  const deltas = cuts.map((end, at) => source.slice(at === 0 ? 0 : cuts[at - 1], end));
  const result = motion(source, [
    { text: 'Start.', streaming: true }, { text: table, streaming: true },
    { text: code, streaming: true }, { text: terminal, streaming: true },
    { text: terminal, streaming: false },
  ], deltas);
  assert.equal(result.terminal, terminal);
  assert.deepEqual(result.frames.map(row => [row!.minSourceUtf16, row!.maxSourceUtf16]), [[6, 8], [60, 64], [91, 97], [source.length, source.length], [source.length, source.length]]);
  assert.equal(result.frames[1]!.visibleWords, 6);
  assert.equal(result.frames[1]!.visible, 'Start.\n\nfile\ttest\nREADME.md\tnpm test');
  assert.equal(result.frames[2]!.content, 'Start.\n\nfile\ttest\nREADME.md\tnpm test\n\n{"test":"npm test"}');
  assert.equal(result.frames[2]!.contentWords, 8);
  assert.equal(result.arrivals[2]!.visibleChanged, false);
  assert.equal(result.arrivals[2]!.contentChanged, false);
  assert.equal(result.arrivals[0]!.requiredMinSourceUtf16, 6);
  assert.equal(result.arrivals.at(-1)!.sourceUtf16, source.length);
  assert.ok(!result.frames[3]!.visible.includes('Expand'));
  assert.ok(result.frames[4]!.visible.includes('Expand'));
  // tableScroll and tableActions are separate DIV boundaries, hence a blank line.
  assert.equal(result.terminalVisible, 'Start.\n\nfile\ttest\nREADME.md\tnpm test\n\nExpand\nCopy as Markdown\nCopy as CSV\n\njson\nWrap\nCopy\n\n{"test":"npm test"}\n\nEnd VISUAL_END_TEST');
});

test('code-header-only arrival creates chrome but zero reply words', () => {
  const result = motion('```json\n', [], ['```json\n']);
  assert.equal(result.terminal, 'jsonWrapCopy');
  assert.equal(result.terminalVisible, 'json\nWrap\nCopy');
  assert.equal(result.terminalContent, '');
  assert.equal(result.arrivals[0]!.contentChanged, false);
  assert.equal(result.arrivals[0]!.projectedWords, 0);
});

test('GFM delimiter arrival can retract reply words while source progresses', () => {
  const deltas = ['a b c d e f g h i\n\n| Name | Result |\n', '| --- | --- |\n| One | Passed |'];
  const result = motion(deltas.join(''), [], deltas);
  assert.deepEqual(result.arrivals.map(row => row.projectedWords), [14, 13]);
  assert.ok(result.arrivals[0]!.requiredMinSourceUtf16 < result.arrivals[1]!.requiredMinSourceUtf16);
});

test('literal HTML, inline marks, lists, entity decoding and UTF16 emoji', () => {
  const answer = '# Title\n\n- first **bold** item\n- second _item_\n\n<img src=x onerror="window.pwned=1"> and 😀.';
  const result = motion(answer, [], [answer]);
  // The list continues the heading block: HAST retains its two root newlines.
  assert.equal(result.terminal, 'Title\n\nfirst bold item\nsecond item\n<img src=x onerror="window.pwned=1"> and 😀.');
  assert.equal(result.sourceUtf16, [...answer].length + 1);
  assert.equal(full('A &amp; B [link](javascript:alert(1))').terminal, 'A & B link');
  assert.equal(full('<script>alert(1)</script>').terminal, '<script>alert(1)</script>');
  assert.equal(full('`<b>` **bold** ~~gone~~').terminal, '<b> bold gone');
  // GFM's checkbox contributes no text; its following space remains in each LI.
  assert.equal(full('- [x] done\n- [ ] pending').terminal, '\n done\n pending\n');
});

test('terminal full projection exceeds 17000 UTF16 units without motion parsing', () => {
  const answer = 'A'.repeat(17001) + ' 😀 END';
  assert.equal(full(answer).terminal, answer);
  assert.equal(full(answer).sourceUtf16, 17008);
  assert.equal(full('x'.repeat(128000)).terminal.length, 128000);
  assert.throws(() => full('x'.repeat(128001)), /bounded text/);
});

test('whole reference/HTML blocks and safe partition boundaries preserve exact raw text', () => {
  assert.deepEqual(partitionMarkdown('a\n\nb\n\n- c\n\nd'), ['a\n\n', 'b\n\n- c\n\n', 'd']);
  assert.deepEqual(partitionMarkdown('[a][id]\n\n[id]: https://example.test'), ['[a][id]\n\n[id]: https://example.test']);
  assert.deepEqual(partitionMarkdown('```text\na\n\nb\n```\n\nc'), ['```text\na\n\nb\n```\n\n', 'c']);
  assert.equal(full('[a][id]\n\n[id]: https://example.test').terminal, 'a');
  assert.equal(full('a\n\nb').terminal, 'ab');
});

test('foreign/corrupt frames remain null and cannot satisfy absence or cardinality', () => {
  const result = motion('A 😀', [{ text: 'foreign', streaming: true }, { text: 'A', streaming: false }, { text: 'A 😀', streaming: false }]);
  assert.deepEqual(result.frames.slice(0, 2), [null, null]);
  assert.equal(result.frames[2]!.minSourceUtf16, 4);
  assert.throws(() => motion('saved', [], ['foreign']), /saved-source prefix/);
  assert.throws(() => motion('x'.repeat(8001)), /bounded text/);
  assert.throws(() => motion('hello[](url)', [], ['hello[](', 'url)']), /Ambiguous visible arrival/);
});

test('all six source identities and seven installed identities are mandatory gates', () => {
  for (const field of Object.keys(expectedProjectionIdentity).filter(k => k !== 'dependencies')) {
    const ctx = context();
    Object.assign(ctx.identity, { [field]: '0'.repeat(64) });
    assert.throws(() => projectMarkdown({ answer: 'ok', frames: [], arrivals: [] }, ctx), /source identity/);
  }
  for (const field of Object.keys(expectedProjectionIdentity.dependencies)) {
    const ctx = context();
    Object.assign(ctx.identity.dependencies, { [field]: 'wrong' });
    assert.throws(() => projectMarkdown({ answer: 'ok', frames: [], arrivals: [] }, ctx), /installed renderer dependencies/);
  }
  const ctx = context();
  Object.assign(ctx.identity, { extra: 'unknown' });
  assert.throws(() => projectMarkdown({ answer: 'ok', frames: [], arrivals: [] }, ctx), /identity fields/);
});

test('strict data-only inputs enforce original frame/arrival/mode bounds', () => {
  for (const input of [
    { answer: '', frames: [], arrivals: [] },
    { answer: 'ok', frames: [], arrivals: [], command: 'true' },
    { answer: 'ok', frames: [{ text: 'ok', streaming: true, browser: {} }], arrivals: [] },
    { answer: 'ok', frames: Array(20001).fill({ text: '', streaming: true }), arrivals: [] },
    { answer: 'ok', frames: [], arrivals: Array(5001).fill('') },
    { answer: 'ok', frames: [], arrivals: [], mode: 'unknown' },
    { answer: 'ok', frames: [], arrivals: ['ok'], mode: 'terminal-full' },
  ]) assert.throws(() => projectMarkdown(input as MarkdownInput, context()));
  const atBounds = motion('A', Array(20000).fill({ text: '', streaming: true }), Array(5000).fill(''));
  assert.equal(atBounds.frames.length, 20000);
  assert.equal(atBounds.arrivals.length, 5000);
});
