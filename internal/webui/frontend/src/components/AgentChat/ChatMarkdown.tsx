// Ported from T3 Code apps/web/src/components/ChatMarkdown.tsx (and the table
// serializer in apps/web/src/markdown-clipboard.ts) at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
// Changes: the GFM, table and code-block parts only (no file links, workspace
// images, skills or alerts); Lezer highlighting in place of Shiki; CSS
// modules in place of Tailwind; raw HTML shows as literal text, as Loom's chat
// always has, instead of T3's sanitized HTML.

import {
  Children,
  isValidElement,
  memo,
  useEffect,
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
import styles from "./ChatMarkdown.module.css";

interface MdNode {
  type: string;
  children?: MdNode[];
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

/**
 * An agent's markdown: GFM (tables, task lists, strikethrough, autolinks),
 * code blocks with their language, highlighting, wrap and copy, and tables
 * that copy as Markdown or CSV. While the text streams, code is not
 * highlighted.
 */
export const ChatMarkdown = memo(function ChatMarkdown({
  text,
  streaming = false,
}: {
  text: string;
  streaming?: boolean;
}) {
  return (
    <div className={styles.markdown} data-testid="chat-markdown">
      <Markdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={REHYPE_PLUGINS}
        components={streaming ? STREAMING : STATIC}
      >
        {text}
      </Markdown>
    </div>
  );
});
