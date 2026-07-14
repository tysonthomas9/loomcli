/**
 * @vitest-environment jsdom
 */

import { describe, it, expect } from "vitest";

import { sanitizeHtml } from "../sanitizeHtml";

describe("sanitizeHtml", () => {
  it("strips script tags", () => {
    expect(sanitizeHtml("<script>alert(1)</script>")).not.toContain("<script");
  });

  it("strips event handlers", () => {
    const result = sanitizeHtml("<img src=x onerror=alert(1)>");
    expect(result).not.toContain("onerror");
  });

  it("strips javascript: URIs", () => {
    const result = sanitizeHtml('<a href="javascript:alert(1)">click</a>');
    expect(result).not.toContain("javascript:");
  });

  it("strips iframe tags", () => {
    expect(sanitizeHtml('<iframe src="evil.com"></iframe>')).not.toContain(
      "<iframe",
    );
  });

  it("strips style tags", () => {
    expect(sanitizeHtml("<style>body{display:none}</style>")).not.toContain(
      "<style",
    );
  });

  it("strips object and embed tags", () => {
    expect(sanitizeHtml('<object data="x"></object>')).not.toContain("<object");
    expect(sanitizeHtml('<embed src="x">')).not.toContain("<embed");
  });

  it("strips form tags", () => {
    expect(
      sanitizeHtml('<form action="evil.com"><input></form>'),
    ).not.toContain("<form");
  });

  it("preserves safe HTML", () => {
    expect(sanitizeHtml("<strong>bold</strong>")).toContain("<strong>");
    expect(sanitizeHtml("<em>italic</em>")).toContain("<em>");
    expect(sanitizeHtml("<code>code</code>")).toContain("<code>");
  });

  it("preserves plain text and markdown syntax", () => {
    const md = "Hello **world** and `code`";
    expect(sanitizeHtml(md)).toBe(md);
  });

  it("handles empty string", () => {
    expect(sanitizeHtml("")).toBe("");
  });

  it("handles mXSS via math/table nesting", () => {
    const mxss =
      "<math><mtext><table><mglyph><style><!--</style><img src=x onerror=alert(1)>-->";
    const result = sanitizeHtml(mxss);
    expect(result).not.toContain("onerror");
  });

  it("preserves presentation SVG", () => {
    const svg =
      '<svg width="100" height="50" viewBox="0 0 100 50">' +
      '<rect x="1" y="1" width="98" height="48" fill="none" stroke="black" stroke-width="2"/>' +
      '<text x="10" y="30">Hi</text></svg>';
    const out = sanitizeHtml(svg);
    expect(out).toContain("<svg");
    expect(out).toContain("<rect");
    expect(out).toContain("<text");
    expect(out).toContain('viewBox="0 0 100 50"');
  });

  it("strips script and event handlers nested in SVG", () => {
    const out = sanitizeHtml(
      '<svg><script>alert(1)</script><rect onload="alert(2)" onclick="x()"/></svg>',
    );
    expect(out.toLowerCase()).not.toContain("<script");
    expect(out).not.toContain("onload");
    expect(out).not.toContain("onclick");
    expect(out.toLowerCase()).not.toContain("alert");
  });
});
