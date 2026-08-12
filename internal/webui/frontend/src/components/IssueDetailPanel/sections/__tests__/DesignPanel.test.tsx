/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DesignPanel component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import "@testing-library/jest-dom";
import { DesignPanel } from "../DesignPanel";

describe("DesignPanel", () => {
  it("renders explicitly formatted HTML through the design-only sanitizer", () => {
    render(
      <DesignPanel
        format="html"
        content={'<h2>Plan</h2>\n<p class="safe">Ship it</p>'}
      />,
    );
    const frame = screen.getByTitle("HTML design artifact");
    expect(frame).toHaveAttribute("sandbox", "allow-same-origin");
    expect(frame.getAttribute("srcdoc")).toContain(
      '<p class="safe">Ship it</p>',
    );
  });

  it("renders safe inline SVG and strips executable SVG content", () => {
    render(
      <DesignPanel
        format="html"
        content={
          '<svg width="40" height="40" viewBox="0 0 40 40"><script>window.bad=1</script><rect width="40" height="40" fill="none" onload="window.bad=2"/></svg>'
        }
      />,
    );
    const frame = screen.getByTestId("design-html-content");
    const srcDoc = frame.getAttribute("srcdoc") ?? "";
    expect(srcDoc).toContain("<svg");
    expect(srcDoc).toContain("<rect");
    expect(srcDoc).not.toContain("<script");
    expect(srcDoc).not.toContain("onload");
    expect(srcDoc).not.toContain("window.bad");
  });

  it("keeps embedded artifact styles in one isolated document", () => {
    render(
      <DesignPanel
        format="html"
        content={
          '<style>.card{display:grid}</style>\n<h2>Plan</h2>\n<div class="card">Ship it</div>'
        }
      />,
    );

    const frames = screen.getAllByTitle("HTML design artifact");
    expect(frames).toHaveLength(1);
    expect(frames[0]?.getAttribute("srcdoc")).toContain(
      "<style>.card{display:grid}</style>",
    );
    expect(
      screen.queryByTestId("design-panel-section"),
    ).not.toBeInTheDocument();
  });

  it("keeps explicit markdown authoritative even when it starts with HTML", () => {
    render(<DesignPanel format="markdown" content='<p class="raw">text</p>' />);
    expect(
      screen.getByTestId("design-panel").querySelector("p.raw"),
    ).toBeNull();
  });
  describe("Empty states", () => {
    it("renders empty placeholder when content is null", () => {
      render(<DesignPanel content={null} />);
      expect(screen.getByTestId("design-panel")).toBeInTheDocument();
      expect(screen.getByTestId("design-empty")).toBeInTheDocument();
      expect(screen.getByText("No design yet")).toBeInTheDocument();
    });

    it("renders empty placeholder when content is undefined", () => {
      render(<DesignPanel content={undefined} />);
      expect(screen.getByTestId("design-empty")).toBeInTheDocument();
      expect(screen.getByText("No design yet")).toBeInTheDocument();
    });

    it("renders empty placeholder when content is empty string", () => {
      render(<DesignPanel content="" />);
      expect(screen.getByTestId("design-empty")).toBeInTheDocument();
      expect(screen.getByText("No design yet")).toBeInTheDocument();
    });
  });

  describe("Markdown rendering", () => {
    it("renders markdown content when provided", () => {
      render(<DesignPanel content="Some design content" />);
      expect(screen.getByTestId("design-panel")).toBeInTheDocument();
      expect(screen.queryByTestId("design-empty")).not.toBeInTheDocument();
      expect(screen.getByText("Some design content")).toBeInTheDocument();
    });
  });

  describe("Collapsible H2 sections", () => {
    it("renders H2 headings as collapsible section headers", () => {
      const content = `## Architecture\nDetails about architecture\n\n## Implementation\nImplementation notes`;
      render(<DesignPanel content={content} />);

      const sections = screen.getAllByTestId("design-panel-section");
      expect(sections).toHaveLength(2);
      expect(screen.getByText("Architecture")).toBeInTheDocument();
      expect(screen.getByText("Implementation")).toBeInTheDocument();
    });

    it("clicking a section header toggles content visibility", () => {
      const content = `## Architecture\nDetails about architecture`;
      render(<DesignPanel content={content} />);

      // Section should start expanded
      const sectionButton = screen.getByText("Architecture").closest("button")!;
      expect(sectionButton).toHaveAttribute("aria-expanded", "true");
      expect(
        screen.getByText("Details about architecture"),
      ).toBeInTheDocument();

      // Click to collapse
      fireEvent.click(sectionButton);
      expect(sectionButton).toHaveAttribute("aria-expanded", "false");
      expect(
        screen.queryByText("Details about architecture"),
      ).not.toBeInTheDocument();

      // Click again to expand
      fireEvent.click(sectionButton);
      expect(sectionButton).toHaveAttribute("aria-expanded", "true");
      expect(
        screen.getByText("Details about architecture"),
      ).toBeInTheDocument();
    });

    it("all sections default to expanded", () => {
      const content = `## Section A\nContent A\n\n## Section B\nContent B\n\n## Section C\nContent C`;
      render(<DesignPanel content={content} />);

      const sectionButtons = screen
        .getAllByTestId("design-panel-section")
        .map((s) => s.querySelector("button")!);

      for (const btn of sectionButtons) {
        expect(btn).toHaveAttribute("aria-expanded", "true");
      }
    });

    it("content with no H2s renders as single block without collapse controls", () => {
      const content = `# Title\nSome paragraph\n\n### Subsection\nMore text`;
      render(<DesignPanel content={content} />);

      expect(
        screen.queryByTestId("design-panel-section"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("Some paragraph")).toBeInTheDocument();
    });

    it("content before first H2 renders without collapse controls", () => {
      const content = `Preamble text\n\n## First Section\nSection content`;
      render(<DesignPanel content={content} />);

      // Preamble should be rendered
      expect(screen.getByText("Preamble text")).toBeInTheDocument();
      // Only one design-panel-section (the H2 section), not the preamble
      const sections = screen.getAllByTestId("design-panel-section");
      expect(sections).toHaveLength(1);
      expect(screen.getByText("First Section")).toBeInTheDocument();
    });
  });

  describe("Fullscreen", () => {
    it("fullscreen button is present in header", () => {
      render(<DesignPanel content="Some content" />);
      expect(screen.getByLabelText("Enter fullscreen")).toBeInTheDocument();
    });

    it("clicking fullscreen button adds fullscreen class", () => {
      const { container } = render(<DesignPanel content="Some content" />);

      expect(screen.getByTestId("design-panel").className).not.toMatch(
        /fullscreen/,
      );

      fireEvent.click(screen.getByLabelText("Enter fullscreen"));

      const panel = screen.getByTestId("design-panel");
      expect(panel.className).toMatch(/fullscreen/);
      expect(panel.parentElement).toBe(document.body);
      expect(
        container.querySelector('[data-testid="design-panel"]'),
      ).toBeNull();
      expect(screen.getByLabelText("Exit fullscreen")).toBeInTheDocument();
    });

    it("escape key exits fullscreen without propagating", () => {
      const { container } = render(<DesignPanel content="Some content" />);

      fireEvent.click(screen.getByLabelText("Enter fullscreen"));
      expect(screen.getByTestId("design-panel").className).toMatch(
        /fullscreen/,
      );

      fireEvent.keyDown(document, { key: "Escape" });

      const panel = screen.getByTestId("design-panel");
      expect(panel.className).not.toMatch(/fullscreen/);
      expect(container.contains(panel)).toBe(true);
      expect(screen.getByLabelText("Enter fullscreen")).toBeInTheDocument();
    });

    it("escape key does not fire when not in fullscreen", () => {
      render(<DesignPanel content="Some content" />);
      const panel = screen.getByTestId("design-panel");

      // Not in fullscreen
      expect(panel.className).not.toMatch(/fullscreen/);

      // Press Escape - nothing should change
      fireEvent.keyDown(document, { key: "Escape" });
      expect(panel.className).not.toMatch(/fullscreen/);
    });
  });
});
