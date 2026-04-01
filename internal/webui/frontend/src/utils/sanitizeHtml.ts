import DOMPurify from "dompurify";

/**
 * Sanitize an HTML or markdown string, stripping dangerous elements
 * while preserving safe HTML and all markdown syntax.
 *
 * Uses DOMPurify with a strict configuration:
 * - Default allowed tags (safe inline/block HTML)
 * - Explicitly forbids iframe, object, embed, form, style
 * - Explicitly forbids dangerous event handler attributes
 * - Disables data: URI attributes
 */
export function sanitizeHtml(input: string): string {
  return DOMPurify.sanitize(input, {
    FORBID_TAGS: ["iframe", "object", "embed", "form", "style"],
    FORBID_ATTR: ["onerror", "onload", "onclick", "onmouseover"],
    ALLOW_DATA_ATTR: false,
  });
}
