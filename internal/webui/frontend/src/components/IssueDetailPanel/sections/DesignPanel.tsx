/**
 * DesignPanel component.
 * Renders design markdown with collapsible H2 sections, fullscreen toggle,
 * independent scrolling, empty placeholder, and XSS sanitization.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";

import { MarkdownRenderer } from "./MarkdownRenderer";
import { sanitizeHtml } from "@/utils/sanitizeHtml";
import styles from "./DesignPanel.module.css";

export interface DesignPanelProps {
  /** Markdown design content */
  content: string | null | undefined;
  /** Durable format from FleetDB artifact metadata. */
  format?: "markdown" | "html" | string;
  /** Additional CSS class name */
  className?: string;
}

interface Section {
  heading: string;
  content: string;
}

/**
 * Match an HTML `<h2>...</h2>` heading occupying a whole line.
 * Captures the inner content (which may contain nested inline tags).
 */
const HTML_H2_RE = /^\s*<h2(?:\s[^>]*)?>(.*?)<\/h2>\s*$/i;

/**
 * Extract a section heading from a line, recognizing both the Markdown
 * `## ` syntax and a whole-line HTML `<h2>` element. Returns the heading
 * text (inline HTML tags stripped) or null if the line is not a heading.
 */
function matchHeading(line: string): string | null {
  const mdMatch = line.match(/^## (.+?)\s*$/);
  if (mdMatch) {
    return mdMatch[1] ?? "";
  }
  const htmlMatch = line.match(HTML_H2_RE);
  if (htmlMatch) {
    // Strip any nested inline tags so the heading renders as plain text.
    return (htmlMatch[1] ?? "").replace(/<[^>]+>/g, "").trim();
  }
  return null;
}

/**
 * Split design content into sections delimited by H2 headings. Recognizes
 * both Markdown `## ` headings and whole-line HTML `<h2>` elements so that
 * HTML designs keep the same collapsible-section UX as Markdown designs.
 * Content before the first H2 becomes a section with an empty heading.
 */
export function splitIntoSections(markdown: string): Section[] {
  const lines = markdown.split("\n");
  const sections: Section[] = [];
  let currentHeading = "";
  let currentLines: string[] = [];

  for (const line of lines) {
    const heading = matchHeading(line);
    if (heading !== null) {
      // Save previous section if it has content
      if (currentLines.length > 0 || currentHeading) {
        sections.push({
          heading: currentHeading,
          content: currentLines.join("\n").trim(),
        });
      }
      currentHeading = heading;
      currentLines = [];
    } else {
      currentLines.push(line);
    }
  }

  // Push final section
  if (currentLines.length > 0 || currentHeading) {
    sections.push({
      heading: currentHeading,
      content: currentLines.join("\n").trim(),
    });
  }

  return sections;
}

/**
 * DesignPanel displays design markdown with collapsible H2 sections,
 * fullscreen toggle, and independent scrolling.
 */
export function DesignPanel({
  content,
  format,
  className,
}: DesignPanelProps): JSX.Element {
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [collapsedSections, setCollapsedSections] = useState<Set<number>>(
    () => new Set(),
  );

  const sections = useMemo(
    () => (content ? splitIntoSections(content) : []),
    [content],
  );

  const hasH2Sections = sections.some((s) => s.heading !== "");
  // Compatibility only for legacy inline HTML that predates format metadata.
  const renderAsHTML =
    format === "html" ||
    (!format &&
      /^\s*<(?:h[1-6]|p|ul|ol|div|section|table|pre|svg|figure)\b/i.test(
        content ?? "",
      ));
  const renderDesignContent = (body: string, key?: string) =>
    renderAsHTML ? (
      <div
        key={key}
        data-testid="design-html-content"
        dangerouslySetInnerHTML={{ __html: sanitizeHtml(body) }}
      />
    ) : (
      <MarkdownRenderer key={key} content={body} />
    );

  // Reset collapsed sections when content changes (e.g., different issue selected)
  useEffect(() => {
    setCollapsedSections(new Set());
    setIsFullscreen(false);
  }, [content]);

  // Body scroll lock for fullscreen mode — save and restore previous value
  useEffect(() => {
    if (!isFullscreen) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [isFullscreen]);

  // Escape key exits fullscreen
  useEffect(() => {
    if (!isFullscreen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        setIsFullscreen(false);
      }
    };

    document.addEventListener("keydown", handleKeyDown, true);
    return () => document.removeEventListener("keydown", handleKeyDown, true);
  }, [isFullscreen]);

  const handleToggleFullscreen = useCallback(
    (event: React.MouseEvent<HTMLButtonElement>) => {
      event.stopPropagation();
      setIsFullscreen((prev) => !prev);
    },
    [],
  );

  const handleToggleSection = useCallback((index: number) => {
    setCollapsedSections((prev) => {
      const next = new Set(prev);
      if (next.has(index)) {
        next.delete(index);
      } else {
        next.add(index);
      }
      return next;
    });
  }, []);

  const rootClassName = [
    styles.designPanel,
    isFullscreen ? styles.fullscreen : undefined,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  // Empty placeholder
  if (!content) {
    return (
      <div
        className={[styles.designPanel, className].filter(Boolean).join(" ")}
        data-testid="design-panel"
      >
        <h3 className={styles.designTitle}>Design</h3>
        <div className={styles.emptyPlaceholder} data-testid="design-empty">
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path
              d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6z"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <path
              d="M14 2v6h6M16 13H8M16 17H8M10 9H8"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <p className={styles.emptyText}>No design yet</p>
        </div>
      </div>
    );
  }

  const panel = (
    <div className={rootClassName} data-testid="design-panel">
      <div className={styles.designHeader}>
        <h3 className={styles.designTitle}>Design</h3>
        <button
          type="button"
          className={styles.fullscreenButton}
          onClick={handleToggleFullscreen}
          aria-label={isFullscreen ? "Exit fullscreen" : "Enter fullscreen"}
        >
          {isFullscreen ? (
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M4 10H2v4h4v-2H4v-2zM2 6h2V4h2V2H2v4zm10 6h-2v2h4v-4h-2v2zM10 2v2h2v2h2V2h-4z"
                fill="currentColor"
              />
            </svg>
          ) : (
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M2 10h2v2h2v2H2v-4zm2-4H2V2h4v2H4v2zm8 6h-2v2h4v-4h-2v2zM10 2v2h2v2h2V2h-4z"
                fill="currentColor"
              />
            </svg>
          )}
        </button>
      </div>
      <div className={styles.scrollContainer}>
        {hasH2Sections
          ? sections.map((section, index) => {
              if (!section.heading) {
                // Content before first H2 — render without collapse
                return section.content
                  ? renderDesignContent(section.content, `preamble-${index}`)
                  : null;
              }

              const isExpanded = !collapsedSections.has(index);
              return (
                <div
                  key={`section-${index}`}
                  data-testid="design-panel-section"
                >
                  <button
                    type="button"
                    className={styles.sectionHeader}
                    onClick={() => handleToggleSection(index)}
                    aria-expanded={isExpanded}
                  >
                    <span className={styles.sectionHeadingText}>
                      {section.heading}
                    </span>
                    <svg
                      className={`${styles.chevron} ${isExpanded ? styles.chevronExpanded : ""}`}
                      viewBox="0 0 16 16"
                      fill="none"
                      aria-hidden="true"
                    >
                      <path
                        d="M6 4l4 4-4 4"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                  </button>
                  {isExpanded && section.content && (
                    <div className={styles.sectionContent}>
                      {renderDesignContent(section.content)}
                    </div>
                  )}
                </div>
              );
            })
          : // No H2 headings — render as single block
            sections.map((section, index) =>
              section.content
                ? renderDesignContent(section.content, `block-${index}`)
                : null,
            )}
      </div>
    </div>
  );

  // Portal fullscreen to document.body so fixed positioning isn't clipped by
  // the issue panel's transform/overflow ancestors.
  if (isFullscreen) {
    return createPortal(panel, document.body);
  }

  return panel;
}
