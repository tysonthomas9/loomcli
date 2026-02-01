/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for PipelineStage component.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { PipelineStage } from '../PipelineStage';
import type { LoomTaskInfo } from '@/types';

/**
 * Create a mock task for testing.
 */
function createTask(overrides: Partial<LoomTaskInfo> = {}): LoomTaskInfo {
  return {
    id: 'bd-123',
    title: 'Test task title',
    priority: 2,
    status: 'open',
    ...overrides,
  };
}

/**
 * Default props for most tests.
 */
const defaultProps = {
  id: 'test-stage',
  label: 'Test Stage',
  count: 3,
  onClick: vi.fn(),
};

describe('PipelineStage', () => {
  describe('rendering', () => {
    it('renders stage label', () => {
      render(<PipelineStage {...defaultProps} />);

      expect(screen.getByText('Test Stage')).toBeInTheDocument();
    });

    it('renders count', () => {
      render(<PipelineStage {...defaultProps} count={5} />);

      expect(screen.getByText('5')).toBeInTheDocument();
    });

    it('renders icon when provided', () => {
      render(<PipelineStage {...defaultProps} icon="📝" />);

      expect(screen.getByText('📝')).toBeInTheDocument();
    });

    it('does not render icon when not provided', () => {
      const { container } = render(<PipelineStage {...defaultProps} />);

      // Check that there's no icon element
      const stageHeader = container.querySelector('[class*="stageHeader"]');
      expect(stageHeader).toBeInTheDocument();
      // The icon span should not exist
      expect(container.querySelector('[class*="stageIcon"]')).toBeNull();
    });

    it('renders with data-testid', () => {
      render(<PipelineStage {...defaultProps} id="plan" />);

      expect(screen.getByTestId('pipeline-stage-plan')).toBeInTheDocument();
    });

    it('renders aria-label with count', () => {
      render(<PipelineStage {...defaultProps} label="Ready" count={7} />);

      expect(screen.getByLabelText('Ready: 7 items')).toBeInTheDocument();
    });
  });

  describe('oldest item preview', () => {
    it('shows oldest item preview when provided', () => {
      const oldestItem = createTask({ id: 'bd-456', title: 'Oldest task' });

      render(<PipelineStage {...defaultProps} oldestItem={oldestItem} />);

      expect(screen.getByText('bd-456')).toBeInTheDocument();
      expect(screen.getByText('Oldest task')).toBeInTheDocument();
    });

    it('does not show preview when no oldest item', () => {
      render(<PipelineStage {...defaultProps} />);

      expect(screen.queryByText('bd-123')).not.toBeInTheDocument();
    });

    it('shows title with title attribute for tooltip', () => {
      const oldestItem = createTask({ title: 'Very long task title that might be truncated' });

      render(<PipelineStage {...defaultProps} oldestItem={oldestItem} />);

      const titleElement = screen.getByText('Very long task title that might be truncated');
      expect(titleElement).toHaveAttribute('title', 'Very long task title that might be truncated');
    });
  });

  describe('click handling', () => {
    it('calls onClick when clicked with items', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={3} onClick={onClick} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-test-stage'));

      expect(onClick).toHaveBeenCalledTimes(1);
      expect(onClick).toHaveBeenCalledWith('test-stage');
    });

    it('does not call onClick when empty (count is 0)', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={0} onClick={onClick} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-test-stage'));

      expect(onClick).not.toHaveBeenCalled();
    });

    it('calls onClick with correct stage id', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} id="review" count={2} onClick={onClick} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-review'));

      expect(onClick).toHaveBeenCalledWith('review');
    });
  });

  describe('keyboard handling', () => {
    it('calls onClick on Enter key when has items', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={3} onClick={onClick} />);

      fireEvent.keyDown(screen.getByTestId('pipeline-stage-test-stage'), { key: 'Enter' });

      expect(onClick).toHaveBeenCalledTimes(1);
      expect(onClick).toHaveBeenCalledWith('test-stage');
    });

    it('calls onClick on Space key when has items', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={3} onClick={onClick} />);

      fireEvent.keyDown(screen.getByTestId('pipeline-stage-test-stage'), { key: ' ' });

      expect(onClick).toHaveBeenCalledTimes(1);
      expect(onClick).toHaveBeenCalledWith('test-stage');
    });

    it('does not call onClick on Enter when empty', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={0} onClick={onClick} />);

      fireEvent.keyDown(screen.getByTestId('pipeline-stage-test-stage'), { key: 'Enter' });

      expect(onClick).not.toHaveBeenCalled();
    });

    it('does not call onClick on other keys', () => {
      const onClick = vi.fn();

      render(<PipelineStage {...defaultProps} count={3} onClick={onClick} />);

      fireEvent.keyDown(screen.getByTestId('pipeline-stage-test-stage'), { key: 'Tab' });

      expect(onClick).not.toHaveBeenCalled();
    });
  });

  describe('variants', () => {
    it('applies default variant styling', () => {
      const { container } = render(<PipelineStage {...defaultProps} variant="default" />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).not.toContain('Blocked');
    });

    it('applies blocked variant styling', () => {
      const { container } = render(<PipelineStage {...defaultProps} variant="blocked" />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).toContain('Blocked');
    });

    it('defaults to default variant', () => {
      const { container } = render(<PipelineStage {...defaultProps} />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).not.toContain('Blocked');
    });
  });

  describe('empty state styling', () => {
    it('applies empty styling when count is 0', () => {
      const { container } = render(<PipelineStage {...defaultProps} count={0} />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).toContain('Empty');
    });

    it('applies clickable styling when count > 0', () => {
      const { container } = render(<PipelineStage {...defaultProps} count={5} />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).toContain('Clickable');
    });

    it('does not apply clickable styling when count is 0', () => {
      const { container } = render(<PipelineStage {...defaultProps} count={0} />);

      const stage = container.firstChild as HTMLElement;
      expect(stage.className).not.toContain('Clickable');
    });
  });

  describe('accessibility', () => {
    it('has button role when has items', () => {
      render(<PipelineStage {...defaultProps} count={3} />);

      expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('does not have button role when empty', () => {
      render(<PipelineStage {...defaultProps} count={0} />);

      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('has tabIndex 0 when has items', () => {
      render(<PipelineStage {...defaultProps} count={3} />);

      expect(screen.getByTestId('pipeline-stage-test-stage')).toHaveAttribute('tabindex', '0');
    });

    it('does not have tabIndex when empty', () => {
      render(<PipelineStage {...defaultProps} count={0} />);

      expect(screen.getByTestId('pipeline-stage-test-stage')).not.toHaveAttribute('tabindex');
    });
  });

  describe('count highlight', () => {
    it('has data-highlight true when count > 0', () => {
      render(<PipelineStage {...defaultProps} count={5} />);

      const countElement = screen.getByText('5');
      expect(countElement).toHaveAttribute('data-highlight', 'true');
    });

    it('has data-highlight false when count is 0', () => {
      render(<PipelineStage {...defaultProps} count={0} />);

      const countElement = screen.getByText('0');
      expect(countElement).toHaveAttribute('data-highlight', 'false');
    });
  });

  describe('edge cases', () => {
    it('handles very large counts', () => {
      render(<PipelineStage {...defaultProps} count={9999} />);

      expect(screen.getByText('9999')).toBeInTheDocument();
    });

    it('handles long task titles', () => {
      const longTitle = 'This is a very long task title that might need to be truncated in the UI display';
      const oldestItem = createTask({ title: longTitle });

      render(<PipelineStage {...defaultProps} oldestItem={oldestItem} />);

      expect(screen.getByText(longTitle)).toBeInTheDocument();
    });

    it('handles special characters in task id', () => {
      const oldestItem = createTask({ id: 'bd-123/abc' });

      render(<PipelineStage {...defaultProps} oldestItem={oldestItem} />);

      expect(screen.getByText('bd-123/abc')).toBeInTheDocument();
    });

    it('handles empty label', () => {
      render(<PipelineStage {...defaultProps} label="" />);

      expect(screen.getByLabelText(': 3 items')).toBeInTheDocument();
    });
  });
});
