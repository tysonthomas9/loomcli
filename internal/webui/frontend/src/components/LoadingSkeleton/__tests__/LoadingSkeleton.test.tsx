/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LoadingSkeleton component.
 */

import { render } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { LoadingSkeleton } from "../LoadingSkeleton";

describe("LoadingSkeleton", () => {
  describe("base component rendering", () => {
    it("renders with default props", () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toBeInTheDocument();
      expect(skeleton.tagName).toBe("DIV");
    });

    it('renders with aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toHaveAttribute("aria-hidden", "true");
    });

    it("applies skeleton base class", () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("skeleton");
    });
  });

  describe("shape variants", () => {
    it("renders rect shape by default", () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("rect");
    });

    it("renders rect shape when explicitly set", () => {
      const { container } = render(<LoadingSkeleton shape="rect" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("rect");
    });

    it("renders text shape correctly", () => {
      const { container } = render(<LoadingSkeleton shape="text" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("text");
    });

    it("renders circle shape correctly", () => {
      const { container } = render(<LoadingSkeleton shape="circle" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("circle");
    });
  });

  describe("custom dimensions", () => {
    it("applies width as number (converts to pixels)", () => {
      const { container } = render(<LoadingSkeleton width={100} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe("100px");
    });

    it("applies height as number (converts to pixels)", () => {
      const { container } = render(<LoadingSkeleton height={50} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe("50px");
    });

    it("applies width as string (CSS value)", () => {
      const { container } = render(<LoadingSkeleton width="100%" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe("100%");
    });

    it("applies height as string (CSS value)", () => {
      const { container } = render(<LoadingSkeleton height="2rem" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe("2rem");
    });

    it("applies both width and height together", () => {
      const { container } = render(
        <LoadingSkeleton width={200} height={100} />,
      );

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe("200px");
      expect(skeleton.style.height).toBe("100px");
    });

    it("does not add style attribute when dimensions are undefined", () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.getAttribute("style")).toBeNull();
    });
  });

  describe("custom className", () => {
    it("applies custom className to base skeleton", () => {
      const { container } = render(
        <LoadingSkeleton className="custom-class" />,
      );

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toHaveClass("custom-class");
    });

    it("preserves skeleton and shape classes when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton shape="circle" className="my-custom-class" />,
      );

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("skeleton");
      expect(skeleton.className).toContain("circle");
      expect(skeleton).toHaveClass("my-custom-class");
    });
  });

  describe("multiple text lines", () => {
    it("renders single line by default for text shape", () => {
      const { container } = render(<LoadingSkeleton shape="text" />);

      // Single line renders as single div, not container
      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("text");
      expect(skeleton.className).not.toContain("textContainer");
    });

    it("renders multiple lines when lines > 1", () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={3} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer.className).toContain("textContainer");
      expect(textContainer.children.length).toBe(3);
    });

    it("renders correct number of lines", () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={5} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer.children.length).toBe(5);
    });

    it('text container has aria-hidden="true"', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={2} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer).toHaveAttribute("aria-hidden", "true");
    });

    it("last line has reduced width (60%)", () => {
      const { container } = render(
        <LoadingSkeleton shape="text" lines={3} width={100} />,
      );

      const textContainer = container.firstChild as HTMLElement;
      const lines = textContainer.children;

      // First and second lines should have full width
      expect((lines[0] as HTMLElement).style.width).toBe("100px");
      expect((lines[1] as HTMLElement).style.width).toBe("100px");
      // Last line should be 60%
      expect((lines[2] as HTMLElement).style.width).toBe("60%");
    });

    it("applies height to each line", () => {
      const { container } = render(
        <LoadingSkeleton shape="text" lines={2} height={20} />,
      );

      const textContainer = container.firstChild as HTMLElement;
      const lines = textContainer.children;

      expect((lines[0] as HTMLElement).style.height).toBe("20px");
      expect((lines[1] as HTMLElement).style.height).toBe("20px");
    });

    it("lines prop is ignored for non-text shapes", () => {
      const { container } = render(<LoadingSkeleton shape="rect" lines={3} />);

      // Should render single rect, not multiple
      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("rect");
      expect(skeleton.children.length).toBe(0);
    });
  });

  describe("edge cases", () => {
    it("handles lines = 1 as single element (not container)", () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={1} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain("text");
      expect(skeleton.className).not.toContain("textContainer");
    });

    it("handles width = 0", () => {
      const { container } = render(<LoadingSkeleton width={0} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe("0px");
    });

    it("handles height = 0", () => {
      const { container } = render(<LoadingSkeleton height={0} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe("0px");
    });

    it("handles empty string className", () => {
      const { container } = render(<LoadingSkeleton className="" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toBeInTheDocument();
    });
  });
});

describe("LoadingSkeleton.Card", () => {
  describe("rendering", () => {
    it("renders with card structure", () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const card = container.firstChild as HTMLElement;
      expect(card).toBeInTheDocument();
      expect(card.className).toContain("card");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const card = container.firstChild as HTMLElement;
      expect(card).toHaveAttribute("aria-hidden", "true");
    });

    it("contains card header section", () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const cardHeader = container.querySelector('[class*="cardHeader"]');
      expect(cardHeader).toBeInTheDocument();
    });

    it("contains skeleton elements in header", () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const cardHeader = container.querySelector('[class*="cardHeader"]');
      expect(cardHeader?.children.length).toBe(2);
    });

    it("contains text skeleton for body content", () => {
      const { container } = render(<LoadingSkeleton.Card />);

      // Card should have header + text content (with 2 lines)
      const card = container.firstChild as HTMLElement;
      // Card has cardHeader and textContainer for body
      expect(card.children.length).toBe(2);
    });
  });

  describe("custom className", () => {
    it("applies custom className to card", () => {
      const { container } = render(
        <LoadingSkeleton.Card className="custom-card-class" />,
      );

      const card = container.firstChild as HTMLElement;
      expect(card).toHaveClass("custom-card-class");
    });

    it("preserves card class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Card className="my-class" />,
      );

      const card = container.firstChild as HTMLElement;
      expect(card.className).toContain("card");
      expect(card).toHaveClass("my-class");
    });
  });
});

describe("LoadingSkeleton.Column", () => {
  describe("rendering", () => {
    it("renders with column structure", () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const column = container.firstChild as HTMLElement;
      expect(column).toBeInTheDocument();
      expect(column.className).toContain("column");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const column = container.firstChild as HTMLElement;
      expect(column).toHaveAttribute("aria-hidden", "true");
    });

    it("contains column header section", () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnHeader = container.querySelector('[class*="columnHeader"]');
      expect(columnHeader).toBeInTheDocument();
    });

    it("contains skeleton elements in header", () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnHeader = container.querySelector('[class*="columnHeader"]');
      // Header has text skeleton + circle skeleton
      expect(columnHeader?.children.length).toBe(2);
    });

    it("contains column content section", () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent).toBeInTheDocument();
    });
  });

  describe("cardCount prop", () => {
    it("renders 3 cards by default", () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(3);
    });

    it("renders custom number of cards", () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={5} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(5);
    });

    it("renders 1 card when cardCount is 1", () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={1} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(1);
    });

    it("renders 0 cards when cardCount is 0", () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={0} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(0);
    });

    it("each card in column has card class", () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={2} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      const cards = columnContent?.children;

      expect((cards?.[0] as HTMLElement).className).toContain("card");
      expect((cards?.[1] as HTMLElement).className).toContain("card");
    });
  });

  describe("custom className", () => {
    it("applies custom className to column", () => {
      const { container } = render(
        <LoadingSkeleton.Column className="custom-column-class" />,
      );

      const column = container.firstChild as HTMLElement;
      expect(column).toHaveClass("custom-column-class");
    });

    it("preserves column class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Column className="my-column" />,
      );

      const column = container.firstChild as HTMLElement;
      expect(column.className).toContain("column");
      expect(column).toHaveClass("my-column");
    });
  });
});

describe("LoadingSkeleton.Graph", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-graph"', () => {
      const { getByTestId } = render(<LoadingSkeleton.Graph />);

      const graph = getByTestId("loading-skeleton-graph");
      expect(graph).toBeInTheDocument();
    });

    it("renders with graph structure", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graph = container.firstChild as HTMLElement;
      expect(graph).toBeInTheDocument();
      expect(graph.className).toContain("graph");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graph = container.firstChild as HTMLElement;
      expect(graph).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("graph nodes", () => {
    it("contains graphNodes section", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      expect(graphNodes).toBeInTheDocument();
    });

    it("contains 3 simulated graph nodes", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      const nodes = graphNodes?.querySelectorAll('[class*="graphNode"]');
      expect(nodes?.length).toBe(3);
    });

    it("each node contains a skeleton rectangle", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      const nodes = graphNodes?.querySelectorAll('[class*="graphNode"]');

      nodes?.forEach((node) => {
        const skeleton = node.querySelector('[class*="skeleton"]');
        expect(skeleton).toBeInTheDocument();
        expect(skeleton?.className).toContain("rect");
      });
    });
  });

  describe("minimap placeholder", () => {
    it("contains minimap placeholder section", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const miniMap = container.querySelector('[class*="graphMiniMap"]');
      expect(miniMap).toBeInTheDocument();
    });

    it("minimap contains a skeleton rectangle", () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const miniMap = container.querySelector('[class*="graphMiniMap"]');
      const skeleton = miniMap?.querySelector('[class*="skeleton"]');
      expect(skeleton).toBeInTheDocument();
      expect(skeleton?.className).toContain("rect");
    });
  });

  describe("custom className", () => {
    it("applies custom className to graph", () => {
      const { container } = render(
        <LoadingSkeleton.Graph className="custom-graph-class" />,
      );

      const graph = container.firstChild as HTMLElement;
      expect(graph).toHaveClass("custom-graph-class");
    });

    it("preserves graph class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Graph className="my-graph" />,
      );

      const graph = container.firstChild as HTMLElement;
      expect(graph.className).toContain("graph");
      expect(graph).toHaveClass("my-graph");
    });
  });
});

describe("LoadingSkeleton.DetailPanel", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-detail-panel"', () => {
      const { getByTestId } = render(<LoadingSkeleton.DetailPanel />);

      const panel = getByTestId("loading-skeleton-detail-panel");
      expect(panel).toBeInTheDocument();
    });

    it("renders with detailPanel structure", () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const panel = container.firstChild as HTMLElement;
      expect(panel).toBeInTheDocument();
      expect(panel.className).toContain("detailPanel");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const panel = container.firstChild as HTMLElement;
      expect(panel).toHaveAttribute("aria-hidden", "true");
    });

    it("contains detailPanelHeader section", () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const header = container.querySelector('[class*="detailPanelHeader"]');
      expect(header).toBeInTheDocument();
    });

    it("contains detailPanelMeta section with 3 badges", () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const meta = container.querySelector('[class*="detailPanelMeta"]');
      expect(meta).toBeInTheDocument();
      expect(meta?.children.length).toBe(3);
    });

    it("contains detailPanelBody section", () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const body = container.querySelector('[class*="detailPanelBody"]');
      expect(body).toBeInTheDocument();
    });

    it("contains detailPanelSection section", () => {
      const { container } = render(<LoadingSkeleton.DetailPanel />);

      const section = container.querySelector('[class*="detailPanelSection"]');
      expect(section).toBeInTheDocument();
    });
  });

  describe("custom className", () => {
    it("applies custom className to detailPanel", () => {
      const { container } = render(
        <LoadingSkeleton.DetailPanel className="custom-detail-class" />,
      );

      const panel = container.firstChild as HTMLElement;
      expect(panel).toHaveClass("custom-detail-class");
    });

    it("preserves detailPanel class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.DetailPanel className="my-detail" />,
      );

      const panel = container.firstChild as HTMLElement;
      expect(panel.className).toContain("detailPanel");
      expect(panel).toHaveClass("my-detail");
    });
  });
});

describe("LoadingSkeleton.Table", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-table"', () => {
      const { getByTestId } = render(<LoadingSkeleton.Table />);

      const table = getByTestId("loading-skeleton-table");
      expect(table).toBeInTheDocument();
    });

    it("renders with table structure", () => {
      const { container } = render(<LoadingSkeleton.Table />);

      const table = container.firstChild as HTMLElement;
      expect(table).toBeInTheDocument();
      expect(table.className).toContain("table");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Table />);

      const table = container.firstChild as HTMLElement;
      expect(table).toHaveAttribute("aria-hidden", "true");
    });

    it("contains tableHeader section with 4 column skeletons", () => {
      const { container } = render(<LoadingSkeleton.Table />);

      const header = container.querySelector('[class*="tableHeader"]');
      expect(header).toBeInTheDocument();
      expect(header?.children.length).toBe(4);
    });

    it("contains 5 tableRow elements", () => {
      const { container } = render(<LoadingSkeleton.Table />);

      const rows = container.querySelectorAll('[class*="tableRow"]');
      expect(rows.length).toBe(5);
    });

    it("each row contains 4 skeleton elements", () => {
      const { container } = render(<LoadingSkeleton.Table />);

      const rows = container.querySelectorAll('[class*="tableRow"]');
      rows.forEach((row) => {
        expect(row.children.length).toBe(4);
      });
    });
  });

  describe("custom className", () => {
    it("applies custom className to table", () => {
      const { container } = render(
        <LoadingSkeleton.Table className="custom-table-class" />,
      );

      const table = container.firstChild as HTMLElement;
      expect(table).toHaveClass("custom-table-class");
    });

    it("preserves table class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Table className="my-table" />,
      );

      const table = container.firstChild as HTMLElement;
      expect(table.className).toContain("table");
      expect(table).toHaveClass("my-table");
    });
  });
});

describe("LoadingSkeleton.FileExplorer", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-file-explorer"', () => {
      const { getByTestId } = render(<LoadingSkeleton.FileExplorer />);

      const explorer = getByTestId("loading-skeleton-file-explorer");
      expect(explorer).toBeInTheDocument();
    });

    it("renders with fileExplorer structure", () => {
      const { container } = render(<LoadingSkeleton.FileExplorer />);

      const explorer = container.firstChild as HTMLElement;
      expect(explorer).toBeInTheDocument();
      expect(explorer.className).toContain("fileExplorer");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.FileExplorer />);

      const explorer = container.firstChild as HTMLElement;
      expect(explorer).toHaveAttribute("aria-hidden", "true");
    });

    it("contains fileTree section with tree items", () => {
      const { container } = render(<LoadingSkeleton.FileExplorer />);

      const fileTree = container.querySelector('[class*="fileTree"]');
      expect(fileTree).toBeInTheDocument();

      const treeItems = fileTree?.querySelectorAll('[class*="fileTreeItem"]');
      expect(treeItems?.length).toBe(8);
    });

    it("tree items have indentation via paddingLeft", () => {
      const { container } = render(<LoadingSkeleton.FileExplorer />);

      const treeItems = container.querySelectorAll('[class*="fileTreeItem"]');
      // First item (level 0): 0*16+8 = 8px
      expect((treeItems[0] as HTMLElement).style.paddingLeft).toBe("8px");
      // Second item (level 1): 1*16+8 = 24px
      expect((treeItems[1] as HTMLElement).style.paddingLeft).toBe("24px");
    });

    it("contains codeArea section with skeleton lines", () => {
      const { container } = render(<LoadingSkeleton.FileExplorer />);

      const codeArea = container.querySelector('[class*="codeArea"]');
      expect(codeArea).toBeInTheDocument();
      expect(codeArea?.children.length).toBe(6);
    });
  });

  describe("custom className", () => {
    it("applies custom className to fileExplorer", () => {
      const { container } = render(
        <LoadingSkeleton.FileExplorer className="custom-explorer-class" />,
      );

      const explorer = container.firstChild as HTMLElement;
      expect(explorer).toHaveClass("custom-explorer-class");
    });

    it("preserves fileExplorer class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.FileExplorer className="my-explorer" />,
      );

      const explorer = container.firstChild as HTMLElement;
      expect(explorer.className).toContain("fileExplorer");
      expect(explorer).toHaveClass("my-explorer");
    });
  });
});

describe("LoadingSkeleton.Terminal", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-terminal"', () => {
      const { getByTestId } = render(<LoadingSkeleton.Terminal />);

      const terminal = getByTestId("loading-skeleton-terminal");
      expect(terminal).toBeInTheDocument();
    });

    it("renders with terminal structure", () => {
      const { container } = render(<LoadingSkeleton.Terminal />);

      const terminal = container.firstChild as HTMLElement;
      expect(terminal).toBeInTheDocument();
      expect(terminal.className).toContain("terminal");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Terminal />);

      const terminal = container.firstChild as HTMLElement;
      expect(terminal).toHaveAttribute("aria-hidden", "true");
    });

    it("contains terminalTabBar section with 2 tab skeletons", () => {
      const { container } = render(<LoadingSkeleton.Terminal />);

      const tabBar = container.querySelector('[class*="terminalTabBar"]');
      expect(tabBar).toBeInTheDocument();
      expect(tabBar?.children.length).toBe(2);
    });

    it("contains terminalBody section with 4 line skeletons", () => {
      const { container } = render(<LoadingSkeleton.Terminal />);

      const body = container.querySelector('[class*="terminalBody"]');
      expect(body).toBeInTheDocument();
      expect(body?.children.length).toBe(4);
    });
  });

  describe("custom className", () => {
    it("applies custom className to terminal", () => {
      const { container } = render(
        <LoadingSkeleton.Terminal className="custom-terminal-class" />,
      );

      const terminal = container.firstChild as HTMLElement;
      expect(terminal).toHaveClass("custom-terminal-class");
    });

    it("preserves terminal class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Terminal className="my-terminal" />,
      );

      const terminal = container.firstChild as HTMLElement;
      expect(terminal.className).toContain("terminal");
      expect(terminal).toHaveClass("my-terminal");
    });
  });
});

describe("LoadingSkeleton.Observability", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-observability"', () => {
      const { getByTestId } = render(<LoadingSkeleton.Observability />);

      const obs = getByTestId("loading-skeleton-observability");
      expect(obs).toBeInTheDocument();
    });

    it("renders with observability structure", () => {
      const { container } = render(<LoadingSkeleton.Observability />);

      const obs = container.firstChild as HTMLElement;
      expect(obs).toBeInTheDocument();
      expect(obs.className).toContain("observability");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Observability />);

      const obs = container.firstChild as HTMLElement;
      expect(obs).toHaveAttribute("aria-hidden", "true");
    });

    it("contains observabilityCards section with 4 metric cards", () => {
      const { container } = render(<LoadingSkeleton.Observability />);

      const cards = container.querySelector('[class*="observabilityCards"]');
      expect(cards).toBeInTheDocument();

      const cardItems = cards?.querySelectorAll('[class*="observabilityCard"]');
      expect(cardItems?.length).toBe(4);
    });

    it("each metric card contains skeleton elements for label and value", () => {
      const { container } = render(<LoadingSkeleton.Observability />);

      const cards = container.querySelector('[class*="observabilityCards"]');
      // Direct children of the cards container are the individual card divs
      const directCards = Array.from(cards?.children ?? []);
      expect(directCards.length).toBe(4);

      directCards.forEach((card) => {
        // Each card has 2 skeleton elements: a label and a value
        const skeletons = card.querySelectorAll('[class*="skeleton"]');
        expect(skeletons.length).toBe(2);
      });
    });

    it("contains observabilityChart section", () => {
      const { container } = render(<LoadingSkeleton.Observability />);

      const chart = container.querySelector('[class*="observabilityChart"]');
      expect(chart).toBeInTheDocument();
      expect(chart?.children.length).toBe(2);
    });
  });

  describe("custom className", () => {
    it("applies custom className to observability", () => {
      const { container } = render(
        <LoadingSkeleton.Observability className="custom-obs-class" />,
      );

      const obs = container.firstChild as HTMLElement;
      expect(obs).toHaveClass("custom-obs-class");
    });

    it("preserves observability class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.Observability className="my-obs" />,
      );

      const obs = container.firstChild as HTMLElement;
      expect(obs.className).toContain("observability");
      expect(obs).toHaveClass("my-obs");
    });
  });
});

describe("LoadingSkeleton.AgentDetail", () => {
  describe("rendering", () => {
    it('renders with data-testid="loading-skeleton-agent-detail"', () => {
      const { getByTestId } = render(<LoadingSkeleton.AgentDetail />);

      const detail = getByTestId("loading-skeleton-agent-detail");
      expect(detail).toBeInTheDocument();
    });

    it("renders with agentDetail structure", () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const detail = container.firstChild as HTMLElement;
      expect(detail).toBeInTheDocument();
      expect(detail.className).toContain("agentDetail");
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const detail = container.firstChild as HTMLElement;
      expect(detail).toHaveAttribute("aria-hidden", "true");
    });

    it("contains agentDetailHeader section with avatar and info", () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const header = container.querySelector('[class*="agentDetailHeader"]');
      expect(header).toBeInTheDocument();
      // Should have circle avatar + info container
      expect(header?.children.length).toBe(2);
    });

    it("header info contains name and status skeletons", () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const info = container.querySelector('[class*="agentDetailInfo"]');
      expect(info).toBeInTheDocument();
      expect(info?.children.length).toBe(2);
    });

    it("contains agentDetailTabs section with 3 tab skeletons", () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const tabs = container.querySelector('[class*="agentDetailTabs"]');
      expect(tabs).toBeInTheDocument();
      expect(tabs?.children.length).toBe(3);
    });

    it("contains agentDetailContent section", () => {
      const { container } = render(<LoadingSkeleton.AgentDetail />);

      const content = container.querySelector('[class*="agentDetailContent"]');
      expect(content).toBeInTheDocument();
    });
  });

  describe("custom className", () => {
    it("applies custom className to agentDetail", () => {
      const { container } = render(
        <LoadingSkeleton.AgentDetail className="custom-agent-class" />,
      );

      const detail = container.firstChild as HTMLElement;
      expect(detail).toHaveClass("custom-agent-class");
    });

    it("preserves agentDetail class when custom className is added", () => {
      const { container } = render(
        <LoadingSkeleton.AgentDetail className="my-agent" />,
      );

      const detail = container.firstChild as HTMLElement;
      expect(detail.className).toContain("agentDetail");
      expect(detail).toHaveClass("my-agent");
    });
  });
});

describe("LoadingSkeleton integration", () => {
  it("all presets are accessible as static properties", () => {
    expect(LoadingSkeleton.Card).toBeDefined();
    expect(LoadingSkeleton.Column).toBeDefined();
    expect(LoadingSkeleton.Graph).toBeDefined();
    expect(LoadingSkeleton.Monitor).toBeDefined();
    expect(LoadingSkeleton.DetailPanel).toBeDefined();
    expect(LoadingSkeleton.Table).toBeDefined();
    expect(LoadingSkeleton.FileExplorer).toBeDefined();
    expect(LoadingSkeleton.Terminal).toBeDefined();
    expect(LoadingSkeleton.Observability).toBeDefined();
    expect(LoadingSkeleton.AgentDetail).toBeDefined();
  });

  it("Card component is a function", () => {
    expect(typeof LoadingSkeleton.Card).toBe("function");
  });

  it("Column component is a function", () => {
    expect(typeof LoadingSkeleton.Column).toBe("function");
  });

  it("Graph component is a function", () => {
    expect(typeof LoadingSkeleton.Graph).toBe("function");
  });

  it("DetailPanel component is a function", () => {
    expect(typeof LoadingSkeleton.DetailPanel).toBe("function");
  });

  it("Table component is a function", () => {
    expect(typeof LoadingSkeleton.Table).toBe("function");
  });

  it("FileExplorer component is a function", () => {
    expect(typeof LoadingSkeleton.FileExplorer).toBe("function");
  });

  it("Terminal component is a function", () => {
    expect(typeof LoadingSkeleton.Terminal).toBe("function");
  });

  it("Observability component is a function", () => {
    expect(typeof LoadingSkeleton.Observability).toBe("function");
  });

  it("AgentDetail component is a function", () => {
    expect(typeof LoadingSkeleton.AgentDetail).toBe("function");
  });

  it("renders all variants without errors", () => {
    expect(() => {
      render(
        <>
          <LoadingSkeleton />
          <LoadingSkeleton shape="rect" />
          <LoadingSkeleton shape="text" />
          <LoadingSkeleton shape="circle" />
          <LoadingSkeleton shape="text" lines={3} />
          <LoadingSkeleton.Card />
          <LoadingSkeleton.Column />
          <LoadingSkeleton.Column cardCount={5} />
          <LoadingSkeleton.Graph />
          <LoadingSkeleton.Monitor />
          <LoadingSkeleton.DetailPanel />
          <LoadingSkeleton.Table />
          <LoadingSkeleton.FileExplorer />
          <LoadingSkeleton.Terminal />
          <LoadingSkeleton.Observability />
          <LoadingSkeleton.AgentDetail />
        </>,
      );
    }).not.toThrow();
  });
});
