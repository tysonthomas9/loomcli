// Ported from T3 Code apps/web/src/components/ChatMarkdown.tsx (and the table
// serializer in apps/web/src/markdown-clipboard.ts) at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
// Changes: the GFM, table and code-block parts only (no file links, workspace
// images, skills or alerts); Lezer highlighting in place of Shiki; CSS
// modules in place of Tailwind; raw HTML shows as literal text, as Loom's chat
// always has, instead of T3's sanitized HTML. Loom's own additions: the text
// renders block by block so streaming re-parses only the last block, and the
// streaming tail carries a caret and fades its newest words in.

import {
  Children,
  isValidElement,
  memo,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";
import Markdown, { type Components } from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import { highlight, highlightLanguage, loadLanguage } from "./codeHighlight";
import type { Token } from "./codeHighlight";
import { useCopy } from "./MessageCopyButton";
import type { FreshRun } from "./useSmoothText";
import styles from "./ChatMarkdown.module.css";

interface MdNode {
  type: string;
  children?: MdNode[];
}

/** The hast nodes the streaming tail touches. */
interface HNode {
  type: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: HNode[];
  position?: { start: { offset?: number }; end: { offset?: number } };
}

/** Raw HTML in a message is shown as the text it is, never as markup. */
function remarkHtmlAsText() {
  const walk = (node: MdNode) => {
    if (node.type === "html") node.type = "text";
    node.children?.forEach(walk);
  };
  return walk;
}

// Module scope: react-markdown rebuilds its processor when these change.
const REMARK_PLUGINS = [remarkGfm, remarkHtmlAsText];
const REHYPE_PLUGINS = [rehypeSanitize];

const CODE_FENCE_LANGUAGE_REGEX = /(?:^|\s)language-([^\s]+)/;

function extractFenceLanguage(className: string | undefined): string {
  return className?.match(CODE_FENCE_LANGUAGE_REGEX)?.[1] ?? "text";
}

function nodeToPlainText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeToPlainText).join("");
  if (isValidElement<{ children?: ReactNode }>(node))
    return nodeToPlainText(node.props.children);
  return "";
}

function extractCodeBlock(
  children: ReactNode,
): { className: string | undefined; code: string } | null {
  const nodes = Children.toArray(children);
  const only = nodes[0];
  if (
    nodes.length !== 1 ||
    !isValidElement<{ className?: string; children?: ReactNode }>(only) ||
    only.type !== "code"
  )
    return null;
  return {
    className: only.props.className,
    code: nodeToPlainText(only.props.children).replace(/\n$/, ""),
  };
}

/** The code's highlighted runs once its language loads; null until then. */
function useHighlight(
  code: string,
  language: string,
  streaming: boolean,
): Token[] | null {
  const [tokens, setTokens] = useState<Token[] | null>(null);
  useEffect(() => {
    setTokens(null);
    const name = highlightLanguage(language);
    if (streaming || !name) return;
    let live = true;
    void loadLanguage(name).then((lang) => {
      if (live && lang) setTokens(highlight(code, lang));
    });
    return () => {
      live = false;
    };
  }, [code, language, streaming]);
  return tokens;
}

function MarkdownCodeBlock({
  code,
  language,
  streaming,
}: {
  code: string;
  language: string;
  streaming: boolean;
}) {
  const { copied, copy } = useCopy();
  const [wrapped, setWrapped] = useState(false);
  const tokens = useHighlight(code, language, streaming);
  const wrapLabel = wrapped ? "Disable line wrap" : "Wrap lines";
  const copyLabel = copied ? "Copied" : "Copy code";
  return (
    <div
      className={styles.codeblock}
      data-language={language}
      data-wrap={wrapped ? "true" : "false"}
      data-testid="chat-codeblock"
    >
      <div className={styles.codeblockHeader}>
        <span className={styles.codeblockLanguage}>{language}</span>
        <span
          className={styles.toolbar}
          role="toolbar"
          aria-label="Code block actions"
        >
          <button
            type="button"
            className={styles.chromeAction}
            aria-pressed={wrapped}
            aria-label={wrapLabel}
            title={wrapLabel}
            onClick={() => setWrapped((w) => !w)}
          >
            Wrap
          </button>
          <button
            type="button"
            className={styles.chromeAction}
            aria-label={copyLabel}
            title={copyLabel}
            onClick={() => copy(code)}
          >
            {copied ? "Copied" : "Copy"}
          </button>
        </span>
      </div>
      <pre>
        <code>
          {tokens
            ? tokens.map((tk, i) =>
                tk.className ? (
                  <span key={i} className={tk.className}>
                    {tk.text}
                  </span>
                ) : (
                  tk.text
                ),
              )
            : code}
        </code>
      </pre>
    </div>
  );
}

function tableCellText(cell: Element): string {
  return (cell.textContent ?? "")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\|/g, "\\|");
}

function tableRows(table: Element): Element[][] {
  return [
    ...table.querySelectorAll(
      ":scope > thead > tr, :scope > tbody > tr, :scope > tr",
    ),
  ]
    .map((row) =>
      [...row.children].filter((c) => c.tagName === "TH" || c.tagName === "TD"),
    )
    .filter((cells) => cells.length > 0);
}

/** A rendered table as GFM markdown, its header row first. */
export function serializeTableElementToMarkdown(table: Element): string {
  const lines: string[] = [];
  tableRows(table).forEach((cells, i) => {
    lines.push(`| ${cells.map(tableCellText).join(" | ")} |`);
    if (i === 0) {
      const markers = cells.map((cell) => {
        const align =
          (cell as HTMLElement).style?.textAlign || cell.getAttribute("align");
        if (align === "center") return ":---:";
        if (align === "right") return "---:";
        return "---";
      });
      lines.push(`| ${markers.join(" | ")} |`);
    }
  });
  return lines.join("\n");
}

function csvCell(value: string): string {
  const v = value.replace(/\s+/g, " ").trim();
  return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}

/** A rendered table as CSV. */
export function serializeTableElementToCsv(table: Element): string {
  return tableRows(table)
    .map((cells) => cells.map((c) => csvCell(c.textContent ?? "")).join(","))
    .join("\n");
}

function MarkdownTable({ children, ...props }: ComponentProps<"table">) {
  const tableRef = useRef<HTMLTableElement | null>(null);
  const [expanded, setExpanded] = useState(false);
  const { copied, copy } = useCopy();
  const expandLabel = expanded ? "Collapse table cells" : "Expand table cells";
  const copyAs = (format: "markdown" | "csv") => {
    const table = tableRef.current;
    if (!table) return;
    copy(
      format === "markdown"
        ? serializeTableElementToMarkdown(table)
        : serializeTableElementToCsv(table),
    );
  };
  return (
    <div
      className={styles.tableContainer}
      data-expanded={expanded ? "true" : "false"}
    >
      <div className={styles.tableScroll}>
        <table ref={tableRef} {...props}>
          {children}
        </table>
      </div>
      <div className={styles.tableActions}>
        <button
          type="button"
          className={styles.chromeAction}
          aria-pressed={expanded}
          aria-label={expandLabel}
          title={expandLabel}
          onClick={() => setExpanded((e) => !e)}
        >
          {expanded ? "Collapse" : "Expand"}
        </button>
        <span className={styles.toolbar}>
          <button
            type="button"
            className={styles.chromeAction}
            onClick={() => copyAs("markdown")}
          >
            {copied ? "Copied" : "Copy as Markdown"}
          </button>
          <button
            type="button"
            className={styles.chromeAction}
            onClick={() => copyAs("csv")}
          >
            Copy as CSV
          </button>
        </span>
      </div>
    </div>
  );
}

function components(streaming: boolean): Components {
  return {
    pre({ children, node: _node, ...props }) {
      const block = extractCodeBlock(children);
      if (!block) return <pre {...props}>{children}</pre>;
      return (
        <MarkdownCodeBlock
          code={block.code}
          language={extractFenceLanguage(block.className)}
          streaming={streaming}
        />
      );
    },
    table({ node: _node, ...props }) {
      return <MarkdownTable {...props} />;
    },
    a({ node: _node, ...props }) {
      return <a {...props} target="_blank" rel="noopener noreferrer" />;
    },
  };
}

const STATIC = components(false);
const STREAMING = components(true);

// Text that a cut could change: reference links, and HTML blocks that run
// past a blank line (comments, <pre>, <script> and the like).
const WHOLE = /^ {0,3}(\[[^\]]+\]:|<([!?]|script|pre|style|textarea))/im;
const FENCE = /^ {0,3}(`{3,}|~{3,})/;
// A line after a blank line that may still belong to the block above.
const CONTINUES = /^(\s|[-*+>|]|\d+[.)](\s|$))/;

/**
 * The text cut into top-level blocks that render the same alone as
 * together: a cut falls only at a blank line outside a code fence, before a
 * line that cannot continue a list, quote or table. Text with reference
 * links or long HTML blocks stays whole.
 */
export function splitBlocks(text: string): string[] {
  if (WHOLE.test(text)) return [text];
  const blocks: string[] = [];
  let start = 0;
  let fence = "";
  let blank = false;
  for (let i = 0; i < text.length; ) {
    const nl = text.indexOf("\n", i);
    const line = text.slice(i, nl === -1 ? text.length : nl);
    const m = FENCE.exec(line);
    const marker = m?.[1] ?? "";
    if (fence) {
      if (
        marker[0] === fence[0] &&
        marker.length >= fence.length &&
        !line.slice(m?.[0].length).trim()
      )
        fence = "";
    } else if (!line.trim()) {
      blank = true;
    } else {
      if (blank && !CONTINUES.test(line)) {
        blocks.push(text.slice(start, i));
        start = i;
      }
      blank = false;
      fence = marker;
    }
    i = nl === -1 ? text.length : nl + 1;
  }
  blocks.push(text.slice(start));
  return blocks;
}

/** Wraps the text from each fresh run's start in a span at its opacity. */
function fadeRuns(node: HNode, runs: readonly FreshRun[]) {
  if (!node.children) return;
  node.children = node.children.flatMap((child): HNode[] => {
    if (child.type !== "text") {
      fadeRuns(child, runs);
      return [child];
    }
    const s = child.position?.start.offset;
    const e = child.position?.end.offset;
    const value = child.value ?? "";
    if (s === undefined || e === undefined || e - s !== value.length)
      return [child];
    const cuts = [s, ...runs.map((r) => r.from).filter((f) => f > s && f < e)];
    return cuts.map((from, i) => {
      const piece = value.slice(from - s, (cuts[i + 1] ?? e) - s);
      const run = runs.filter((r) => r.from <= from).pop();
      if (!run || run.opacity >= 1) return { type: "text", value: piece };
      return {
        type: "element",
        tagName: "span",
        properties: { dataFresh: "", style: `opacity:${run.opacity}` },
        children: [{ type: "text", value: piece }],
      };
    });
  });
}

/** Puts the caret at the end of the last element with text. */
function appendCaret(root: HNode) {
  let node = root;
  for (;;) {
    const last = [...(node.children ?? [])]
      .reverse()
      .find((c) => c.type === "element" || c.value?.trim());
    if (
      last?.type !== "element" ||
      last.tagName === "pre" ||
      last.properties?.dataFresh !== undefined
    )
      break;
    node = last;
  }
  (node.children ??= []).push({
    type: "element",
    tagName: "span",
    properties: {
      className: [styles.caret],
      dataStreamingCaret: "",
      ariaHidden: "true",
    },
    children: [],
  });
}

/** The streaming tail: fresh runs, from the block's start, and the caret. */
interface Tail {
  fresh: readonly FreshRun[];
}

const MarkdownBlock = memo(function MarkdownBlock({
  text,
  streaming,
  tail,
}: {
  text: string;
  streaming: boolean;
  tail?: Tail | undefined;
}) {
  const rehype = tail
    ? [
        ...REHYPE_PLUGINS,
        () => (tree: HNode) => {
          if (tail.fresh.length) fadeRuns(tree, tail.fresh);
          appendCaret(tree);
        },
      ]
    : REHYPE_PLUGINS;
  return (
    <Markdown
      remarkPlugins={REMARK_PLUGINS}
      rehypePlugins={rehype}
      components={streaming ? STREAMING : STATIC}
    >
      {text}
    </Markdown>
  );
});

/**
 * An agent's markdown: GFM (tables, task lists, strikethrough, autolinks),
 * code blocks with their language, highlighting, wrap and copy, and tables
 * that copy as Markdown or CSV. It renders block by block, so a growing
 * message re-parses only its last block. While the text streams, code is
 * not highlighted, a caret ends the text and `fresh` runs fade in.
 */
export const ChatMarkdown = memo(function ChatMarkdown({
  text,
  streaming = false,
  fresh = NO_RUNS,
}: {
  text: string;
  streaming?: boolean;
  fresh?: readonly FreshRun[];
}) {
  const blocks = useMemo(() => splitBlocks(text), [text]);
  const lastStart = text.length - (blocks[blocks.length - 1] ?? "").length;
  return (
    <div className={styles.markdown} data-testid="chat-markdown">
      {blocks.map((block, i) => (
        <MarkdownBlock
          key={i}
          text={block}
          streaming={streaming}
          tail={
            streaming && i === blocks.length - 1
              ? {
                  fresh: fresh.map((r) => ({
                    ...r,
                    from: Math.max(0, r.from - lastStart),
                  })),
                }
              : undefined
          }
        />
      ))}
    </div>
  );
});

const NO_RUNS: readonly FreshRun[] = [];
