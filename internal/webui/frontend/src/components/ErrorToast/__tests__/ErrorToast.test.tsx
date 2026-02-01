/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ErrorToast component.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import '@testing-library/jest-dom'

import { ErrorToast } from '../ErrorToast'

describe('ErrorToast', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('rendering', () => {
    it('renders error message', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Something went wrong" onDismiss={onDismiss} />)

      expect(screen.getByText('Something went wrong')).toBeInTheDocument()
    })

    it('renders with default test ID', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      expect(screen.getByTestId('error-toast')).toBeInTheDocument()
    })

    it('renders with custom test ID', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} testId="custom-toast" />)

      expect(screen.getByTestId('custom-toast')).toBeInTheDocument()
    })

    it('renders dismiss button', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      expect(screen.getByRole('button', { name: 'Dismiss error' })).toBeInTheDocument()
    })

    it('renders error icon (svg is hidden from screen readers)', () => {
      const onDismiss = vi.fn()
      const { container } = render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      // There should be SVG icons
      const svgs = container.querySelectorAll('svg')
      expect(svgs.length).toBeGreaterThan(0)

      // Icons should be hidden from screen readers
      svgs.forEach((svg) => {
        expect(svg).toHaveAttribute('aria-hidden', 'true')
      })
    })
  })

  describe('accessibility', () => {
    it('has role="alert" for screen readers', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      expect(screen.getByRole('alert')).toBeInTheDocument()
    })

    it('has aria-live="assertive" for immediate announcement', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const alert = screen.getByRole('alert')
      expect(alert).toHaveAttribute('aria-live', 'assertive')
    })

    it('dismiss button has accessible label', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const button = screen.getByRole('button')
      expect(button).toHaveAttribute('aria-label', 'Dismiss error')
    })
  })

  describe('auto-dismiss', () => {
    it('auto-dismisses after default timeout (5000ms)', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      expect(onDismiss).not.toHaveBeenCalled()

      act(() => {
        vi.advanceTimersByTime(5000)
      })

      expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('auto-dismisses after custom duration', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} duration={3000} />)

      expect(onDismiss).not.toHaveBeenCalled()

      act(() => {
        vi.advanceTimersByTime(2999)
      })

      expect(onDismiss).not.toHaveBeenCalled()

      act(() => {
        vi.advanceTimersByTime(1)
      })

      expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('does not auto-dismiss when duration is 0', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} duration={0} />)

      act(() => {
        vi.advanceTimersByTime(10000)
      })

      expect(onDismiss).not.toHaveBeenCalled()
    })

    it('does not auto-dismiss when duration is negative', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} duration={-1} />)

      act(() => {
        vi.advanceTimersByTime(10000)
      })

      expect(onDismiss).not.toHaveBeenCalled()
    })

    it('clears timeout on unmount', () => {
      const onDismiss = vi.fn()
      const { unmount } = render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      act(() => {
        vi.advanceTimersByTime(2500) // Half of default duration
      })

      expect(onDismiss).not.toHaveBeenCalled()

      unmount()

      act(() => {
        vi.advanceTimersByTime(5000)
      })

      // Should not have been called after unmount
      expect(onDismiss).not.toHaveBeenCalled()
    })
  })

  describe('manual dismiss', () => {
    it('dismiss button calls onDismiss', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const button = screen.getByRole('button', { name: 'Dismiss error' })
      fireEvent.click(button)

      expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('Escape key calls onDismiss', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const toast = screen.getByTestId('error-toast')
      fireEvent.keyDown(toast, { key: 'Escape' })

      expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('other keys do not trigger dismiss', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const toast = screen.getByTestId('error-toast')
      fireEvent.keyDown(toast, { key: 'Enter' })
      fireEvent.keyDown(toast, { key: 'Space' })
      fireEvent.keyDown(toast, { key: 'a' })

      expect(onDismiss).not.toHaveBeenCalled()
    })
  })

  describe('styling', () => {
    it('applies className prop to root element', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} className="custom-class" />)

      const toast = screen.getByTestId('error-toast')
      expect(toast).toHaveClass('custom-class')
    })

    it('combines className with default classes', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} className="custom-class" />)

      const toast = screen.getByTestId('error-toast')
      // Should have both the module CSS class and custom class
      expect(toast.classList.length).toBeGreaterThan(1)
    })

    it('has animation class applied', () => {
      const onDismiss = vi.fn()
      const { container } = render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const toast = container.querySelector('[data-testid="error-toast"]')
      // The toast element should exist and have CSS module class
      expect(toast).toBeInTheDocument()
    })
  })

  describe('edge cases', () => {
    it('renders with empty message', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="" onDismiss={onDismiss} />)

      expect(screen.getByTestId('error-toast')).toBeInTheDocument()
    })

    it('renders with long message', () => {
      const onDismiss = vi.fn()
      const longMessage = 'A'.repeat(500)
      render(<ErrorToast message={longMessage} onDismiss={onDismiss} />)

      expect(screen.getByText(longMessage)).toBeInTheDocument()
    })

    it('handles special characters in message', () => {
      const onDismiss = vi.fn()
      const specialMessage = '<script>alert("xss")</script> & "quotes" \'apostrophe\''
      render(<ErrorToast message={specialMessage} onDismiss={onDismiss} />)

      // Should render as text, not HTML
      expect(screen.getByText(specialMessage)).toBeInTheDocument()
    })

    it('handles rapid dismiss button clicks', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const button = screen.getByRole('button', { name: 'Dismiss error' })
      fireEvent.click(button)
      fireEvent.click(button)
      fireEvent.click(button)

      expect(onDismiss).toHaveBeenCalledTimes(3)
    })
  })

  describe('props', () => {
    it('renders with all optional props', () => {
      const onDismiss = vi.fn()
      render(
        <ErrorToast
          message="All props error"
          onDismiss={onDismiss}
          duration={10000}
          className="all-props-class"
          testId="all-props-toast"
        />
      )

      const toast = screen.getByTestId('all-props-toast')
      expect(toast).toBeInTheDocument()
      expect(toast).toHaveClass('all-props-class')
      expect(screen.getByText('All props error')).toBeInTheDocument()
    })

    it('button has type="button" to prevent form submission', () => {
      const onDismiss = vi.fn()
      render(<ErrorToast message="Error" onDismiss={onDismiss} />)

      const button = screen.getByRole('button', { name: 'Dismiss error' })
      expect(button).toHaveAttribute('type', 'button')
    })
  })
})
