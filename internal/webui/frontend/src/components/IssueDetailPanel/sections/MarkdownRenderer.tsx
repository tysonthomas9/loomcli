/**
 * MarkdownRenderer component.
 *
 * Renders markdown content with consistent styling. Content authored as HTML
 * (e.g. workspace design_format="html" designs) is rendered as sanitized HTML
 * directly via DOMPurify rather than through the react-markdown +
 * rehype-sanitize path, because rehype-sanitize's default schema strips inline
 * `<svg>` (and some presentation attributes), so HTML designs would lose their
 * diagrams. DOMPurify (sanitizeHtml) permits safe HTML — including
 * presentation-only SVG — so HTML-leading content goes straight to a sanitized
 * dangerouslySetInnerHTML, while markdown content keeps the react-markdown path.
 *
 * The leading-tag heuristic applies to ALL content, not just designs: anything
 * that begins with an HTML tag is treated as HTML (so markdown that starts with
 * a raw block tag is rendered as HTML, not parsed as markdown).
 */

import Markdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";

import { sanitizeHtml } from "@/utils/sanitizeHtml";
import styles from "./MarkdownRenderer.module.css";

export interface MarkdownRendererProps {
  /** Markdown content to render */
  content: string | undefined | null;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Heuristic: content that begins with a block/inline HTML tag is treated as
 * HTML (the planner emits HTML designs starting with `<h2>`/`<p>`/`<svg>`).
 * Markdown begins with text, `#`, list markers, etc.
 */
const HTML_LEADING_RE =
  /^\s*<(?:h[1-6]|p|ul|ol|li|div|section|article|table|thead|tbody|tr|td|th|pre|blockquote|svg|figure|img|hr|span|code|strong|em|b|i)\b/i;

const HTML_CHUNK_RE =
  /<(h[1-6]|p|ul|ol|li|div|section|article|table|thead|tbody|tr|td|th|pre|blockquote|svg|figure|span|code|strong|em|b|i|a|script|iframe|object|embed|form|style|math|mtext|mglyph)\b[^>]*>[\s\S]*?<\/\1>|<(?:img|hr)\b[^>]*\/?>/gi;

function renderMarkdownChunk(content: string, key: string): JSX.Element | null {
  if (!content.trim()) {
    return null;
  }
  return (
    <Markdown key={key} rehypePlugins={[rehypeSanitize]}>
      {sanitizeHtml(content)}
    </Markdown>
  );
}

function renderHtmlChunk(content: string, key: string): JSX.Element | null {
  const sanitizedContent = sanitizeHtml(content);
  if (!sanitizedContent.trim()) {
    return null;
  }
  return (
    <span key={key} dangerouslySetInnerHTML={{ __html: sanitizedContent }} />
  );
}

function renderMixedContent(content: string): Array<JSX.Element | null> {
  const chunks: Array<JSX.Element | null> = [];
  let lastIndex = 0;
  HTML_CHUNK_RE.lastIndex = 0;

  for (const match of content.matchAll(HTML_CHUNK_RE)) {
    if (match.index === undefined) {
      continue;
    }
    chunks.push(
      renderMarkdownChunk(
        content.slice(lastIndex, match.index),
        `md-${lastIndex}`,
      ),
    );
    chunks.push(renderHtmlChunk(match[0], `html-${match.index}`));
    lastIndex = match.index + match[0].length;
  }

  chunks.push(renderMarkdownChunk(content.slice(lastIndex), `md-${lastIndex}`));
  return chunks;
}

/**
 * MarkdownRenderer displays markdown content with consistent typography styles.
 * Handles empty/null content gracefully.
 */
export function MarkdownRenderer({
  content,
  className,
}: MarkdownRendererProps): JSX.Element {
  const rootClassName = [styles.markdown, className].filter(Boolean).join(" ");

  if (!content) {
    return (
      <div className={rootClassName} data-testid="markdown-empty">
        <p className={styles.empty}>No content</p>
      </div>
    );
  }

  const sanitizedContent = sanitizeHtml(content);

  // HTML designs render faithfully as sanitized HTML (incl. inline SVG); the
  // markdown renderer would escape the raw HTML and strip the SVG.
  if (HTML_LEADING_RE.test(content)) {
    return (
      <div
        className={rootClassName}
        data-testid="markdown-content"
        dangerouslySetInnerHTML={{ __html: sanitizedContent }}
      />
    );
  }

  HTML_CHUNK_RE.lastIndex = 0;
  if (HTML_CHUNK_RE.test(content)) {
    return (
      <div className={rootClassName} data-testid="markdown-content">
        {renderMixedContent(content)}
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="markdown-content">
      <Markdown rehypePlugins={[rehypeSanitize]}>{sanitizedContent}</Markdown>
    </div>
  );
}
