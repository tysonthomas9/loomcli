import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkRehype from 'remark-rehype';
import rehypeSanitize from 'rehype-sanitize';
import type { Root as MdRoot } from 'mdast';
import type { Root as HtmlRoot, RootContent } from 'hast';
import { boundedText, projectRendererTree, requireProjection, type RendererNode, type RendererText } from './text.js';
import { validateProjectionIdentity, type ProjectionContext, type ProjectionIdentity } from './identity.js';

export const markdownProjectionContract = 'loom-chat-markdown-projection-v1' as const;
export interface MarkdownInput {
  answer: string; frames: { text: string; streaming: boolean }[]; arrivals: string[]; mode?: 'terminal-full';
}
export interface FrameProjection {
  minSourceUtf16: number; maxSourceUtf16: number; visibleWords: number; visible: string; content: string; contentWords: number;
}
export interface ArrivalProjection {
  sourceUtf16: number; visibleChanged: boolean; contentChanged: boolean; requiredMinSourceUtf16: number;
  projectedUtf16: number; projectedWords: number;
}
export interface MotionProjection {
  version: 1; terminal: string; terminalVisible: string; terminalContent: string;
  frames: (FrameProjection | null)[]; arrivals: ArrivalProjection[]; sourceUtf16: number; identity: ProjectionIdentity;
}
export interface TerminalProjection {
  version: 1; mode: 'terminal-full'; sourceUtf16: number; terminal: string; chatMarkdownSha256: string; cssSha256: string;
}

// Raw HTML becomes inert text before Markdown->HAST, never executable markup.
function literalHtml() {
  return (root: MdRoot) => {
    const visit = (node: { type: string; children?: unknown[] }) => {
      if (node.type === 'html') node.type = 'text';
      node.children?.forEach(child => visit(child as { type: string; children?: unknown[] }));
    };
    visit(root);
  };
}
const processor = unified().use(remarkParse).use(remarkGfm).use(literalHtml)
  .use(remarkRehype, { allowDangerousHtml: true }).use(rehypeSanitize);
const text = (value: string): RendererNode => ({ kind: 'text', text: value });
const element = (tag: Extract<RendererNode, { kind: 'element' }>['tag'], children: RendererNode[],
  options: Pick<Extract<RendererNode, { kind: 'element' }>, 'chrome' | 'separator'> = {}): RendererNode =>
  ({ kind: 'element', tag, children, ...options });

// Source-shaped block boundaries from the pinned component contract. This is
// a Markdown processor input partition, not a general-purpose hand parser.
export function partitionMarkdown(text: string): string[] {
  boundedText(text);
  if (/^ {0,3}(\[[^\]]+\]:|<([!?]|script|pre|style|textarea))/im.test(text)) return [text];
  const blocks: string[] = [];
  let start = 0, fence = '', blank = false;
  for (let at = 0; at < text.length;) {
    const newline = text.indexOf('\n', at), line = text.slice(at, newline === -1 ? text.length : newline);
    const match = /^ {0,3}(`{3,}|~{3,})/.exec(line), marker = match?.[1] ?? '';
    if (fence) {
      if (marker[0] === fence[0] && marker.length >= fence.length && !line.slice(match?.[0].length).trim()) fence = '';
    } else if (!line.trim()) blank = true;
    else {
      if (blank && !/^(\s|[-*+>|]|\d+[.)](\s|$))/.test(line)) { blocks.push(text.slice(start, at)); start = at; }
      blank = false; fence = marker;
    }
    at = newline === -1 ? text.length : newline + 1;
  }
  blocks.push(text.slice(start));
  return blocks;
}

function astNode(node: RootContent): RendererNode {
  if (node.type === 'text') return text(node.value);
  requireProjection(node.type === 'element', 'unsupported-input', 'Unsupported sanitized AST node');
  if (node.tagName === 'pre' && node.children.length === 1 && node.children[0]?.type === 'element' && node.children[0].tagName === 'code') {
    const code = node.children[0];
    const plain = (n: RootContent): string => n.type === 'text' ? n.value : n.type === 'element' ? n.children.map(plain).join('') : '';
    const classes = code.properties.className;
    const language = (Array.isArray(classes) ? classes.join(' ') : String(classes ?? '')).match(/(?:^|\s)language-([^\s]+)/)?.[1] ?? 'text';
    return element('DIV', [element('DIV', [text(language), element('SPAN', [element('BUTTON', [text('Wrap')]),
      element('BUTTON', [text('Copy')])], { separator: 'toolbar' })], { chrome: 'code-header', separator: 'codeblockHeader' }),
    element('PRE', [element('CODE', [text(code.children.map(plain).join('').replace(/\n$/, ''))])])]);
  }
  // React's table runtime omits inter-element whitespace under these parents.
  // Keep ordinary paragraph/list soft newlines; they contribute to raw text.
  const children = node.children.filter(child => !['table', 'thead', 'tbody', 'tr'].includes(node.tagName) ||
    child.type !== 'text' || /[^\t\n\r ]/.test(child.value)).map(astNode);
  if (node.tagName === 'table') return element('DIV', [element('DIV', [element('TABLE', children)]),
    element('DIV', [element('BUTTON', [text('Expand')]), element('SPAN', [element('BUTTON', [text('Copy as Markdown')]),
      element('BUTTON', [text('Copy as CSV')])], { separator: 'toolbar' })], { chrome: 'table-actions', separator: 'tableActions' })]);
  return element(node.tagName.toUpperCase() as Extract<RendererNode, { kind: 'element' }>['tag'], children);
}

function renderSource(source: string, streaming: boolean): RendererText {
  const children: RendererNode[] = [];
  for (const block of partitionMarkdown(source)) {
    const root = processor.runSync(processor.parse(block)) as HtmlRoot;
    children.push(...root.children.map(astNode));
  }
  // Caret/fresh/highlight spans have no extra text; zero-text caret is omitted.
  return projectRendererTree({ complete: true, streaming, tree: element('DIV', children) });
}

export function projectMarkdown(input: MarkdownInput, context: ProjectionContext): MotionProjection | TerminalProjection {
  const identity = validateProjectionIdentity(context?.identity);
  requireProjection(input && typeof input === 'object' && Object.keys(input).every(k => ['answer', 'frames', 'arrivals', 'mode'].includes(k)),
    'invalid-input', 'Unknown Markdown input field');
  const terminalOnly = input.mode === 'terminal-full';
  requireProjection(input.mode === undefined || terminalOnly, 'unsupported-input', 'Invalid projection mode');
  boundedText(input.answer, terminalOnly ? 128000 : 8000);
  requireProjection(input.answer.length > 0 && Array.isArray(input.frames) && input.frames.length <= 20000 &&
    Array.isArray(input.arrivals) && input.arrivals.length <= 5000, 'invalid-input', 'Invalid projection input');
  input.frames.forEach(frame => {
    requireProjection(frame && Object.keys(frame).length === 2 && Object.keys(frame).every(k => k === 'text' || k === 'streaming') &&
      typeof frame.streaming === 'boolean', 'invalid-input', 'Invalid frame');
    boundedText(frame.text);
  });
  input.arrivals.forEach(delta => boundedText(delta));
  requireProjection(!terminalOnly || (!input.frames.length && !input.arrivals.length), 'invalid-input', 'Terminal mode has motion evidence');
  const terminal = renderSource(input.answer, false);
  if (terminalOnly) return { version: 1, mode: 'terminal-full', sourceUtf16: input.answer.length,
    terminal: terminal.raw, chatMarkdownSha256: identity.chatMarkdownSha256, cssSha256: identity.cssSha256 };
  const raw = new Map<string, [number, number]>(), visible = new Map<string, [number, number]>(), content = new Map<string, [number, number]>();
  const projections: RendererText[] = [];
  for (let at = 0; at <= input.answer.length; at++) {
    const projection = renderSource(input.answer.slice(0, at), true);
    projections.push(projection);
    for (const [map, value] of [[raw, projection.raw], [visible, projection.visible], [content, projection.content]] as const) {
      const range = map.get(value);
      if (range) range[1] = at; else map.set(value, [at, at]);
    }
  }
  let cumulative = '', previous = projections[0]!.visible, previousContent = projections[0]!.content;
  const arrivals = input.arrivals.map(delta => {
    const priorLength = cumulative.length;
    cumulative += delta;
    requireProjection(input.answer.startsWith(cumulative), 'invalid-input', 'Arrival is not a saved-source prefix');
    const projection = projections[cumulative.length]!;
    const visibleRange = visible.get(projection.visible)!, contentRange = content.get(projection.content)!;
    requireProjection(projection.visible === previous || visibleRange[0] > priorLength, 'invalid-input', 'Ambiguous visible arrival projection');
    requireProjection(projection.content === previousContent || contentRange[0] > priorLength, 'invalid-input', 'Ambiguous content arrival projection');
    const row = { sourceUtf16: cumulative.length, visibleChanged: projection.visible !== previous,
      contentChanged: projection.content !== previousContent, requiredMinSourceUtf16: contentRange[0],
      projectedUtf16: projection.visible.length, projectedWords: projection.contentWords };
    previous = projection.visible; previousContent = projection.content;
    return row;
  });
  const frameProjection = (projection: RendererText, range: [number, number]): FrameProjection => ({
    minSourceUtf16: range[0], maxSourceUtf16: range[1], visibleWords: projection.visibleWords,
    visible: projection.visible, content: projection.content, contentWords: projection.contentWords,
  });
  const frames = input.frames.map(frame => {
    if (!frame.streaming) return frame.text === terminal.raw ? frameProjection(terminal, [input.answer.length, input.answer.length]) : null;
    const range = raw.get(frame.text);
    if (!range) return null;
    const first = projections[range[0]]!, last = projections[range[1]]!;
    return first.visible === last.visible && first.content === last.content ? frameProjection(first, range) : null;
  });
  return { version: 1, terminal: terminal.raw, terminalVisible: terminal.visible, terminalContent: terminal.content,
    frames, arrivals, sourceUtf16: input.answer.length, identity };
}
