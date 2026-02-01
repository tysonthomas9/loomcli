/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ToastContainer component.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import '@testing-library/jest-dom'
import { ToastContainer } from '../ToastContainer'
import type { Toast } from '@/hooks/useToast'

describe('ToastContainer', () => {
  const createToast = (overrides: Partial<Toast> = {}): Toast => ({
    id: `toast-${Math.random().toString(36).slice(2)}`,
    message: 'Test message',
    type: 'info',
    duration: 5000,
    ...overrides,
  })

  const defaultProps = {
    toasts: [],
    onDismiss: vi.fn(),
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders container element', () => {
      render(<ToastContainer {...defaultProps} />)
      expect(screen.getByTestId('toast-container')).toBeInTheDocument()
    })

    it('renders with aria-label', () => {
      render(<ToastContainer {...defaultProps} />)
      expect(screen.getByLabelText('Notifications')).toBeInTheDocument()
    })

    it('renders nothing when toasts is empty', () => {
      render(<ToastContainer {...defaultProps} />)
      const container = screen.getByTestId('toast-container')
      expect(container.children).toHaveLength(0)
    })
  })

  describe('toast rendering', () => {
    it('renders all toasts in array', () => {
      const toasts = [
        createToast({ id: 'toast-1', message: 'First' }),
        createToast({ id: 'toast-2', message: 'Second' }),
        createToast({ id: 'toast-3', message: 'Third' }),
      ]

      render(<ToastContainer {...defaultProps} toasts={toasts} />)

      expect(screen.getByText('First')).toBeInTheDocument()
      expect(screen.getByText('Second')).toBeInTheDocument()
      expect(screen.getByText('Third')).toBeInTheDocument()
    })

    it('renders toasts in order', () => {
      const toasts = [
        createToast({ id: 'toast-1', message: 'First' }),
        createToast({ id: 'toast-2', message: 'Second' }),
      ]

      render(<ToastContainer {...defaultProps} toasts={toasts} />)

      const alerts = screen.getAllByRole('alert')
      expect(alerts).toHaveLength(2)
      expect(alerts[0]).toHaveTextContent('First')
      expect(alerts[1]).toHaveTextContent('Second')
    })

    it('renders toasts with correct types', () => {
      const toasts = [
        createToast({ id: 'toast-1', type: 'success', message: 'Success' }),
        createToast({ id: 'toast-2', type: 'error', message: 'Error' }),
      ]

      render(<ToastContainer {...defaultProps} toasts={toasts} />)

      expect(screen.getByTestId('toast-success')).toBeInTheDocument()
      expect(screen.getByTestId('toast-error')).toBeInTheDocument()
    })
  })

  describe('dismiss handling', () => {
    it('passes onDismiss to each Toast', () => {
      const onDismiss = vi.fn()
      const toasts = [createToast({ id: 'toast-1' })]

      render(<ToastContainer toasts={toasts} onDismiss={onDismiss} />)

      const dismissButton = screen.getByLabelText('Dismiss notification')
      fireEvent.click(dismissButton)

      expect(onDismiss).toHaveBeenCalledWith('toast-1')
    })

    it('calls onDismiss with correct ID for specific toast', () => {
      const onDismiss = vi.fn()
      const toasts = [
        createToast({ id: 'toast-1', message: 'First' }),
        createToast({ id: 'toast-2', message: 'Second' }),
      ]

      render(<ToastContainer toasts={toasts} onDismiss={onDismiss} />)

      const dismissButtons = screen.getAllByLabelText('Dismiss notification')
      fireEvent.click(dismissButtons[1])

      expect(onDismiss).toHaveBeenCalledWith('toast-2')
    })
  })

  describe('position variants', () => {
    it('defaults to bottom-right position', () => {
      render(<ToastContainer {...defaultProps} />)
      const container = screen.getByTestId('toast-container')
      expect(container.className).toContain('bottomright')
    })

    it('applies bottom-left position class', () => {
      render(<ToastContainer {...defaultProps} position="bottom-left" />)
      const container = screen.getByTestId('toast-container')
      expect(container.className).toContain('bottomleft')
    })

    it('applies top-right position class', () => {
      render(<ToastContainer {...defaultProps} position="top-right" />)
      const container = screen.getByTestId('toast-container')
      expect(container.className).toContain('topright')
    })

    it('applies top-left position class', () => {
      render(<ToastContainer {...defaultProps} position="top-left" />)
      const container = screen.getByTestId('toast-container')
      expect(container.className).toContain('topleft')
    })
  })

  describe('className', () => {
    it('applies custom className', () => {
      render(<ToastContainer {...defaultProps} className="custom-container" />)
      const container = screen.getByTestId('toast-container')
      expect(container).toHaveClass('custom-container')
    })
  })
})
