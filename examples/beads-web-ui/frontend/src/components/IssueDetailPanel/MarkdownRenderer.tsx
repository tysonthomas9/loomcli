/**
 * MarkdownRenderer component.
 * Renders markdown content with consistent styling.
 */

import Markdown from 'react-markdown';

import styles from './MarkdownRenderer.module.css';

export interface MarkdownRendererProps {
  /** Markdown content to render */
  content: string | undefined | null;
  /** Additional CSS class name */
  className?: string;
}

/**
 * MarkdownRenderer displays markdown content with consistent typography styles.
 * Handles empty/null content gracefully.
 */
export function MarkdownRenderer({ content, className }: MarkdownRendererProps): JSX.Element {
  const rootClassName = [styles.markdown, className].filter(Boolean).join(' ');

  if (!content) {
    return (
      <div className={rootClassName} data-testid="markdown-empty">
        <p className={styles.empty}>No content</p>
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="markdown-content">
      <Markdown>{content}</Markdown>
    </div>
  );
}
