import { sanitizeDesignHtml } from "./sanitizeHtml";

const FRAME_CSP = [
  "default-src 'none'",
  "style-src 'unsafe-inline'",
  "img-src data:",
  "font-src data:",
  "media-src 'none'",
  "connect-src 'none'",
  "frame-src 'none'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join("; ");

const FRAME_BASE_STYLES = `
  :root { color-scheme: light; }
  html { box-sizing: border-box; background: transparent; }
  *, *::before, *::after { box-sizing: inherit; }
  html, body { width: 100%; margin: 0; padding: 0; overflow: hidden; }
  body {
    color: #334155;
    font: 14px/1.5 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont,
      "Segoe UI", sans-serif;
  }
  img, svg { max-width: 100%; }
`;

/** Build the complete, isolated document used by the design iframe. */
export function buildDesignHtmlDocument(input: string): string {
  const sanitized = sanitizeDesignHtml(input);

  return `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <meta http-equiv="Content-Security-Policy" content="${FRAME_CSP}">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>${FRAME_BASE_STYLES}</style>
  </head>
  <body>${sanitized}</body>
</html>`;
}
