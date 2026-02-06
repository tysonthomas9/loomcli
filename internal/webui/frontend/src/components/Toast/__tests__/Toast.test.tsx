/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for Toast component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import '@testing-library/jest-dom';
import { Toast } from '../Toast';

describe('Toast', () => {
  const defaultProps = {
    id: 'toast-1',
    message: 'Test message',
    type: 'info' as const,
    onDismiss: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('rendering', () => {
    it('renders message text', () => {
      render(<Toast {...defaultProps} />);
      expect(screen.getByText('Test message')).toBeInTheDocument();
    });

    it('renders with role="alert"', () => {
      render(<Toast {...defaultProps} />);
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('renders with aria-live="polite" for non-error types', () => {
      render(<Toast {...defaultProps} type="info" />);
      const alert = screen.getByRole('alert');
      expect(alert).toHaveAttribute('aria-live', 'polite');
    });

    it('renders with aria-live="assertive" for error type', () => {
      render(<Toast {...defaultProps} type="error" />);
      const alert = screen.getByRole('alert');
      expect(alert).toHaveAttribute('aria-live', 'assertive');
    });

    it('renders dismiss button with accessible label', () => {
      render(<Toast {...defaultProps} />);
      expect(screen.getByLabelText('Dismiss notification')).toBeInTheDocument();
    });

    it('renders icon for toast', () => {
      render(<Toast {...defaultProps} />);
      const svgs = document.querySelectorAll('svg');
      expect(svgs.length).toBeGreaterThanOrEqual(1);
    });
  });

  describe('toast types', () => {
    it('applies info class for info type', () => {
      render(<Toast {...defaultProps} type="info" />);
      expect(screen.getByTestId('toast-info')).toBeInTheDocument();
    });

    it('applies success class for success type', () => {
      render(<Toast {...defaultProps} type="success" />);
      expect(screen.getByTestId('toast-success')).toBeInTheDocument();
    });

    it('applies error class for error type', () => {
      render(<Toast {...defaultProps} type="error" />);
      expect(screen.getByTestId('toast-error')).toBeInTheDocument();
    });

    it('applies warning class for warning type', () => {
      render(<Toast {...defaultProps} type="warning" />);
      expect(screen.getByTestId('toast-warning')).toBeInTheDocument();
    });
  });

  describe('dismiss interaction', () => {
    it('calls onDismiss with ID when dismiss button clicked', () => {
      const onDismiss = vi.fn();
      render(<Toast {...defaultProps} onDismiss={onDismiss} />);

      const dismissButton = screen.getByLabelText('Dismiss notification');
      fireEvent.click(dismissButton);

      expect(onDismiss).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledWith('toast-1');
    });

    it('calls onDismiss with correct ID for different toasts', () => {
      const onDismiss = vi.fn();
      render(<Toast {...defaultProps} id="custom-id" onDismiss={onDismiss} />);

      const dismissButton = screen.getByLabelText('Dismiss notification');
      fireEvent.click(dismissButton);

      expect(onDismiss).toHaveBeenCalledWith('custom-id');
    });
  });

  describe('className', () => {
    it('applies custom className', () => {
      render(<Toast {...defaultProps} className="custom-class" />);
      const alert = screen.getByRole('alert');
      expect(alert).toHaveClass('custom-class');
    });
  });

  describe('icons per type', () => {
    it('renders checkmark for success type', () => {
      const { container } = render(<Toast {...defaultProps} type="success" />);
      // Success icon has a checkmark path
      const path = container.querySelector('svg path[d*="M9 12l2 2 4-4"]');
      expect(path).toBeInTheDocument();
    });

    it('renders exclamation circle for error type', () => {
      const { container } = render(<Toast {...defaultProps} type="error" />);
      // Error icon has a circle and exclamation lines
      const circle = container.querySelector('svg circle');
      const lines = container.querySelectorAll('svg line');
      expect(circle).toBeInTheDocument();
      expect(lines.length).toBeGreaterThan(0);
    });

    it('renders triangle for warning type', () => {
      const { container } = render(<Toast {...defaultProps} type="warning" />);
      // Warning icon has a triangle path
      const path = container.querySelector('svg path');
      expect(path).toBeInTheDocument();
    });

    it('renders info circle for info type', () => {
      const { container } = render(<Toast {...defaultProps} type="info" />);
      // Info icon has a circle
      const circle = container.querySelector('svg circle');
      expect(circle).toBeInTheDocument();
    });
  });
});
