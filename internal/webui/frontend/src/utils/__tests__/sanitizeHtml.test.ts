/**
 * @vitest-environment jsdom
 */

import { describe, it, expect } from "vitest";

import { sanitizeDesignHtml, sanitizeHtml } from "../sanitizeHtml";

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

  it("preserves styles only for isolated design HTML", () => {
    const input =
      '<style>.card{display:grid}</style><div class="card">content</div>';

    expect(sanitizeHtml(input)).not.toContain("<style");
    expect(sanitizeDesignHtml(input)).toContain(
      "<style>.card{display:grid}</style>",
    );
  });

  it("strips executable and interactive design HTML", () => {
    const out = sanitizeDesignHtml(
      '<script>alert(1)</script><form><input autofocus onfocus="alert(2)"></form><p>safe</p>',
    );

    expect(out).toContain("<p>safe</p>");
    expect(out).not.toContain("<script");
    expect(out).not.toContain("<form");
    expect(out).not.toContain("<input");
    expect(out).not.toContain("onfocus");
  });

  it("removes stylesheet network and execution escape paths", () => {
    const out = sanitizeDesignHtml(
      '<style>@import "https://evil.test/x.css";.a{background:url(https://evil.test/x);width:expression(alert(1));behavior:url(x)}</style><div class="a">safe</div>',
    );

    expect(out).not.toContain("@import");
    expect(out).not.toContain("https://evil.test");
    expect(out).not.toContain("expression(");
    expect(out).not.toContain("behavior:");
    expect(out).toContain('<div class="a">safe</div>');
  });
});
