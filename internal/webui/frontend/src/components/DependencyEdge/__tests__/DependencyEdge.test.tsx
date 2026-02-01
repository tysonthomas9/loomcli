/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DependencyEdge component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { ReactFlowProvider } from '@xyflow/react';
import { Position } from '@xyflow/react';

import { DependencyEdge } from '../DependencyEdge';
import type { DependencyEdgeData, DependencyType } from '@/types';

// Mock EdgeLabelRenderer to render children inline (it normally uses a portal)
vi.mock('@xyflow/react', async () => {
  const actual = await vi.importActual('@xyflow/react');
  return {
    ...actual,
    EdgeLabelRenderer: ({ children }: { children: React.ReactNode }) => (
      <foreignObject data-testid="edge-label-renderer">{children}</foreignObject>
    ),
  };
});

/**
 * Create test props for DependencyEdge component.
 */
function createTestProps(overrides: Partial<DependencyEdgeData> = {}) {
  return {
    id: 'edge-1',
    source: 'node-1',
    target: 'node-2',
    sourceX: 0,
    sourceY: 0,
    targetX: 100,
    targetY: 100,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    data: {
      dependencyType: 'blocks' as DependencyType,
      isBlocking: true,
      sourceIssueId: 'beads-123',
      targetIssueId: 'beads-456',
      ...overrides,
    },
  };
}

/**
 * Wrapper with ReactFlowProvider for testing.
 */
function renderWithProvider(ui: React.ReactElement) {
  return render(
    <ReactFlowProvider>
      <svg>{ui}</svg>
    </ReactFlowProvider>
  );
}

describe('DependencyEdge', () => {
  describe('rendering', () => {
    it('renders edge path', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      expect(container.querySelector('path')).toBeInTheDocument();
    });

    it('renders edge with correct id', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path[id="edge-1"]');
      expect(path).toBeInTheDocument();
    });
  });

  describe('type-based styling', () => {
    it('applies typeBlocks class for blocks dependency', () => {
      const props = createTestProps({ dependencyType: 'blocks' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeBlocks');
    });

    it('applies typeParentChild class for parent-child dependency', () => {
      const props = createTestProps({ dependencyType: 'parent-child' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeParentChild');
    });

    it('applies typeConditionalBlocks class for conditional-blocks dependency', () => {
      const props = createTestProps({ dependencyType: 'conditional-blocks' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeConditionalBlocks');
    });

    it('applies typeWaitsFor class for waits-for dependency', () => {
      const props = createTestProps({ dependencyType: 'waits-for' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeWaitsFor');
    });

    it('applies typeRelated class for related dependency', () => {
      const props = createTestProps({ dependencyType: 'related' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeRelated');
    });

    it('applies typeDefault class for unknown dependency type', () => {
      const props = createTestProps({ dependencyType: 'custom-type' as DependencyType });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeDefault');
    });

    it('applies typeDefault class for discovered-from (unknown to styling)', () => {
      const props = createTestProps({ dependencyType: 'discovered-from' });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeDefault');
    });
  });

  describe('data-type attribute', () => {
    it('adds data-type attribute to label with dependency type', () => {
      const props = createTestProps({ dependencyType: 'parent-child' });
      renderWithProvider(<DependencyEdge {...props} />);

      const label = screen.getByText('parent-child');
      expect(label).toHaveAttribute('data-type', 'parent-child');
    });

    it('adds data-type attribute for blocks type', () => {
      const props = createTestProps({ dependencyType: 'blocks' });
      renderWithProvider(<DependencyEdge {...props} />);

      const label = screen.getByText('blocks');
      expect(label).toHaveAttribute('data-type', 'blocks');
    });
  });

  describe('highlighted state with type styling', () => {
    it('combines type class with highlighted class', () => {
      const props = createTestProps({ dependencyType: 'blocks', isHighlighted: true });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeBlocks');
      expect(classAttr).toContain('highlighted');
    });

    it('combines parent-child type with highlighted class', () => {
      const props = createTestProps({ dependencyType: 'parent-child', isHighlighted: true });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeParentChild');
      expect(classAttr).toContain('highlighted');
    });
  });

  describe('edge cases', () => {
    it('handles undefined data gracefully', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined data
      delete props.data;

      // Should not throw
      expect(() =>
        renderWithProvider(<DependencyEdge {...props} />)
      ).not.toThrow();
    });

    it('defaults to typeBlocks when dependencyType is undefined', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined dependencyType
      props.data = { ...props.data, dependencyType: undefined };

      const { container } = renderWithProvider(<DependencyEdge {...props} />);
      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeBlocks');
    });

    it('handles various coordinate positions', () => {
      const positions = [
        { sourceX: 0, sourceY: 0, targetX: 100, targetY: 100 },
        { sourceX: 100, sourceY: 0, targetX: 0, targetY: 100 },
        { sourceX: 50, sourceY: 50, targetX: 50, targetY: 150 },
        { sourceX: 0, sourceY: 100, targetX: 200, targetY: 0 },
      ];

      positions.forEach((coords) => {
        const props = { ...createTestProps(), ...coords };
        expect(() =>
          renderWithProvider(<DependencyEdge {...props} />)
        ).not.toThrow();
      });
    });
  });

  describe('highlighted state', () => {
    it('applies highlighted class when isHighlighted is true', () => {
      const props = createTestProps({ isHighlighted: true });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('highlighted');
    });

    it('does not apply highlighted class when isHighlighted is false', () => {
      const props = createTestProps({ isHighlighted: false });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).not.toContain('highlighted');
    });

    it('combines highlighted with blocking class', () => {
      const props = createTestProps({ isBlocking: true, isHighlighted: true });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeBlocks');
      expect(classAttr).toContain('highlighted');
    });

    it('combines highlighted with related type class', () => {
      const props = createTestProps({ dependencyType: 'related', isHighlighted: true });
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('typeRelated');
      expect(classAttr).toContain('highlighted');
    });

    it('defaults isHighlighted to false when undefined', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined isHighlighted
      props.data = { ...props.data, isHighlighted: undefined };

      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).not.toContain('highlighted');
    });
  });

  describe('marker', () => {
    it('renders with arrow marker end', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      expect(path).toHaveAttribute('marker-end', 'url(#arrow)');
    });
  });

  describe('path generation', () => {
    it('generates valid SVG path', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      expect(path).toHaveAttribute('d');
      // SVG paths start with M (moveto)
      expect(path?.getAttribute('d')).toMatch(/^M/);
    });

    it('includes interaction path for click handling', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<DependencyEdge {...props} />);

      // React Flow adds an interaction path for easier clicking
      const interactionPath = container.querySelector(
        'path.react-flow__edge-interaction'
      );
      expect(interactionPath).toBeInTheDocument();
    });
  });

  describe('dependency type labels', () => {
    // Parameterized test for all 10 dependency types
    const dependencyTypes = [
      'blocks',
      'parent-child',
      'conditional-blocks',
      'waits-for',
      'related',
      'discovered-from',
      'replies-to',
      'relates-to',
      'duplicates',
      'supersedes',
    ] as const;

    it.each(dependencyTypes)(
      'displays "%s" label for dependency type %s',
      (dependencyType) => {
        const props = createTestProps({ dependencyType });
        renderWithProvider(<DependencyEdge {...props} />);

        // EdgeLabelRenderer creates a portal, find label by text content
        const label = screen.getByText(dependencyType);
        expect(label).toBeInTheDocument();
      }
    );

    it('displays dependency type as-is without transformation', () => {
      // Use a mixed-case type to verify no casing transformation occurs
      const props = createTestProps({ dependencyType: 'parent-child' as DependencyType });
      renderWithProvider(<DependencyEdge {...props} />);

      // Should be exactly 'parent-child', not 'Parent-Child' or 'PARENT-CHILD'
      const label = screen.getByText('parent-child');
      expect(label).toBeInTheDocument();
      expect(label.textContent).toBe('parent-child');
    });

    it('defaults to "blocks" when dependencyType is undefined', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined dependencyType
      props.data = { ...props.data, dependencyType: undefined };

      renderWithProvider(<DependencyEdge {...props} />);

      // Should default to 'blocks' when dependencyType is undefined
      const label = screen.getByText('blocks');
      expect(label).toBeInTheDocument();
    });
  });

  describe('selected state label styling', () => {
    it('applies selected class to label when selected is true', () => {
      const props = { ...createTestProps(), selected: true };
      renderWithProvider(<DependencyEdge {...props} />);

      // Find label by text, then check for selected class
      const label = screen.getByText('blocks');
      expect(label.className).toContain('selected');
    });

    it('does not apply selected class when selected is false', () => {
      const props = { ...createTestProps(), selected: false };
      renderWithProvider(<DependencyEdge {...props} />);

      // Find label by text, then check it doesn't have selected class
      const label = screen.getByText('blocks');
      expect(label.className).not.toContain('selected');
    });
  });
});
