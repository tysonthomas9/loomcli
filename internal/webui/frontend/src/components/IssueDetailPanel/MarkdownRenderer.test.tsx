/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MarkdownRenderer XSS sanitization.
 * Verifies that rehype-sanitize strips dangerous HTML while preserving safe elements.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import "@testing-library/jest-dom";
import { MarkdownRenderer } from "./MarkdownRenderer";

describe("MarkdownRenderer XSS sanitization", () => {
  it("strips script tags from rendered output", () => {
    const malicious = `Hello <script>alert("xss")</script> World`;
    render(<MarkdownRenderer content={malicious} />);

    const container = screen.getByTestId("markdown-content");
    // Script tag element should be stripped — no <script> in the DOM
    expect(container.innerHTML).not.toContain("<script");
    expect(container.querySelector("script")).toBeNull();
    // Safe text around the script tag should still be present
    expect(container).toHaveTextContent("Hello");
    expect(container).toHaveTextContent("World");
  });

  it("preserves safe HTML elements in markdown rendering", () => {
    const content = `**Bold text** and *italic text* and [a link](https://example.com)`;
    render(<MarkdownRenderer content={content} />);

    const container = screen.getByTestId("markdown-content");
    // Bold should be rendered as <strong>
    const strong = container.querySelector("strong");
    expect(strong).not.toBeNull();
    expect(strong).toHaveTextContent("Bold text");

    // Italic should be rendered as <em>
    const em = container.querySelector("em");
    expect(em).not.toBeNull();
    expect(em).toHaveTextContent("italic text");

    // Link should be preserved
    const link = screen.getByRole("link", { name: "a link" });
    expect(link).toHaveAttribute("href", "https://example.com");
  });

  it("renders normally with no content (empty state)", () => {
    render(<MarkdownRenderer content={null} />);
    expect(screen.getByTestId("markdown-empty")).toBeInTheDocument();
    expect(screen.getByText("No content")).toBeInTheDocument();
  });

  it("strips event handler injection via img tag", () => {
    render(<MarkdownRenderer content="<img src=x onerror=alert(1)>" />);
    const container = screen.getByTestId("markdown-content");
    expect(container.innerHTML).not.toContain("onerror");
  });

  it("strips javascript: URI in links", () => {
    render(<MarkdownRenderer content="[click](javascript:alert(1))" />);
    const container = screen.getByTestId("markdown-content");
    const links = container.querySelectorAll("a");
    links.forEach((link) => {
      expect(link.getAttribute("href") ?? "").not.toContain("javascript:");
    });
  });

  it("strips iframe injection", () => {
    render(<MarkdownRenderer content='<iframe src="evil.com"></iframe>' />);
    const container = screen.getByTestId("markdown-content");
    expect(container.querySelector("iframe")).toBeNull();
  });

  it("strips mXSS via math/table nesting", () => {
    const mxss =
      "<math><mtext><table><mglyph><style><!--</style><img src=x onerror=alert(1)>-->";
    render(<MarkdownRenderer content={mxss} />);
    const container = screen.getByTestId("markdown-content");
    expect(container.innerHTML).not.toContain("onerror");
  });

  it("strips onmouseover handler on div", () => {
    render(
      <MarkdownRenderer content='<div onmouseover="alert(1)">hover</div>' />,
    );
    const container = screen.getByTestId("markdown-content");
    expect(container.innerHTML).not.toContain("onmouseover");
  });
});
