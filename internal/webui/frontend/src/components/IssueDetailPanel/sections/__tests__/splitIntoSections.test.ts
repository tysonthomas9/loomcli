/**
 * Unit tests for DesignPanel.splitIntoSections().
 * Verifies section splitting works for Markdown `## ` headings, HTML `<h2>`
 * headings, and mixed content, keeping Markdown behavior unchanged.
 */

import { describe, it, expect } from "vitest";

import { splitIntoSections } from "../DesignPanel";

describe("splitIntoSections", () => {
  it("splits Markdown `## ` headings into sections", () => {
    expect(splitIntoSections("## A\nfoo\n## B\nbar")).toEqual([
      { heading: "A", content: "foo" },
      { heading: "B", content: "bar" },
    ]);
  });

  it("splits whole-line HTML `<h2>` headings into sections", () => {
    expect(
      splitIntoSections(
        '<h2>A</h2>\n<p>foo</p>\n<h2 id="x">B</h2>\n<p>bar</p>',
      ),
    ).toEqual([
      { heading: "A", content: "<p>foo</p>" },
      { heading: "B", content: "<p>bar</p>" },
    ]);
  });

  it("strips nested inline tags from HTML headings", () => {
    expect(splitIntoSections("<h2><span>Title</span></h2>\n<p>x</p>")).toEqual([
      { heading: "Title", content: "<p>x</p>" },
    ]);
  });

  it("handles mixed Markdown and HTML headings with a preamble", () => {
    expect(
      splitIntoSections("preamble\n## A\nfoo\n<h2>B</h2>\n<p>bar</p>"),
    ).toEqual([
      { heading: "", content: "preamble" },
      { heading: "A", content: "foo" },
      { heading: "B", content: "<p>bar</p>" },
    ]);
  });

  it("renders HTML without an <h2> as a single non-collapsible block", () => {
    expect(splitIntoSections("<p>just a block</p>")).toEqual([
      { heading: "", content: "<p>just a block</p>" },
    ]);
  });

  it("does not treat inline <h2> within a line as a heading", () => {
    const input = "text <h2>inline</h2> more";
    expect(splitIntoSections(input)).toEqual([
      { heading: "", content: "text <h2>inline</h2> more" },
    ]);
  });
});
