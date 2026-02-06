/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AssigneePrompt component.
 * Tests modal behavior for entering assignee name on drag to In Progress.
 */

import { render, screen, fireEvent, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import '@testing-library/jest-dom';
import { AssigneePrompt } from '../AssigneePrompt';

describe('AssigneePrompt', () => {
  const defaultProps = {
    isOpen: true,
    onConfirm: vi.fn(),
    onSkip: vi.fn(),
    recentNames: [] as string[],
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe('Rendering', () => {
    it('renders when isOpen is true', () => {
      render(<AssigneePrompt {...defaultProps} isOpen={true} />);

      expect(screen.getByTestId('assignee-prompt-overlay')).toBeInTheDocument();
      expect(screen.getByTestId('assignee-prompt-modal')).toBeInTheDocument();
      expect(screen.getByText('Who is working on this?')).toBeInTheDocument();
    });

    it('sets aria-hidden="false" on overlay when open', () => {
      render(<AssigneePrompt {...defaultProps} isOpen={true} />);

      const overlay = screen.getByTestId('assignee-prompt-overlay');
      expect(overlay).toHaveAttribute('aria-hidden', 'false');
    });

    it('sets aria-hidden="true" on overlay when closed', () => {
      render(<AssigneePrompt {...defaultProps} isOpen={false} />);

      const overlay = screen.getByTestId('assignee-prompt-overlay');
      expect(overlay).toHaveAttribute('aria-hidden', 'true');
    });

    it('renders title and subtitle', () => {
      render(<AssigneePrompt {...defaultProps} />);

      expect(screen.getByText('Who is working on this?')).toBeInTheDocument();
      expect(screen.getByText('Enter your name to claim this task')).toBeInTheDocument();
    });

    it('renders input field with placeholder', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const input = screen.getByTestId('assignee-name-input');
      expect(input).toBeInTheDocument();
      expect(input).toHaveAttribute('placeholder', 'e.g., Tyson');
    });

    it('renders Assign and Skip buttons', () => {
      render(<AssigneePrompt {...defaultProps} />);

      expect(screen.getByTestId('assignee-confirm-button')).toHaveTextContent('Assign');
      expect(screen.getByTestId('assignee-skip-button')).toHaveTextContent('Skip');
    });

    it('renders "Your name" label when no recent names', () => {
      render(<AssigneePrompt {...defaultProps} recentNames={[]} />);

      expect(screen.getByText('Your name')).toBeInTheDocument();
    });

    it('renders "Or enter a new name" label when recent names exist', () => {
      render(<AssigneePrompt {...defaultProps} recentNames={['Alice', 'Bob']} />);

      expect(screen.getByText('Or enter a new name')).toBeInTheDocument();
    });
  });

  describe('Recent names', () => {
    it('shows recent names dropdown when recentNames is not empty', () => {
      render(<AssigneePrompt {...defaultProps} recentNames={['Alice', 'Bob', 'Charlie']} />);

      expect(screen.getByText('Recent')).toBeInTheDocument();
      expect(screen.getByTestId('recent-name-Alice')).toHaveTextContent('Alice');
      expect(screen.getByTestId('recent-name-Bob')).toHaveTextContent('Bob');
      expect(screen.getByTestId('recent-name-Charlie')).toHaveTextContent('Charlie');
    });

    it('does not show recent section when recentNames is empty', () => {
      render(<AssigneePrompt {...defaultProps} recentNames={[]} />);

      expect(screen.queryByText('Recent')).not.toBeInTheDocument();
    });

    it('clicking recent name calls onConfirm with [H] prefix', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} recentNames={['Alice']} />);

      fireEvent.click(screen.getByTestId('recent-name-Alice'));

      expect(onConfirm).toHaveBeenCalledWith('[H] Alice');
    });

    it('clicking recent name calls onConfirm once', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} recentNames={['Alice']} />);

      fireEvent.click(screen.getByTestId('recent-name-Alice'));

      expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it('handles multiple recent names correctly', () => {
      const onConfirm = vi.fn();
      render(
        <AssigneePrompt
          {...defaultProps}
          onConfirm={onConfirm}
          recentNames={['Alice', 'Bob', 'Charlie']}
        />
      );

      fireEvent.click(screen.getByTestId('recent-name-Bob'));

      expect(onConfirm).toHaveBeenCalledWith('[H] Bob');
    });
  });

  describe('Form submission', () => {
    it('calls onConfirm with name including [H] prefix when Assign clicked', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: 'Tyson' } });
      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).toHaveBeenCalledWith('[H] Tyson');
    });

    it('calls onConfirm with trimmed name', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: '  Tyson  ' } });
      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).toHaveBeenCalledWith('[H] Tyson');
    });

    it('calls onConfirm on form submit (Enter key)', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: 'Tyson' } });
      fireEvent.submit(input.closest('form')!);

      expect(onConfirm).toHaveBeenCalledWith('[H] Tyson');
    });

    it('does not call onConfirm when input is empty', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).not.toHaveBeenCalled();
    });

    it('does not call onConfirm when input is whitespace only', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: '   ' } });
      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).not.toHaveBeenCalled();
    });
  });

  describe('Skip behavior', () => {
    it('calls onSkip when Skip button clicked', () => {
      const onSkip = vi.fn();
      render(<AssigneePrompt {...defaultProps} onSkip={onSkip} />);

      fireEvent.click(screen.getByTestId('assignee-skip-button'));

      expect(onSkip).toHaveBeenCalledTimes(1);
    });

    it('calls onSkip when Escape key pressed', () => {
      const onSkip = vi.fn();
      render(<AssigneePrompt {...defaultProps} onSkip={onSkip} />);

      fireEvent.keyDown(document, { key: 'Escape' });

      expect(onSkip).toHaveBeenCalledTimes(1);
    });

    it('does not call onSkip on Escape when closed', () => {
      const onSkip = vi.fn();
      render(<AssigneePrompt {...defaultProps} isOpen={false} onSkip={onSkip} />);

      fireEvent.keyDown(document, { key: 'Escape' });

      expect(onSkip).not.toHaveBeenCalled();
    });
  });

  describe('Input validation', () => {
    it('Assign button is disabled when input is empty', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const button = screen.getByTestId('assignee-confirm-button');
      expect(button).toBeDisabled();
    });

    it('Assign button is enabled when input has text', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: 'Tyson' } });

      const button = screen.getByTestId('assignee-confirm-button');
      expect(button).not.toBeDisabled();
    });

    it('Assign button is disabled when input is whitespace only', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: '   ' } });

      const button = screen.getByTestId('assignee-confirm-button');
      expect(button).toBeDisabled();
    });

    it('Assign button becomes disabled after clearing input', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const input = screen.getByTestId('assignee-name-input');
      const button = screen.getByTestId('assignee-confirm-button');

      fireEvent.change(input, { target: { value: 'Tyson' } });
      expect(button).not.toBeDisabled();

      fireEvent.change(input, { target: { value: '' } });
      expect(button).toBeDisabled();
    });
  });

  describe('Focus behavior', () => {
    it('focuses input when modal opens', () => {
      render(<AssigneePrompt {...defaultProps} isOpen={true} />);

      // Input focus happens after 100ms timeout
      act(() => {
        vi.advanceTimersByTime(100);
      });

      expect(document.activeElement).toBe(screen.getByTestId('assignee-name-input'));
    });

    it('resets input value when modal opens', () => {
      const { rerender } = render(<AssigneePrompt {...defaultProps} isOpen={true} />);

      const input = screen.getByTestId('assignee-name-input') as HTMLInputElement;
      fireEvent.change(input, { target: { value: 'Tyson' } });
      expect(input.value).toBe('Tyson');

      // Close and reopen
      rerender(<AssigneePrompt {...defaultProps} isOpen={false} />);
      rerender(<AssigneePrompt {...defaultProps} isOpen={true} />);

      // Input should be reset
      expect(input.value).toBe('');
    });
  });

  describe('Accessibility', () => {
    it('has role="dialog" on modal', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const modal = screen.getByTestId('assignee-prompt-modal');
      expect(modal).toHaveAttribute('role', 'dialog');
    });

    it('has aria-modal="true" on modal', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const modal = screen.getByTestId('assignee-prompt-modal');
      expect(modal).toHaveAttribute('aria-modal', 'true');
    });

    it('has aria-labelledby pointing to title', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const modal = screen.getByTestId('assignee-prompt-modal');
      expect(modal).toHaveAttribute('aria-labelledby', 'assignee-prompt-title');

      const title = document.getElementById('assignee-prompt-title');
      expect(title).toHaveTextContent('Who is working on this?');
    });

    it('input has associated label', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const input = screen.getByTestId('assignee-name-input');
      expect(input).toHaveAttribute('id', 'assignee-name');

      const label = document.querySelector('label[for="assignee-name"]');
      expect(label).toBeInTheDocument();
    });

    it('Skip button has type="button"', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const skipButton = screen.getByTestId('assignee-skip-button');
      expect(skipButton).toHaveAttribute('type', 'button');
    });

    it('Assign button has type="submit"', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const assignButton = screen.getByTestId('assignee-confirm-button');
      expect(assignButton).toHaveAttribute('type', 'submit');
    });

    it('recent name buttons have type="button"', () => {
      render(<AssigneePrompt {...defaultProps} recentNames={['Alice']} />);

      const recentButton = screen.getByTestId('recent-name-Alice');
      expect(recentButton).toHaveAttribute('type', 'button');
    });
  });

  describe('Modal click behavior', () => {
    it('clicking inside modal does not trigger outside actions', () => {
      const onSkip = vi.fn();
      render(<AssigneePrompt {...defaultProps} onSkip={onSkip} />);

      const modal = screen.getByTestId('assignee-prompt-modal');
      fireEvent.click(modal);

      // onSkip should not be called from clicking inside modal
      expect(onSkip).not.toHaveBeenCalled();
    });
  });

  describe('Keyboard navigation', () => {
    it('prevents default on Escape key', () => {
      render(<AssigneePrompt {...defaultProps} />);

      const event = new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      });
      const preventDefaultSpy = vi.spyOn(event, 'preventDefault');

      document.dispatchEvent(event);

      expect(preventDefaultSpy).toHaveBeenCalled();
    });

    it('other keys do not trigger onSkip', () => {
      const onSkip = vi.fn();
      render(<AssigneePrompt {...defaultProps} onSkip={onSkip} />);

      fireEvent.keyDown(document, { key: 'Enter' });
      fireEvent.keyDown(document, { key: 'Tab' });
      fireEvent.keyDown(document, { key: 'Space' });

      expect(onSkip).not.toHaveBeenCalled();
    });
  });

  describe('Edge cases', () => {
    it('handles rapid button clicks', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} recentNames={['Alice']} />);

      const recentButton = screen.getByTestId('recent-name-Alice');
      fireEvent.click(recentButton);
      fireEvent.click(recentButton);
      fireEvent.click(recentButton);

      expect(onConfirm).toHaveBeenCalledTimes(3);
    });

    it('handles input with special characters', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: "John O'Connor-Smith" } });
      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).toHaveBeenCalledWith("[H] John O'Connor-Smith");
    });

    it('handles unicode characters in name', () => {
      const onConfirm = vi.fn();
      render(<AssigneePrompt {...defaultProps} onConfirm={onConfirm} />);

      const input = screen.getByTestId('assignee-name-input');
      fireEvent.change(input, { target: { value: 'Jose Garcia' } });
      fireEvent.click(screen.getByTestId('assignee-confirm-button'));

      expect(onConfirm).toHaveBeenCalledWith('[H] Jose Garcia');
    });

    it('cleans up event listeners on unmount', () => {
      const onSkip = vi.fn();
      const { unmount } = render(<AssigneePrompt {...defaultProps} onSkip={onSkip} />);

      unmount();

      fireEvent.keyDown(document, { key: 'Escape' });

      expect(onSkip).not.toHaveBeenCalled();
    });

    it('cleans up timeout on unmount', () => {
      const { unmount } = render(<AssigneePrompt {...defaultProps} isOpen={true} />);

      // Unmount before the 100ms focus timeout fires
      unmount();

      // This should not throw
      act(() => {
        vi.advanceTimersByTime(100);
      });
    });
  });

  describe('Props changes', () => {
    it('handles isOpen changing from false to true', () => {
      const { rerender } = render(<AssigneePrompt {...defaultProps} isOpen={false} />);

      expect(screen.getByTestId('assignee-prompt-overlay')).toHaveAttribute('aria-hidden', 'true');

      rerender(<AssigneePrompt {...defaultProps} isOpen={true} />);

      expect(screen.getByTestId('assignee-prompt-overlay')).toHaveAttribute('aria-hidden', 'false');
    });

    it('handles recentNames prop changes', () => {
      const { rerender } = render(<AssigneePrompt {...defaultProps} recentNames={['Alice']} />);

      expect(screen.getByTestId('recent-name-Alice')).toBeInTheDocument();

      rerender(<AssigneePrompt {...defaultProps} recentNames={['Alice', 'Bob']} />);

      expect(screen.getByTestId('recent-name-Alice')).toBeInTheDocument();
      expect(screen.getByTestId('recent-name-Bob')).toBeInTheDocument();
    });

    it('handles callback prop changes', () => {
      const onConfirm1 = vi.fn();
      const onConfirm2 = vi.fn();

      const { rerender } = render(
        <AssigneePrompt {...defaultProps} onConfirm={onConfirm1} recentNames={['Alice']} />
      );

      rerender(<AssigneePrompt {...defaultProps} onConfirm={onConfirm2} recentNames={['Alice']} />);

      fireEvent.click(screen.getByTestId('recent-name-Alice'));

      expect(onConfirm1).not.toHaveBeenCalled();
      expect(onConfirm2).toHaveBeenCalledWith('[H] Alice');
    });
  });
});
