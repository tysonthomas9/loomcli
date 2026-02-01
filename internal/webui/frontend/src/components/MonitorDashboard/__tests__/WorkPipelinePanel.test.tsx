/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkPipelinePanel component.
 */

import { describe, it, expect, vi as _vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import '@testing-library/jest-dom';

import { WorkPipelinePanel } from '../WorkPipelinePanel';
import type { LoomTaskSummary, LoomTaskLists, LoomTaskInfo } from '@/types';

/**
 * Create a mock task for testing.
 */
function createTask(overrides: Partial<LoomTaskInfo> = {}): LoomTaskInfo {
  return {
    id: 'bd-123',
    title: 'Test task',
    priority: 2,
    status: 'open',
    ...overrides,
  };
}

/**
 * Create mock task summary for testing.
 */
function createTaskSummary(overrides: Partial<LoomTaskSummary> = {}): LoomTaskSummary {
  return {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    blocked: 0,
    ...overrides,
  };
}

/**
 * Create mock task lists for testing.
 */
function createTaskLists(overrides: Partial<LoomTaskLists> = {}): LoomTaskLists {
  return {
    needsPlanning: [],
    readyToImplement: [],
    inProgress: [],
    needsReview: [],
    blocked: [],
    ...overrides,
  };
}

/**
 * Default props for most tests.
 */
const defaultProps = {
  tasks: createTaskSummary(),
  taskLists: createTaskLists(),
};

describe('WorkPipelinePanel', () => {
  describe('rendering', () => {
    it('renders with data-testid', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByTestId('work-pipeline-panel')).toBeInTheDocument();
    });

    it('renders all main pipeline stages', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByTestId('pipeline-stage-plan')).toBeInTheDocument();
      expect(screen.getByTestId('pipeline-stage-ready')).toBeInTheDocument();
      expect(screen.getByTestId('pipeline-stage-inProgress')).toBeInTheDocument();
      expect(screen.getByTestId('pipeline-stage-review')).toBeInTheDocument();
    });

    it('renders Done stage', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByText('Done')).toBeInTheDocument();
    });

    it('renders stage labels', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByText('Plan')).toBeInTheDocument();
      expect(screen.getByText('Ready')).toBeInTheDocument();
      expect(screen.getByText('In Progress')).toBeInTheDocument();
      expect(screen.getByText('Review')).toBeInTheDocument();
    });

    it('renders arrows between stages', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      // There should be arrows connecting stages (rendered as →)
      const arrows = screen.getAllByText('→');
      // 4 main stages + 1 to Done = 4 arrows between them + 1 to Done
      expect(arrows.length).toBeGreaterThanOrEqual(4);
    });

    it('applies custom className', () => {
      render(<WorkPipelinePanel {...defaultProps} className="custom-class" />);

      expect(screen.getByTestId('work-pipeline-panel')).toHaveClass('custom-class');
    });
  });

  describe('stage counts', () => {
    it('shows correct count for Plan stage', () => {
      const tasks = createTaskSummary({ needs_planning: 5 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      const planStage = screen.getByTestId('pipeline-stage-plan');
      expect(within(planStage).getByText('5')).toBeInTheDocument();
    });

    it('shows correct count for Ready stage', () => {
      const tasks = createTaskSummary({ ready_to_implement: 3 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      const readyStage = screen.getByTestId('pipeline-stage-ready');
      expect(within(readyStage).getByText('3')).toBeInTheDocument();
    });

    it('shows correct count for In Progress stage', () => {
      const tasks = createTaskSummary({ in_progress: 2 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      const inProgressStage = screen.getByTestId('pipeline-stage-inProgress');
      expect(within(inProgressStage).getByText('2')).toBeInTheDocument();
    });

    it('shows correct count for Review stage', () => {
      const tasks = createTaskSummary({ need_review: 4 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      const reviewStage = screen.getByTestId('pipeline-stage-review');
      expect(within(reviewStage).getByText('4')).toBeInTheDocument();
    });

    it('shows all counts correctly', () => {
      const tasks = createTaskSummary({
        needs_planning: 1,
        ready_to_implement: 2,
        in_progress: 3,
        need_review: 4,
        blocked: 5,
      });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      expect(within(screen.getByTestId('pipeline-stage-plan')).getByText('1')).toBeInTheDocument();
      expect(within(screen.getByTestId('pipeline-stage-ready')).getByText('2')).toBeInTheDocument();
      expect(
        within(screen.getByTestId('pipeline-stage-inProgress')).getByText('3')
      ).toBeInTheDocument();
      expect(
        within(screen.getByTestId('pipeline-stage-review')).getByText('4')
      ).toBeInTheDocument();
    });
  });

  describe('blocked branch', () => {
    it('renders blocked branch when blocked count > 0', () => {
      const tasks = createTaskSummary({ blocked: 3 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      expect(screen.getByTestId('pipeline-stage-blocked')).toBeInTheDocument();
      expect(screen.getByText('Backlog')).toBeInTheDocument();
    });

    it('shows correct blocked count', () => {
      const tasks = createTaskSummary({ blocked: 7 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      const blockedStage = screen.getByTestId('pipeline-stage-blocked');
      expect(within(blockedStage).getByText('7')).toBeInTheDocument();
    });

    it('does not render blocked branch when blocked count is 0', () => {
      const tasks = createTaskSummary({ blocked: 0 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      expect(screen.queryByTestId('pipeline-stage-blocked')).not.toBeInTheDocument();
    });

    it('renders branch line arrow for blocked', () => {
      const tasks = createTaskSummary({ blocked: 2 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      expect(screen.getByText('↳')).toBeInTheDocument();
    });
  });

  describe('oldest item preview', () => {
    it('shows oldest item in Plan stage', () => {
      const taskLists = createTaskLists({
        needsPlanning: [
          createTask({ id: 'bd-plan-1', title: 'Planning task' }),
          createTask({ id: 'bd-plan-2', title: 'Another task' }),
        ],
      });
      const tasks = createTaskSummary({ needs_planning: 2 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const planStage = screen.getByTestId('pipeline-stage-plan');
      expect(within(planStage).getByText('bd-plan-1')).toBeInTheDocument();
      expect(within(planStage).getByText('Planning task')).toBeInTheDocument();
    });

    it('shows oldest item in Ready stage', () => {
      const taskLists = createTaskLists({
        readyToImplement: [createTask({ id: 'bd-ready-1', title: 'Ready task' })],
      });
      const tasks = createTaskSummary({ ready_to_implement: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const readyStage = screen.getByTestId('pipeline-stage-ready');
      expect(within(readyStage).getByText('bd-ready-1')).toBeInTheDocument();
    });

    it('shows oldest item in In Progress stage', () => {
      const taskLists = createTaskLists({
        inProgress: [createTask({ id: 'bd-ip-1', title: 'In progress task' })],
      });
      const tasks = createTaskSummary({ in_progress: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const inProgressStage = screen.getByTestId('pipeline-stage-inProgress');
      expect(within(inProgressStage).getByText('bd-ip-1')).toBeInTheDocument();
    });

    it('shows oldest item in Review stage', () => {
      const taskLists = createTaskLists({
        needsReview: [createTask({ id: 'bd-review-1', title: 'Review task' })],
      });
      const tasks = createTaskSummary({ need_review: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const reviewStage = screen.getByTestId('pipeline-stage-review');
      expect(within(reviewStage).getByText('bd-review-1')).toBeInTheDocument();
    });

    it('shows oldest item in Blocked stage', () => {
      const taskLists = createTaskLists({
        blocked: [createTask({ id: 'bd-blocked-1', title: 'Blocked task' })],
      });
      const tasks = createTaskSummary({ blocked: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const blockedStage = screen.getByTestId('pipeline-stage-blocked');
      expect(within(blockedStage).getByText('bd-blocked-1')).toBeInTheDocument();
    });
  });

  describe('oldest items table', () => {
    it('renders oldest items table header', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByText('Oldest in Each Stage')).toBeInTheDocument();
    });

    it('renders table column headers', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByRole('columnheader', { name: 'Stage' })).toBeInTheDocument();
      expect(screen.getByRole('columnheader', { name: 'Task' })).toBeInTheDocument();
      expect(screen.getByRole('columnheader', { name: 'Priority' })).toBeInTheDocument();
    });

    it('shows oldest items from each stage in table', () => {
      const taskLists = createTaskLists({
        needsPlanning: [createTask({ id: 'bd-1', title: 'Plan task', priority: 1 })],
        readyToImplement: [createTask({ id: 'bd-2', title: 'Ready task', priority: 2 })],
        inProgress: [createTask({ id: 'bd-3', title: 'Progress task', priority: 0 })],
        needsReview: [createTask({ id: 'bd-4', title: 'Review task', priority: 3 })],
      });
      const tasks = createTaskSummary({
        needs_planning: 1,
        ready_to_implement: 1,
        in_progress: 1,
        need_review: 1,
      });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      // Check table contains task info
      const table = screen.getByRole('table');
      expect(within(table).getByText('bd-1')).toBeInTheDocument();
      expect(within(table).getByText('bd-2')).toBeInTheDocument();
      expect(within(table).getByText('bd-3')).toBeInTheDocument();
      expect(within(table).getByText('bd-4')).toBeInTheDocument();

      // Check priorities
      expect(within(table).getByText('P1')).toBeInTheDocument();
      expect(within(table).getByText('P2')).toBeInTheDocument();
      expect(within(table).getByText('P0')).toBeInTheDocument();
      expect(within(table).getByText('P3')).toBeInTheDocument();
    });

    it('does not show rows for empty stages', () => {
      const taskLists = createTaskLists({
        needsPlanning: [createTask({ id: 'bd-1', title: 'Plan task' })],
        readyToImplement: [], // Empty
        inProgress: [], // Empty
        needsReview: [], // Empty
      });
      const tasks = createTaskSummary({ needs_planning: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const table = screen.getByRole('table');
      // Only Plan should have a row
      const rows = within(table).getAllByRole('row');
      // 1 header row + 1 data row
      expect(rows.length).toBe(2);
    });
  });

  describe('TaskDrawer interaction', () => {
    it('opens TaskDrawer when clicking a stage with items', () => {
      const taskLists = createTaskLists({
        needsPlanning: [createTask({ id: 'bd-1', title: 'Task 1' })],
      });
      const tasks = createTaskSummary({ needs_planning: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-plan'));

      // TaskDrawer should open with the title
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByText('Needs Planning')).toBeInTheDocument();
    });

    it('shows correct tasks in drawer for Plan stage', () => {
      const taskLists = createTaskLists({
        needsPlanning: [
          createTask({ id: 'bd-1', title: 'Task 1' }),
          createTask({ id: 'bd-2', title: 'Task 2' }),
        ],
      });
      const tasks = createTaskSummary({ needs_planning: 2 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-plan'));

      const drawer = screen.getByRole('dialog');
      expect(within(drawer).getByText('Task 1')).toBeInTheDocument();
      expect(within(drawer).getByText('Task 2')).toBeInTheDocument();
    });

    it('shows correct tasks in drawer for Ready stage', () => {
      const taskLists = createTaskLists({
        readyToImplement: [createTask({ id: 'bd-ready', title: 'Ready task' })],
      });
      const tasks = createTaskSummary({ ready_to_implement: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-ready'));

      expect(screen.getByText('Ready to Implement')).toBeInTheDocument();
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('shows correct tasks in drawer for In Progress stage', () => {
      const taskLists = createTaskLists({
        inProgress: [createTask({ id: 'bd-ip', title: 'In progress task' })],
      });
      const tasks = createTaskSummary({ in_progress: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-inProgress'));

      // Drawer should open - check for the dialog role
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      // Check that the drawer title shows "In Progress" with count
      const drawer = screen.getByRole('dialog');
      expect(within(drawer).getByText('In progress task')).toBeInTheDocument();
    });

    it('shows correct tasks in drawer for Review stage', () => {
      const taskLists = createTaskLists({
        needsReview: [createTask({ id: 'bd-review', title: 'Review task' })],
      });
      const tasks = createTaskSummary({ need_review: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-review'));

      expect(screen.getByText('Needs Review')).toBeInTheDocument();
    });

    it('shows correct tasks in drawer for Blocked stage', () => {
      const taskLists = createTaskLists({
        blocked: [createTask({ id: 'bd-blocked', title: 'Blocked task' })],
      });
      const tasks = createTaskSummary({ blocked: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-blocked'));

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      // The drawer title shows "Backlog"
      const drawer = screen.getByRole('dialog');
      expect(within(drawer).getByText('Blocked task')).toBeInTheDocument();
    });

    it('closes TaskDrawer when close button clicked', () => {
      const taskLists = createTaskLists({
        needsPlanning: [createTask({ id: 'bd-1', title: 'Task 1' })],
      });
      const tasks = createTaskSummary({ needs_planning: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      // Open drawer
      fireEvent.click(screen.getByTestId('pipeline-stage-plan'));
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      // Close drawer
      fireEvent.click(screen.getByLabelText('Close drawer'));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('does not open drawer when clicking empty stage', () => {
      const tasks = createTaskSummary({ needs_planning: 0 });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      fireEvent.click(screen.getByTestId('pipeline-stage-plan'));

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles all empty stages', () => {
      render(<WorkPipelinePanel {...defaultProps} />);

      expect(screen.getByTestId('work-pipeline-panel')).toBeInTheDocument();
      // All stages should show 0
      expect(within(screen.getByTestId('pipeline-stage-plan')).getByText('0')).toBeInTheDocument();
      expect(within(screen.getByTestId('pipeline-stage-ready')).getByText('0')).toBeInTheDocument();
    });

    it('handles large counts', () => {
      const tasks = createTaskSummary({
        needs_planning: 999,
        ready_to_implement: 500,
        in_progress: 100,
        need_review: 50,
        blocked: 25,
      });

      render(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);

      expect(
        within(screen.getByTestId('pipeline-stage-plan')).getByText('999')
      ).toBeInTheDocument();
      expect(
        within(screen.getByTestId('pipeline-stage-blocked')).getByText('25')
      ).toBeInTheDocument();
    });

    it('handles tasks with long titles in table', () => {
      const longTitle = 'This is a very long task title that should be handled properly';
      const taskLists = createTaskLists({
        needsPlanning: [createTask({ id: 'bd-1', title: longTitle })],
      });
      const tasks = createTaskSummary({ needs_planning: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      const table = screen.getByRole('table');
      const titleElement = within(table).getByText(longTitle);
      expect(titleElement).toHaveAttribute('title', longTitle);
    });
  });

  describe('accessibility', () => {
    it('maintains testid in all states', () => {
      const { rerender } = render(<WorkPipelinePanel {...defaultProps} />);
      expect(screen.getByTestId('work-pipeline-panel')).toBeInTheDocument();

      // With tasks
      const tasks = createTaskSummary({
        needs_planning: 5,
        blocked: 3,
      });
      rerender(<WorkPipelinePanel {...defaultProps} tasks={tasks} />);
      expect(screen.getByTestId('work-pipeline-panel')).toBeInTheDocument();
    });

    it('has accessible table structure', () => {
      const taskLists = createTaskLists({
        needsPlanning: [createTask()],
      });
      const tasks = createTaskSummary({ needs_planning: 1 });

      render(<WorkPipelinePanel tasks={tasks} taskLists={taskLists} />);

      expect(screen.getByRole('table')).toBeInTheDocument();
      expect(screen.getAllByRole('columnheader').length).toBe(3);
    });
  });
});
