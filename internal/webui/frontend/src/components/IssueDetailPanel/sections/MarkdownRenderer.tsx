/**
 * MarkdownRenderer component.
 * Renders markdown content with consistent styling.
 */

import { memo, useMemo } from "react";
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

// memo + useMemo because transcript-streaming parents re-render on every
// event, but Markdown content usually isn't changing on each tick — reparsing
// 10 KB of prompt every render shows up in profiles.
function MarkdownRendererImpl({
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
      <Markdown rehypePlugins={[rehypeSanitize]}>{sanitizedContent}</Markdown>
    </div>
  );
}

export const MarkdownRenderer = memo(MarkdownRendererImpl);
