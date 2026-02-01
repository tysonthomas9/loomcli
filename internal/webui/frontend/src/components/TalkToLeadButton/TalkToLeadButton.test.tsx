/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TalkToLeadButton component.
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

import { TalkToLeadButton } from './TalkToLeadButton';

describe('TalkToLeadButton', () => {
  describe('rendering', () => {
    it('renders a button element', () => {
      const { container } = render(<TalkToLeadButton />);

      expect(container.querySelector('button')).toBeInTheDocument();
    });

    it('renders with type="button" attribute', () => {
      const { container } = render(<TalkToLeadButton />);

      const button = container.querySelector('button');
      expect(button).toHaveAttribute('type', 'button');
    });

    it('renders the "Talk to Lead" text', () => {
      render(<TalkToLeadButton />);

      expect(screen.getByText('Talk to Lead')).toBeInTheDocument();
    });

    it('renders an SVG element', () => {
      const { container } = render(<TalkToLeadButton />);

      expect(container.querySelector('svg')).toBeInTheDocument();
    });

    it('renders an SVG with correct dimensions', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('width', '20');
      expect(svg).toHaveAttribute('height', '20');
    });

    it('renders an SVG with correct viewBox', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('viewBox', '0 0 24 24');
    });

    it('renders a path element inside SVG', () => {
      const { container } = render(<TalkToLeadButton />);

      const path = container.querySelector('svg path');
      expect(path).toBeInTheDocument();
    });

    it('renders SVG with fill="none"', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('fill', 'none');
    });

    it('renders SVG with stroke="currentColor"', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('stroke', 'currentColor');
    });

    it('renders SVG with stroke-width="2"', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('stroke-width', '2');
    });

    it('renders SVG with stroke-linecap="round"', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('stroke-linecap', 'round');
    });

    it('renders SVG with stroke-linejoin="round"', () => {
      const { container } = render(<TalkToLeadButton />);

      const svg = container.querySelector('svg');
      expect(svg).toHaveAttribute('stroke-linejoin', 'round');
    });
  });

  describe('data-testid attribute', () => {
    it('has data-testid="talk-to-lead-button"', () => {
      render(<TalkToLeadButton />);

      expect(screen.getByTestId('talk-to-lead-button')).toBeInTheDocument();
    });

    it('can be selected by data-testid', () => {
      const { container } = render(<TalkToLeadButton />);

      const button = container.querySelector('[data-testid="talk-to-lead-button"]');
      expect(button).toBeInTheDocument();
      expect(button?.tagName).toBe('BUTTON');
    });
  });

  describe('chat icon', () => {
    it('renders a chat bubble icon (SVG path with message path)', () => {
      const { container } = render(<TalkToLeadButton />);

      const path = container.querySelector('svg path');
      expect(path).toHaveAttribute('d');

      // The icon should be a chat/message bubble
      const pathData = path?.getAttribute('d');
      // Should contain message bubble path with cursor dots
      expect(pathData).toBeTruthy();
    });

    it('SVG path has the correct message bubble icon path', () => {
      const { container } = render(<TalkToLeadButton />);

      const path = container.querySelector('svg path');
      const expectedPath =
        'M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z';

      expect(path).toHaveAttribute('d', expectedPath);
    });
  });

  describe('styling', () => {
    it('applies the fab class from CSS module', () => {
      const { container } = render(<TalkToLeadButton />);

      const button = container.querySelector('button');
      // The className should contain the fab class
      expect(button?.className).toMatch(/fab/);
    });
  });

  describe('button behavior', () => {
    it('is a properly functioning button element', () => {
      render(<TalkToLeadButton />);

      const button = screen.getByTestId('talk-to-lead-button');
      expect(button).toBeInstanceOf(HTMLButtonElement);
    });

    it('renders button with both SVG icon and text content', () => {
      const { container } = render(<TalkToLeadButton />);

      const button = container.querySelector('button');

      // Verify button contains SVG
      const svg = button?.querySelector('svg');
      expect(svg).toBeInTheDocument();

      // Verify button contains text
      const buttonText = button?.textContent?.trim();
      expect(buttonText).toContain('Talk to Lead');
    });
  });
});
