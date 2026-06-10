/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MarkdownRenderer component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import "@testing-library/jest-dom";
import { MarkdownRenderer } from "../MarkdownRenderer";

describe("MarkdownRenderer", () => {
  describe("Empty states", () => {
    it("renders empty state for null content", () => {
      render(<MarkdownRenderer content={null} />);
      expect(screen.getByTestId("markdown-empty")).toBeInTheDocument();
      expect(screen.getByText("No content")).toBeInTheDocument();
    });

    it("renders empty state for undefined content", () => {
      render(<MarkdownRenderer content={undefined} />);
      expect(screen.getByTestId("markdown-empty")).toBeInTheDocument();
    });

    it("renders empty state for empty string", () => {
      render(<MarkdownRenderer content="" />);
      expect(screen.getByTestId("markdown-empty")).toBeInTheDocument();
    });
  });

  describe("Markdown rendering", () => {
    it("renders markdown content", () => {
      render(<MarkdownRenderer content="# Hello World" />);
      expect(screen.getByTestId("markdown-content")).toBeInTheDocument();
      expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
        "Hello World",
      );
    });

    it("renders H2 headings correctly", () => {
      render(<MarkdownRenderer content="## Summary" />);
      expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(
        "Summary",
      );
    });

    it("renders H3 headings correctly", () => {
      render(<MarkdownRenderer content="### Details" />);
      expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent(
        "Details",
      );
    });

    it("renders multiple headings", () => {
      const content = `## Summary

### Details`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(
        "Summary",
      );
      expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent(
        "Details",
      );
    });

    it("renders code blocks", () => {
      const content = `\`\`\`typescript
const x = 1;
\`\`\``;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByText("const x = 1;")).toBeInTheDocument();
    });

    it("renders inline code", () => {
      render(<MarkdownRenderer content="Use `npm install` to setup" />);
      const code = screen.getByText("npm install");
      expect(code.tagName.toLowerCase()).toBe("code");
    });

    it("renders unordered lists", () => {
      const content = `- Item 1
- Item 2`;
      render(<MarkdownRenderer content={content} />);
      const items = screen.getAllByRole("listitem");
      expect(items).toHaveLength(2);
      expect(items[0]).toHaveTextContent("Item 1");
      expect(items[1]).toHaveTextContent("Item 2");
    });

    it("renders ordered lists", () => {
      const content = `1. First
2. Second`;
      render(<MarkdownRenderer content={content} />);
      const items = screen.getAllByRole("listitem");
      expect(items).toHaveLength(2);
      expect(items[0]).toHaveTextContent("First");
      expect(items[1]).toHaveTextContent("Second");
    });

    it("renders links", () => {
      render(<MarkdownRenderer content="[Click here](https://example.com)" />);
      const link = screen.getByRole("link", { name: "Click here" });
      expect(link).toHaveAttribute("href", "https://example.com");
    });

    it("renders bold text", () => {
      render(<MarkdownRenderer content="This is **bold** text" />);
      const strong = screen.getByText("bold");
      expect(strong.tagName.toLowerCase()).toBe("strong");
    });

    it("renders italic text", () => {
      render(<MarkdownRenderer content="This is *italic* text" />);
      const em = screen.getByText("italic");
      expect(em.tagName.toLowerCase()).toBe("em");
    });

    // Tables require remark-gfm plugin which is not currently configured
    it.skip("renders tables", () => {
      const content = `| Col1 | Col2 |
|------|------|
| A | B |`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByRole("table")).toBeInTheDocument();
      expect(screen.getByText("A")).toBeInTheDocument();
      expect(screen.getByText("B")).toBeInTheDocument();
    });

    it("renders paragraphs", () => {
      const content = `First paragraph

Second paragraph`;
      render(<MarkdownRenderer content={content} />);
      expect(screen.getByText("First paragraph")).toBeInTheDocument();
      expect(screen.getByText("Second paragraph")).toBeInTheDocument();
    });
  });

  describe("Custom className", () => {
    it("applies custom className to content", () => {
      render(<MarkdownRenderer content="Test" className="custom-class" />);
      const container = screen.getByTestId("markdown-content");
      expect(container).toHaveClass("custom-class");
    });

    it("applies custom className to empty state", () => {
      render(<MarkdownRenderer content={null} className="custom-class" />);
      const container = screen.getByTestId("markdown-empty");
      expect(container).toHaveClass("custom-class");
    });
  });
});

describe("MarkdownRenderer raw HTML passthrough", () => {
  it("renders a pure-HTML design as formatted elements", () => {
    render(
      <MarkdownRenderer content="<h2>Approach</h2><p>hello <strong>world</strong></p>" />,
    );
    const container = screen.getByTestId("markdown-content");
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(
      "Approach",
    );
    const strong = container.querySelector("strong");
    expect(strong).not.toBeNull();
    expect(strong).toHaveTextContent("world");
    // Heading is a real element, not literal "## Approach" / "<h2>" text.
    expect(screen.queryByText("<h2>Approach</h2>")).toBeNull();
  });

  it("keeps safe attributes on mixed markdown and raw HTML", () => {
    // Markdown-leading raw HTML is split into markdown and DOMPurify-sanitized
    // HTML chunks. Safe attributes like class are preserved, matching the
    // HTML-leading design path below.
    render(
      <MarkdownRenderer content={'## Notes\n\n<p class="danger">text</p>'} />,
    );
    const container = screen.getByTestId("markdown-content");
    const p = container.querySelector("p");
    expect(p).not.toBeNull();
    expect(p).toHaveTextContent("text");
    expect(p?.getAttribute("class")).toBe("danger");
  });

  it("renders HTML lists as list elements", () => {
    render(<MarkdownRenderer content="<ul><li>One</li><li>Two</li></ul>" />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("One");
    expect(items[1]).toHaveTextContent("Two");
  });

  it("renders mixed Markdown and HTML content", () => {
    render(
      <MarkdownRenderer
        content={"## Markdown heading\n\n<p>An <code>html</code> paragraph</p>"}
      />,
    );
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(
      "Markdown heading",
    );
    const code = screen.getByText("html");
    expect(code.tagName.toLowerCase()).toBe("code");
  });

  it("renders HTML links safely", () => {
    render(
      <MarkdownRenderer content='<a href="https://example.com">Click</a>' />,
    );
    const link = screen.getByRole("link", { name: "Click" });
    expect(link).toHaveAttribute("href", "https://example.com");
  });
});

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

  it("renders empty state with null content (xss suite)", () => {
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
