/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LoadingSkeleton component.
 */

import { render } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import { LoadingSkeleton } from '../LoadingSkeleton';

describe('LoadingSkeleton', () => {
  describe('base component rendering', () => {
    it('renders with default props', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toBeInTheDocument();
      expect(skeleton.tagName).toBe('DIV');
    });

    it('renders with aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toHaveAttribute('aria-hidden', 'true');
    });

    it('applies skeleton base class', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('skeleton');
    });
  });

  describe('shape variants', () => {
    it('renders rect shape by default', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('rect');
    });

    it('renders rect shape when explicitly set', () => {
      const { container } = render(<LoadingSkeleton shape="rect" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('rect');
    });

    it('renders text shape correctly', () => {
      const { container } = render(<LoadingSkeleton shape="text" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('text');
    });

    it('renders circle shape correctly', () => {
      const { container } = render(<LoadingSkeleton shape="circle" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('circle');
    });
  });

  describe('custom dimensions', () => {
    it('applies width as number (converts to pixels)', () => {
      const { container } = render(<LoadingSkeleton width={100} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe('100px');
    });

    it('applies height as number (converts to pixels)', () => {
      const { container } = render(<LoadingSkeleton height={50} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe('50px');
    });

    it('applies width as string (CSS value)', () => {
      const { container } = render(<LoadingSkeleton width="100%" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe('100%');
    });

    it('applies height as string (CSS value)', () => {
      const { container } = render(<LoadingSkeleton height="2rem" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe('2rem');
    });

    it('applies both width and height together', () => {
      const { container } = render(<LoadingSkeleton width={200} height={100} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe('200px');
      expect(skeleton.style.height).toBe('100px');
    });

    it('does not add style attribute when dimensions are undefined', () => {
      const { container } = render(<LoadingSkeleton />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.getAttribute('style')).toBeNull();
    });
  });

  describe('custom className', () => {
    it('applies custom className to base skeleton', () => {
      const { container } = render(<LoadingSkeleton className="custom-class" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toHaveClass('custom-class');
    });

    it('preserves skeleton and shape classes when custom className is added', () => {
      const { container } = render(<LoadingSkeleton shape="circle" className="my-custom-class" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('skeleton');
      expect(skeleton.className).toContain('circle');
      expect(skeleton).toHaveClass('my-custom-class');
    });
  });

  describe('multiple text lines', () => {
    it('renders single line by default for text shape', () => {
      const { container } = render(<LoadingSkeleton shape="text" />);

      // Single line renders as single div, not container
      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('text');
      expect(skeleton.className).not.toContain('textContainer');
    });

    it('renders multiple lines when lines > 1', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={3} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer.className).toContain('textContainer');
      expect(textContainer.children.length).toBe(3);
    });

    it('renders correct number of lines', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={5} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer.children.length).toBe(5);
    });

    it('text container has aria-hidden="true"', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={2} />);

      const textContainer = container.firstChild as HTMLElement;
      expect(textContainer).toHaveAttribute('aria-hidden', 'true');
    });

    it('last line has reduced width (60%)', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={3} width={100} />);

      const textContainer = container.firstChild as HTMLElement;
      const lines = textContainer.children;

      // First and second lines should have full width
      expect((lines[0] as HTMLElement).style.width).toBe('100px');
      expect((lines[1] as HTMLElement).style.width).toBe('100px');
      // Last line should be 60%
      expect((lines[2] as HTMLElement).style.width).toBe('60%');
    });

    it('applies height to each line', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={2} height={20} />);

      const textContainer = container.firstChild as HTMLElement;
      const lines = textContainer.children;

      expect((lines[0] as HTMLElement).style.height).toBe('20px');
      expect((lines[1] as HTMLElement).style.height).toBe('20px');
    });

    it('lines prop is ignored for non-text shapes', () => {
      const { container } = render(<LoadingSkeleton shape="rect" lines={3} />);

      // Should render single rect, not multiple
      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('rect');
      expect(skeleton.children.length).toBe(0);
    });
  });

  describe('edge cases', () => {
    it('handles lines = 1 as single element (not container)', () => {
      const { container } = render(<LoadingSkeleton shape="text" lines={1} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.className).toContain('text');
      expect(skeleton.className).not.toContain('textContainer');
    });

    it('handles width = 0', () => {
      const { container } = render(<LoadingSkeleton width={0} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.width).toBe('0px');
    });

    it('handles height = 0', () => {
      const { container } = render(<LoadingSkeleton height={0} />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton.style.height).toBe('0px');
    });

    it('handles empty string className', () => {
      const { container } = render(<LoadingSkeleton className="" />);

      const skeleton = container.firstChild as HTMLElement;
      expect(skeleton).toBeInTheDocument();
    });
  });
});

describe('LoadingSkeleton.Card', () => {
  describe('rendering', () => {
    it('renders with card structure', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const card = container.firstChild as HTMLElement;
      expect(card).toBeInTheDocument();
      expect(card.className).toContain('card');
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const card = container.firstChild as HTMLElement;
      expect(card).toHaveAttribute('aria-hidden', 'true');
    });

    it('contains card header section', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const cardHeader = container.querySelector('[class*="cardHeader"]');
      expect(cardHeader).toBeInTheDocument();
    });

    it('contains skeleton elements in header', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      const cardHeader = container.querySelector('[class*="cardHeader"]');
      expect(cardHeader?.children.length).toBe(2);
    });

    it('contains text skeleton for body content', () => {
      const { container } = render(<LoadingSkeleton.Card />);

      // Card should have header + text content (with 2 lines)
      const card = container.firstChild as HTMLElement;
      // Card has cardHeader and textContainer for body
      expect(card.children.length).toBe(2);
    });
  });

  describe('custom className', () => {
    it('applies custom className to card', () => {
      const { container } = render(<LoadingSkeleton.Card className="custom-card-class" />);

      const card = container.firstChild as HTMLElement;
      expect(card).toHaveClass('custom-card-class');
    });

    it('preserves card class when custom className is added', () => {
      const { container } = render(<LoadingSkeleton.Card className="my-class" />);

      const card = container.firstChild as HTMLElement;
      expect(card.className).toContain('card');
      expect(card).toHaveClass('my-class');
    });
  });
});

describe('LoadingSkeleton.Column', () => {
  describe('rendering', () => {
    it('renders with column structure', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const column = container.firstChild as HTMLElement;
      expect(column).toBeInTheDocument();
      expect(column.className).toContain('column');
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const column = container.firstChild as HTMLElement;
      expect(column).toHaveAttribute('aria-hidden', 'true');
    });

    it('contains column header section', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnHeader = container.querySelector('[class*="columnHeader"]');
      expect(columnHeader).toBeInTheDocument();
    });

    it('contains skeleton elements in header', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnHeader = container.querySelector('[class*="columnHeader"]');
      // Header has text skeleton + circle skeleton
      expect(columnHeader?.children.length).toBe(2);
    });

    it('contains column content section', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent).toBeInTheDocument();
    });
  });

  describe('cardCount prop', () => {
    it('renders 3 cards by default', () => {
      const { container } = render(<LoadingSkeleton.Column />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(3);
    });

    it('renders custom number of cards', () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={5} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(5);
    });

    it('renders 1 card when cardCount is 1', () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={1} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(1);
    });

    it('renders 0 cards when cardCount is 0', () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={0} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      expect(columnContent?.children.length).toBe(0);
    });

    it('each card in column has card class', () => {
      const { container } = render(<LoadingSkeleton.Column cardCount={2} />);

      const columnContent = container.querySelector('[class*="columnContent"]');
      const cards = columnContent?.children;

      expect((cards?.[0] as HTMLElement).className).toContain('card');
      expect((cards?.[1] as HTMLElement).className).toContain('card');
    });
  });

  describe('custom className', () => {
    it('applies custom className to column', () => {
      const { container } = render(<LoadingSkeleton.Column className="custom-column-class" />);

      const column = container.firstChild as HTMLElement;
      expect(column).toHaveClass('custom-column-class');
    });

    it('preserves column class when custom className is added', () => {
      const { container } = render(<LoadingSkeleton.Column className="my-column" />);

      const column = container.firstChild as HTMLElement;
      expect(column.className).toContain('column');
      expect(column).toHaveClass('my-column');
    });
  });
});

describe('LoadingSkeleton.Graph', () => {
  describe('rendering', () => {
    it('renders with data-testid="loading-skeleton-graph"', () => {
      const { getByTestId } = render(<LoadingSkeleton.Graph />);

      const graph = getByTestId('loading-skeleton-graph');
      expect(graph).toBeInTheDocument();
    });

    it('renders with graph structure', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graph = container.firstChild as HTMLElement;
      expect(graph).toBeInTheDocument();
      expect(graph.className).toContain('graph');
    });

    it('has aria-hidden="true" for accessibility', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graph = container.firstChild as HTMLElement;
      expect(graph).toHaveAttribute('aria-hidden', 'true');
    });
  });

  describe('graph nodes', () => {
    it('contains graphNodes section', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      expect(graphNodes).toBeInTheDocument();
    });

    it('contains 3 simulated graph nodes', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      const nodes = graphNodes?.querySelectorAll('[class*="graphNode"]');
      expect(nodes?.length).toBe(3);
    });

    it('each node contains a skeleton rectangle', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const graphNodes = container.querySelector('[class*="graphNodes"]');
      const nodes = graphNodes?.querySelectorAll('[class*="graphNode"]');

      nodes?.forEach((node) => {
        const skeleton = node.querySelector('[class*="skeleton"]');
        expect(skeleton).toBeInTheDocument();
        expect(skeleton?.className).toContain('rect');
      });
    });
  });

  describe('minimap placeholder', () => {
    it('contains minimap placeholder section', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const miniMap = container.querySelector('[class*="graphMiniMap"]');
      expect(miniMap).toBeInTheDocument();
    });

    it('minimap contains a skeleton rectangle', () => {
      const { container } = render(<LoadingSkeleton.Graph />);

      const miniMap = container.querySelector('[class*="graphMiniMap"]');
      const skeleton = miniMap?.querySelector('[class*="skeleton"]');
      expect(skeleton).toBeInTheDocument();
      expect(skeleton?.className).toContain('rect');
    });
  });

  describe('custom className', () => {
    it('applies custom className to graph', () => {
      const { container } = render(<LoadingSkeleton.Graph className="custom-graph-class" />);

      const graph = container.firstChild as HTMLElement;
      expect(graph).toHaveClass('custom-graph-class');
    });

    it('preserves graph class when custom className is added', () => {
      const { container } = render(<LoadingSkeleton.Graph className="my-graph" />);

      const graph = container.firstChild as HTMLElement;
      expect(graph.className).toContain('graph');
      expect(graph).toHaveClass('my-graph');
    });
  });
});

describe('LoadingSkeleton integration', () => {
  it('Card, Column, and Graph are accessible as static properties', () => {
    expect(LoadingSkeleton.Card).toBeDefined();
    expect(LoadingSkeleton.Column).toBeDefined();
    expect(LoadingSkeleton.Graph).toBeDefined();
  });

  it('Card component is a function', () => {
    expect(typeof LoadingSkeleton.Card).toBe('function');
  });

  it('Column component is a function', () => {
    expect(typeof LoadingSkeleton.Column).toBe('function');
  });

  it('Graph component is a function', () => {
    expect(typeof LoadingSkeleton.Graph).toBe('function');
  });

  it('renders all variants without errors', () => {
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
        </>
      );
    }).not.toThrow();
  });
});
