/**
 * MarkdownRenderer component.
 *
 * Renders markdown content with one consistent sanitization policy. Explicit
 * HTML design rendering belongs to DesignPanel and must not affect comments,
 * descriptions, or notes based on content sniffing.
 */

import { memo, useMemo } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";

import { sanitizeHtml } from "@/utils/sanitizeHtml";
import styles from "./MarkdownRenderer.module.css";

// Module scope: react-markdown rebuilds its processor whenever the plugin
// arrays change identity, so fresh literals would re-parse on every render.
const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeSanitize];

export interface MarkdownRendererProps {
  /** Markdown content to render */
  content: string | undefined | null;
  /** Additional CSS class name */
  className?: string | undefined;
}

/**
 * MarkdownRenderer displays markdown content with consistent typography styles.
 * Handles empty/null content gracefully. GFM (tables, strikethrough, task lists,
 * autolinks) is enabled via remark-gfm.
 *
 * Memoized because sanitizing and parsing markdown is expensive and callers
 * re-render on a poll (the PR reviewer chat) with the same string content.
 */
export const MarkdownRenderer = memo(function MarkdownRenderer({
  content,
  className,
}: MarkdownRendererProps): JSX.Element {
  const rootClassName = [styles.markdown, className].filter(Boolean).join(" ");
  const sanitizedContent = useMemo(
    () => (content ? sanitizeHtml(content) : ""),
    [content],
  );

  if (!content) {
    return (
      <div className={rootClassName} data-testid="markdown-empty">
        <p className={styles.empty}>No content</p>
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="markdown-content">
      <Markdown remarkPlugins={REMARK_PLUGINS} rehypePlugins={REHYPE_PLUGINS}>
        {sanitizedContent}
      </Markdown>
    </div>
  );
});
