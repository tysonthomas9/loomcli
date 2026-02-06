/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BlockingEdge component.
 */

import '@testing-library/jest-dom';
import { render } from '@testing-library/react';
import { ReactFlowProvider, Position } from '@xyflow/react';
import { describe, it, expect } from 'vitest';

import type { DependencyEdgeData, DependencyType } from '@/types';

import { BlockingEdge } from '../BlockingEdge';

/**
 * Create test props for BlockingEdge component.
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

describe('BlockingEdge', () => {
  describe('rendering', () => {
    it('renders edge path', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      expect(container.querySelector('path')).toBeInTheDocument();
    });

    it('renders with the correct CSS class', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('blockingEdge');
    });

    it('renders edge with correct id', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path[id="edge-1"]');
      expect(path).toBeInTheDocument();
    });
  });

  describe('highlighted state', () => {
    it('applies highlighted class when highlighted', () => {
      const props = createTestProps({ isHighlighted: true });
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('highlighted');
    });

    it('does not apply highlighted class when isHighlighted is false', () => {
      const props = createTestProps({ isHighlighted: false });
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).not.toContain('highlighted');
    });

    it('defaults isHighlighted to false when undefined', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined isHighlighted
      props.data = { ...props.data, isHighlighted: undefined };

      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).not.toContain('highlighted');
    });

    it('combines blockingEdge class with highlighted class', () => {
      const props = createTestProps({ isHighlighted: true });
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      const classAttr = path?.getAttribute('class') ?? '';
      expect(classAttr).toContain('blockingEdge');
      expect(classAttr).toContain('highlighted');
    });
  });

  describe('marker', () => {
    it('passes markerEnd prop through to the rendered path', () => {
      const props = { ...createTestProps(), markerEnd: 'url(#test-marker)' };
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      expect(path).toHaveAttribute('marker-end', 'url(#test-marker)');
    });
  });

  describe('path generation', () => {
    it('generates valid SVG path', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const path = container.querySelector('path.react-flow__edge-path');
      expect(path).toHaveAttribute('d');
      expect(path?.getAttribute('d')).toMatch(/^M/);
    });

    it('includes interaction path for click handling', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(<BlockingEdge {...props} />);

      const interactionPath = container.querySelector('path.react-flow__edge-interaction');
      expect(interactionPath).toBeInTheDocument();
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
        expect(() => renderWithProvider(<BlockingEdge {...props} />)).not.toThrow();
      });
    });
  });

  describe('edge cases', () => {
    it('handles undefined data gracefully', () => {
      const props = createTestProps();
      // @ts-expect-error Testing undefined data
      delete props.data;

      expect(() => renderWithProvider(<BlockingEdge {...props} />)).not.toThrow();
    });
  });
});
