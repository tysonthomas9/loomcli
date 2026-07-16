/**
 * MarkdownRenderer component.
 *
 * Renders markdown content with one consistent sanitization policy. Explicit
 * HTML design rendering belongs to DesignPanel and must not affect comments,
 * descriptions, or notes based on content sniffing.
 */

import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";

import { sanitizeHtml } from "@/utils/sanitizeHtml";
import styles from "./MarkdownRenderer.module.css";

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

  return (
    <div className={rootClassName} data-testid="markdown-content">
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
        {sanitizedContent}
      </Markdown>
    </div>
  );
}
