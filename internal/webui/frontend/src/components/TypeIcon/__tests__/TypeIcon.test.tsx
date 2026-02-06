/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TypeIcon component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import { TypeIcon } from '../TypeIcon';

describe('TypeIcon', () => {
  describe('rendering', () => {
    it('renders an SVG element', () => {
      const { container } = render(<TypeIcon type="bug" />);

      expect(container.querySelector('svg')).toBeInTheDocument();
    });

    it('renders with role="img"', () => {
      render(<TypeIcon type="task" />);

      expect(screen.getByRole('img')).toBeInTheDocument();
    });

    it('renders with correct viewBox', () => {
      const { container } = render(<TypeIcon type="feature" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('viewBox', '0 0 24 24');
    });

    it('renders a path element inside SVG', () => {
      const { container } = render(<TypeIcon type="epic" />);

      const path = container.querySelector('svg path');
      expect(path).toBeInTheDocument();
      expect(path).toHaveAttribute('d');
    });
  });

  describe('known issue types', () => {
    it.each([
      ['bug', 'bug'],
      ['feature', 'feature'],
      ['task', 'task'],
      ['epic', 'epic'],
      ['chore', 'chore'],
    ] as const)('renders %s type with correct data-type attribute', (type, expectedDataType) => {
      const { container } = render(<TypeIcon type={type} />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('data-type', expectedDataType);
    });

    it('renders different path for bug type', () => {
      const { container: bugContainer } = render(<TypeIcon type="bug" />);
      const { container: taskContainer } = render(<TypeIcon type="task" />);

      const bugPath = bugContainer.querySelector('path')?.getAttribute('d');
      const taskPath = taskContainer.querySelector('path')?.getAttribute('d');

      expect(bugPath).not.toBe(taskPath);
    });
  });

  describe('fallback behavior', () => {
    it('renders unknown type for undefined', () => {
      const { container } = render(<TypeIcon type={undefined} />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('data-type', 'unknown');
    });

    it('renders unknown type for empty string', () => {
      // @ts-expect-error Testing empty string type
      const { container } = render(<TypeIcon type="" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('data-type', 'unknown');
    });

    it('renders unknown type for custom/unknown type', () => {
      const { container } = render(<TypeIcon type="custom-type" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('data-type', 'unknown');
    });
  });

  describe('size prop', () => {
    it('uses default size of 16', () => {
      const { container } = render(<TypeIcon type="bug" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('width', '16');
      expect(svg).toHaveAttribute('height', '16');
    });

    it('applies custom size', () => {
      const { container } = render(<TypeIcon type="bug" size={24} />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('width', '24');
      expect(svg).toHaveAttribute('height', '24');
    });

    it('handles small size', () => {
      const { container } = render(<TypeIcon type="bug" size={12} />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('width', '12');
      expect(svg).toHaveAttribute('height', '12');
    });

    it('handles large size', () => {
      const { container } = render(<TypeIcon type="bug" size={48} />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('width', '48');
      expect(svg).toHaveAttribute('height', '48');
    });
  });

  describe('className prop', () => {
    it('applies custom className', () => {
      const { container } = render(<TypeIcon type="bug" className="custom-class" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveClass('custom-class');
    });

    it('combines with default styles', () => {
      const { container } = render(<TypeIcon type="bug" className="my-class" />);

      const svg = container.querySelector('svg');
      // Should have both the module class and the custom class
      expect(svg?.classList.length).toBeGreaterThanOrEqual(2);
      expect(svg).toHaveClass('my-class');
    });
  });

  describe('accessibility', () => {
    it('has aria-label with type name', () => {
      render(<TypeIcon type="bug" />);

      expect(screen.getByLabelText('bug type')).toBeInTheDocument();
    });

    it('has aria-label for each known type', () => {
      const types = ['bug', 'feature', 'task', 'epic', 'chore'] as const;

      types.forEach((type) => {
        const { unmount } = render(<TypeIcon type={type} />);
        expect(screen.getByLabelText(`${type} type`)).toBeInTheDocument();
        unmount();
      });
    });

    it('has aria-label for unknown type when undefined', () => {
      render(<TypeIcon type={undefined} />);

      expect(screen.getByLabelText('unknown type')).toBeInTheDocument();
    });

    it('uses custom type name in aria-label for unknown types', () => {
      render(<TypeIcon type="my-custom-type" />);

      expect(screen.getByLabelText('my-custom-type type')).toBeInTheDocument();
    });

    it('applies custom aria-label when provided', () => {
      render(<TypeIcon type="bug" aria-label="Bug issue icon" />);

      expect(screen.getByLabelText('Bug issue icon')).toBeInTheDocument();
    });
  });

  describe('fill attribute', () => {
    it('uses currentColor for fill', () => {
      const { container } = render(<TypeIcon type="bug" />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('fill', 'currentColor');
    });
  });
});
