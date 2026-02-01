/**
 * @vitest-environment jsdom
 */

/**
 * DependencySection component tests.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { DependencySection, type DependencySectionProps } from '../DependencySection';
import type { IssueWithDependencyMetadata } from '@/types';

// Helper to create test dependencies
function createDependency(
  id: string,
  title: string,
  status: 'open' | 'in_progress' | 'closed' = 'open',
  dependencyType: string = 'blocks'
): IssueWithDependencyMetadata {
  return {
    id,
    title,
    status,
    dependency_type: dependencyType,
    description: '',
    issue_type: 'task',
    priority: 2,
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    created_by: 'test-user',
  };
}

// Default test props
function defaultProps(overrides?: Partial<DependencySectionProps>): DependencySectionProps {
  return {
    issueId: 'test-issue-1',
    dependencies: [],
    onAddDependency: vi.fn().mockResolvedValue(undefined),
    onRemoveDependency: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe('DependencySection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('rendering', () => {
    it('renders empty state when no dependencies', () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId('dependency-section')).toBeInTheDocument();
      expect(screen.getByText('Blocked By')).toBeInTheDocument();
      expect(screen.getByTestId('no-dependencies')).toHaveTextContent('No blocking dependencies');
    });

    it('renders dependency count in header when dependencies exist', () => {
      const deps = [
        createDependency('dep-1', 'First dep'),
        createDependency('dep-2', 'Second dep'),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByText('Blocked By (2)')).toBeInTheDocument();
    });

    it('renders list of dependencies with IDs and titles', () => {
      const deps = [
        createDependency('dep-1', 'First dependency'),
        createDependency('dep-2', 'Second dependency'),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId('dependency-list')).toBeInTheDocument();
      expect(screen.getByTestId('dependency-item-dep-1')).toBeInTheDocument();
      expect(screen.getByTestId('dependency-item-dep-2')).toBeInTheDocument();
      expect(screen.getByText('dep-1')).toBeInTheDocument();
      expect(screen.getByText('First dependency')).toBeInTheDocument();
      expect(screen.getByText('dep-2')).toBeInTheDocument();
      expect(screen.getByText('Second dependency')).toBeInTheDocument();
    });

    it('renders remove buttons for each dependency', () => {
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId('remove-dependency-dep-1')).toBeInTheDocument();
    });

    it('shows dependency type badge', () => {
      const deps = [createDependency('dep-1', 'Test dep', 'open', 'parent-child')];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByText('parent-child')).toBeInTheDocument();
    });

    it('applies closed styling to closed dependencies', () => {
      const deps = [createDependency('dep-1', 'Closed dep', 'closed')];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId('dependency-item-dep-1');
      expect(item.className).toContain('dependencyClosed');
    });

    it('shows add button when not disabled', () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId('add-dependency-button')).toBeInTheDocument();
    });

    it('hides add button when disabled', () => {
      render(<DependencySection {...defaultProps({ disabled: true })} />);

      expect(screen.queryByTestId('add-dependency-button')).not.toBeInTheDocument();
    });

    it('hides remove buttons when disabled', () => {
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps, disabled: true })} />);

      expect(screen.queryByTestId('remove-dependency-dep-1')).not.toBeInTheDocument();
    });
  });

  describe('adding dependencies', () => {
    it('shows add form when add button is clicked', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));

      expect(screen.getByTestId('add-dependency-form')).toBeInTheDocument();
      expect(screen.getByTestId('dependency-input')).toBeInTheDocument();
      expect(screen.getByTestId('confirm-add-dependency')).toBeInTheDocument();
      expect(screen.getByTestId('cancel-add-dependency')).toBeInTheDocument();
    });

    it('focuses input when entering add mode', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));

      expect(screen.getByTestId('dependency-input')).toHaveFocus();
    });

    it('hides add button when in add mode', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));

      expect(screen.queryByTestId('add-dependency-button')).not.toBeInTheDocument();
    });

    it('cancels add mode when cancel button is clicked', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.click(screen.getByTestId('cancel-add-dependency'));

      expect(screen.queryByTestId('add-dependency-form')).not.toBeInTheDocument();
      expect(screen.getByTestId('add-dependency-button')).toBeInTheDocument();
    });

    it('cancels add mode when Escape is pressed', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.keyDown(screen.getByTestId('dependency-input'), { key: 'Escape' });

      expect(screen.queryByTestId('add-dependency-form')).not.toBeInTheDocument();
    });

    it('calls onAddDependency with issue ID when form is submitted', async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'new-dep-id' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      await waitFor(() => {
        expect(onAddDependency).toHaveBeenCalledWith('new-dep-id', 'blocks');
      });
    });

    it('submits on Enter key', async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'new-dep-id' } });
      fireEvent.keyDown(screen.getByTestId('dependency-input'), { key: 'Enter' });

      await waitFor(() => {
        expect(onAddDependency).toHaveBeenCalledWith('new-dep-id', 'blocks');
      });
    });

    it('resets form after successful add', async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'new-dep-id' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      await waitFor(() => {
        expect(screen.queryByTestId('add-dependency-form')).not.toBeInTheDocument();
      });
    });

    it('shows error when pressing Enter with empty input', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      // Press Enter on empty input - this will call handleAdd which validates
      fireEvent.keyDown(screen.getByTestId('dependency-input'), { key: 'Enter' });

      expect(screen.getByTestId('dependency-error')).toHaveTextContent('Please enter an issue ID');
    });

    it('shows error when adding self as dependency', () => {
      render(<DependencySection {...defaultProps({ issueId: 'issue-1' })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'issue-1' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      expect(screen.getByTestId('dependency-error')).toHaveTextContent('Cannot add self as dependency');
    });

    it('shows error when adding duplicate dependency', () => {
      const deps = [createDependency('existing-dep', 'Existing')];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'existing-dep' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      expect(screen.getByTestId('dependency-error')).toHaveTextContent('Already a dependency');
    });

    it('shows error from API failure', async () => {
      const onAddDependency = vi.fn().mockRejectedValue(new Error('Issue not found'));
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'nonexistent' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      await waitFor(() => {
        expect(screen.getByTestId('dependency-error')).toHaveTextContent('Issue not found');
      });
    });

    it('shows loading state while adding', async () => {
      let resolveAdd: () => void;
      const onAddDependency = vi.fn().mockImplementation(() => new Promise(resolve => {
        resolveAdd = resolve;
      }));
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      fireEvent.change(screen.getByTestId('dependency-input'), { target: { value: 'new-dep' } });
      fireEvent.click(screen.getByTestId('confirm-add-dependency'));

      expect(screen.getByTestId('confirm-add-dependency')).toHaveTextContent('Adding...');
      expect(screen.getByTestId('confirm-add-dependency')).toBeDisabled();

      // Clean up the pending promise
      await act(async () => {
        resolveAdd!();
      });
    });

    it('disables confirm button when input is empty', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));

      expect(screen.getByTestId('confirm-add-dependency')).toBeDisabled();
    });
  });

  describe('removing dependencies', () => {
    it('calls onRemoveDependency when remove button is clicked', async () => {
      const onRemoveDependency = vi.fn().mockResolvedValue(undefined);
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps, onRemoveDependency })} />);

      fireEvent.click(screen.getByTestId('remove-dependency-dep-1'));

      await waitFor(() => {
        expect(onRemoveDependency).toHaveBeenCalledWith('dep-1');
      });
    });

    it('shows error from removal failure', async () => {
      const onRemoveDependency = vi.fn().mockRejectedValue(new Error('Failed to remove'));
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps, onRemoveDependency })} />);

      fireEvent.click(screen.getByTestId('remove-dependency-dep-1'));

      await waitFor(() => {
        expect(screen.getByTestId('dependency-error')).toHaveTextContent('Failed to remove');
      });
    });

    it('shows loading state while removing', async () => {
      let resolveRemove: () => void;
      const onRemoveDependency = vi.fn().mockImplementation(() => new Promise(resolve => {
        resolveRemove = resolve;
      }));
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps, onRemoveDependency })} />);

      fireEvent.click(screen.getByTestId('remove-dependency-dep-1'));

      const item = screen.getByTestId('dependency-item-dep-1');
      expect(item.className).toContain('removing');

      // Clean up the pending promise
      await act(async () => {
        resolveRemove!();
      });
    });

    it('disables all buttons during removal', async () => {
      let resolveRemove: () => void;
      const onRemoveDependency = vi.fn().mockImplementation(() => new Promise(resolve => {
        resolveRemove = resolve;
      }));
      const deps = [
        createDependency('dep-1', 'First'),
        createDependency('dep-2', 'Second'),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps, onRemoveDependency })} />);

      fireEvent.click(screen.getByTestId('remove-dependency-dep-1'));

      expect(screen.getByTestId('remove-dependency-dep-2')).toBeDisabled();
      expect(screen.getByTestId('add-dependency-button')).toBeDisabled();

      // Clean up the pending promise
      await act(async () => {
        resolveRemove!();
      });
    });
  });

  describe('accessibility', () => {
    it('has aria-label on remove buttons', () => {
      const deps = [createDependency('dep-1', 'Test dep')];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId('remove-dependency-dep-1')).toHaveAttribute(
        'aria-label',
        'Remove dependency dep-1'
      );
    });

    it('has aria-label on add button', () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId('add-dependency-button')).toHaveAttribute(
        'aria-label',
        'Add dependency'
      );
    });

    it('has aria-label on input', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));

      expect(screen.getByTestId('dependency-input')).toHaveAttribute(
        'aria-label',
        'Issue ID'
      );
    });

    it('error has role="alert"', () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId('add-dependency-button'));
      // Press Enter on empty input to trigger validation error
      fireEvent.keyDown(screen.getByTestId('dependency-input'), { key: 'Enter' });

      expect(screen.getByRole('alert')).toBeInTheDocument();
    });
  });

  describe('custom className', () => {
    it('applies custom className', () => {
      render(<DependencySection {...defaultProps({ className: 'custom-class' })} />);

      expect(screen.getByTestId('dependency-section').className).toContain('custom-class');
    });
  });
});
