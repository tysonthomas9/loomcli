// Pure data operations. This module neither parses Markdown nor observes a DOM.
// A renderer-tree projection alone does not establish saved-source/DOM parity.
export class ProjectionError extends Error {
  constructor(readonly code: 'invalid-input' | 'incomplete-observation' | 'unsupported-input', message: string) {
    super(message);
  }
}

export function requireProjection(condition: unknown, code: ProjectionError['code'], message: string): asserts condition {
  if (!condition) throw new ProjectionError(code, message);
}

export function boundedText(value: unknown, maximum = 128000): asserts value is string {
  requireProjection(typeof value === 'string' && value.length <= maximum, 'invalid-input', 'Expected bounded text');
}

export function utf16Length(text: string): number {
  boundedText(text);
  return text.length;
}

const pythonWhitespace = /[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/u;
export function words(text: string): string[] {
  boundedText(text, 4000000);
  return text.split(pythonWhitespace).filter(Boolean);
}
export function javascriptWords(text: string): string[] {
  boundedText(text, 4000000);
  return text.trim().split(/\s+/).filter(Boolean);
}

export function reasoningPreview(text: string, units: 'utf16' | 'python-codepoints'): string {
  boundedText(text);
  requireProjection(units === 'utf16' || units === 'python-codepoints', 'unsupported-input', 'Unknown preview length contract');
  // Python's original oracle and the product's JS firstLine differ for non-BMP
  // text near the cap. Keep both named semantics; the caller must choose.
  const trim = (s: string) => units === 'utf16' ? s.trim() :
    s.replace(/^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/gu, '');
  let line = (trim(text).split('\n')[0] ?? '').replace(units === 'utf16' ? /^#{1,6}\s+/ :
    /^#{1,6}[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/u, '');
  for (;;) {
    const next = line.replace(units === 'utf16' ? /(\*\*|__|\*|_|`)(.+?)\1/g : /(\*\*|__|\*|_|`)([^\n]+?)\1/g, '$2');
    if (next === line) break;
    line = next;
  }
  line = trim(line);
  if (units === 'utf16') return line.length > 120 ? line.slice(0, 119) + '…' : line;
  const points = [...line];
  return points.length > 120 ? points.slice(0, 119).join('') + '…' : line;
}

export interface TextObservation { complete: boolean; text: string }
export interface TextComparison { expected: string; observed: string; equal: boolean }
export function compareText(expected: string, observation: TextObservation, mode: 'exact' | 'python-words'): TextComparison {
  boundedText(expected);
  requireProjection(observation?.complete === true, 'incomplete-observation', 'Text observation is incomplete');
  boundedText(observation.text);
  requireProjection(mode === 'exact' || mode === 'python-words', 'unsupported-input', 'Unknown comparison mode');
  return { expected, observed: observation.text, equal: mode === 'exact' ? expected === observation.text :
    JSON.stringify(words(expected)) === JSON.stringify(words(observation.text)) };
}

const tags = ['DIV', 'P', 'PRE', 'UL', 'OL', 'LI', 'BLOCKQUOTE', 'SECTION', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6',
  'SPAN', 'BUTTON', 'A', 'CODE', 'STRONG', 'EM', 'DEL', 'TABLE', 'THEAD', 'TBODY', 'TR', 'TH', 'TD',
  'BR', 'IMG', 'HR', 'SCRIPT', 'STYLE', 'SVG', 'INPUT', 'SUP', 'TFOOT'] as const;
type Tag = typeof tags[number];
export type RendererNode = { kind: 'text'; text: string } | {
  kind: 'element'; tag: Tag; children: RendererNode[];
  chrome?: 'code-header' | 'table-actions';
  separator?: 'toolbar' | 'codeblockHeader' | 'tableActions';
};
export interface RendererTreeObservation { complete: boolean; streaming: boolean; tree: RendererNode }
export interface RendererText { raw: string; visible: string; content: string; visibleWords: number; contentWords: number }
const boundaries = new Set<string>(tags.slice(0, 14));
const excluded = new Set<string>(['SCRIPT', 'STYLE', 'SVG', 'INPUT']);

export function projectRendererTree(input: RendererTreeObservation): RendererText {
  requireProjection(input?.complete === true, 'incomplete-observation', 'Renderer tree is incomplete');
  requireProjection(typeof input.streaming === 'boolean', 'invalid-input', 'Streaming flag is required');
  let nodes = 0, textUnits = 0;
  const seen = new Set<object>();
  const validate = (node: RendererNode, depth: number): void => {
    requireProjection(node && typeof node === 'object' && !seen.has(node) && depth <= 512 && ++nodes <= 524288,
      'invalid-input', 'Invalid or over-budget renderer tree');
    seen.add(node);
    if (node.kind === 'text') {
      requireProjection(Object.keys(node).every(k => k === 'kind' || k === 'text'), 'unsupported-input', 'Unknown text field');
      boundedText(node.text);
      textUnits += node.text.length;
      requireProjection(textUnits <= 4000000, 'invalid-input', 'Renderer text budget exceeded');
      return;
    }
    requireProjection(node.kind === 'element' && tags.includes(node.tag) && Array.isArray(node.children) && node.children.length <= 128000,
      'unsupported-input', 'Unknown renderer element');
    requireProjection(Object.keys(node).every(k => ['kind', 'tag', 'children', 'chrome', 'separator'].includes(k)) &&
      (node.chrome === undefined || ['code-header', 'table-actions'].includes(node.chrome)) &&
      (node.separator === undefined || ['toolbar', 'codeblockHeader', 'tableActions'].includes(node.separator)),
      'unsupported-input', 'Unknown renderer semantics');
    requireProjection(!['BR', 'IMG', 'HR', 'INPUT'].includes(node.tag) || node.children.length === 0,
      'invalid-input', 'Void element has children');
    node.children.forEach(child => validate(child, depth + 1));
  };
  validate(input.tree, 0);
  const raw = (node: RendererNode): string => node.kind === 'text' ? node.text : node.children.map(raw).join('');
  const shown = (node: RendererNode, replyOnly: boolean): string => {
    if (node.kind === 'text') return node.text;
    if (replyOnly && node.chrome !== undefined) return '';
    if (input.streaming && node.separator === 'tableActions') return '';
    if (excluded.has(node.tag)) return '';
    if (node.tag === 'BR') return '\n';
    if (node.tag === 'TABLE') {
      const rows: RendererNode[] = [];
      const collect = (n: RendererNode): void => {
        if (n.kind !== 'element') return;
        if (n.tag === 'TR') rows.push(n);
        n.children.forEach(collect);
      };
      node.children.forEach(collect);
      return rows.map(row => shown(row, replyOnly)).join('\n');
    }
    if (node.tag === 'TR') return node.children.filter(child => child.kind === 'element' && ['TH', 'TD'].includes(child.tag))
      .map(cell => shown(cell, replyOnly)).join('\t');
    const body = node.children.map(child => shown(child, replyOnly)).join(node.separator === undefined ? '' : '\n');
    return boundaries.has(node.tag) ? `\n${body}\n` : body;
  };
  const normalize = (value: string) => value.trim().replace(/\n{3,}/g, '\n\n');
  const visible = normalize(shown(input.tree, false)), content = normalize(shown(input.tree, true));
  return { raw: raw(input.tree), visible, content, visibleWords: javascriptWords(visible).length, contentWords: javascriptWords(content).length };
}

export interface PlainFrame { text: string; streaming: boolean }
export interface PlainSourceInput {
  saved: string; complete: boolean; frames: PlainFrame[]; arrivals: string[]; final: string;
}
export function projectPlainSource(input: PlainSourceInput) {
  requireProjection(input?.complete === true, 'incomplete-observation', 'Saved source is incomplete');
  boundedText(input.saved, 8000);
  requireProjection(input.saved.length > 0 && !/[\n\r`*_~|#<>\[\]\\!]/.test(input.saved),
    'unsupported-input', 'Markdown source needs an independent parser and renderer parity seam');
  requireProjection(Array.isArray(input.frames) && input.frames.length <= 20000 &&
    Array.isArray(input.arrivals) && input.arrivals.length <= 5000, 'invalid-input', 'Invalid motion inputs');
  boundedText(input.final, 8000);
  requireProjection(input.final === input.saved, 'invalid-input', 'Final text differs from saved source');
  let cumulative = '', previous = '';
  const arrivals = input.arrivals.map(delta => {
    boundedText(delta, 8000);
    cumulative += delta;
    requireProjection(input.saved.startsWith(cumulative), 'invalid-input', 'Arrival is not a saved-source prefix');
    const row = { sourceUtf16: cumulative.length, visibleChanged: cumulative !== previous, contentChanged: cumulative !== previous,
      requiredMinSourceUtf16: cumulative.length, projectedUtf16: cumulative.length, projectedWords: words(cumulative).length };
    previous = cumulative;
    return row;
  });
  const frames = input.frames.map(frame => {
    boundedText(frame?.text, 8000);
    requireProjection(typeof frame.streaming === 'boolean' && input.saved.startsWith(frame.text),
      'invalid-input', 'Frame is not a saved-source prefix');
    return { minSourceUtf16: frame.text.length, maxSourceUtf16: frame.text.length, visible: frame.text, content: frame.text,
      visibleWords: words(frame.text).length, contentWords: words(frame.text).length };
  });
  requireProjection(frames.every((row, at) => at === 0 || row.minSourceUtf16 >= frames[at - 1]!.minSourceUtf16),
    'invalid-input', 'Source projection moved backward');
  return { version: 1 as const, sourceUtf16: input.saved.length, terminal: input.saved, terminalVisible: input.saved,
    terminalContent: input.saved, identity: { mode: 'plain-source-byte-exact' as const }, arrivals, frames };
}
