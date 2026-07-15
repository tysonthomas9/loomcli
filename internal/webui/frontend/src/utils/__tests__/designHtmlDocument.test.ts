/**
 * @vitest-environment jsdom
 */

import { describe, expect, it } from "vitest";

import { buildDesignHtmlDocument } from "../designHtmlDocument";

describe("buildDesignHtmlDocument", () => {
  it("wraps sanitized styles in a network-blocked document", () => {
    const document = buildDesignHtmlDocument(
      '<style>.hero{color:red}</style><h1 class="hero">Hello</h1>',
    );

    expect(document).toContain("Content-Security-Policy");
    expect(document).toContain("default-src 'none'");
    expect(document).toContain("connect-src 'none'");
    expect(document).toContain("<style>.hero{color:red}</style>");
    expect(document).toContain('<h1 class="hero">Hello</h1>');
  });

  it("removes executable markup before constructing srcdoc", () => {
    const document = buildDesignHtmlDocument(
      '<svg><script>window.bad=1</script><rect onload="window.bad=2"></rect></svg>',
    );

    expect(document).not.toContain("<script");
    expect(document).not.toContain("onload");
    expect(document).not.toContain("window.bad");
  });
});
