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

/**
 * Sanitize a design artifact for rendering in the isolated HTML frame.
 *
 * Design HTML intentionally supports embedded styles. Styles are extracted
 * and constrained before DOMPurify handles the remaining markup. The frame
 * supplies the final CSS/network isolation.
 */
export function sanitizeDesignHtml(input: string): string {
  const styles: string[] = [];
  const markup = input.replace(
    /<style(?:\s[^>]*)?>([\s\S]*?)<\/style\s*>/gi,
    (_match, css: string) => {
      styles.push(sanitizeDesignCss(css));
      return "";
    },
  );
  const sanitizedMarkup = DOMPurify.sanitize(markup, {
    FORBID_TAGS: [
      "script",
      "iframe",
      "object",
      "embed",
      "form",
      "input",
      "button",
      "textarea",
      "select",
      "option",
      "link",
      "meta",
      "base",
    ],
    ALLOW_DATA_ATTR: true,
  });

  const sanitizedStyles = styles
    .filter(Boolean)
    .map((css) => `<style>${css}</style>`)
    .join("");
  return `${sanitizedStyles}${sanitizedMarkup}`;
}

/** Remove CSS features that could attempt network access or script execution. */
function sanitizeDesignCss(input: string): string {
  return input
    .replace(/@import\s+[^;]+;?/gi, "")
    .replace(/url\s*\([^)]*\)/gi, "none")
    .replace(/expression\s*\([^)]*\)/gi, "")
    .replace(/(?:behavior|-moz-binding)\s*:[^;}]*/gi, "");
}
