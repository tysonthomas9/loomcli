/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AppLayout component.
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';

import { AppLayout } from '../AppLayout';

describe('AppLayout', () => {
  describe('rendering', () => {
    it('renders children inside main element', () => {
      render(
        <AppLayout>
          <div data-testid="child-content">Child Content</div>
        </AppLayout>
      );

      const main = screen.getByRole('main');
      expect(main).toContainElement(screen.getByTestId('child-content'));
    });

    it('renders with minimum required props (just children)', () => {
      render(
        <AppLayout>
          <p>Minimal content</p>
        </AppLayout>
      );

      expect(screen.getByRole('banner')).toBeInTheDocument();
      expect(screen.getByRole('main')).toBeInTheDocument();
      expect(screen.getByText('Minimal content')).toBeInTheDocument();
    });

    it('applies custom className to root element', () => {
      const { container } = render(
        <AppLayout className="custom-class">
          <p>Content</p>
        </AppLayout>
      );

      const rootDiv = container.firstChild;
      expect(rootDiv).toHaveClass('custom-class');
    });

    it('renders empty main area when no children provided', () => {
      render(<AppLayout>{null}</AppLayout>);

      const main = screen.getByRole('main');
      expect(main).toBeInTheDocument();
    });
  });

  describe('title', () => {
    it('displays default title "Beads" when no title prop', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('heading', { name: 'Beads' })).toBeInTheDocument();
    });

    it('displays custom title when title prop provided', () => {
      render(
        <AppLayout title="Custom Title">
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('heading', { name: 'Custom Title' })).toBeInTheDocument();
    });

    it('title is an h1 element', () => {
      render(
        <AppLayout title="Test Title">
          <p>Content</p>
        </AppLayout>
      );

      const heading = screen.getByRole('heading', { name: 'Test Title' });
      expect(heading.tagName).toBe('H1');
    });
  });

  describe('navigation slot', () => {
    it('renders navigation slot content when provided', () => {
      render(
        <AppLayout navigation={<button>Nav Button</button>}>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('button', { name: 'Nav Button' })).toBeInTheDocument();
      expect(screen.getByRole('navigation')).toBeInTheDocument();
    });

    it('renders navigation inside nav element', () => {
      render(
        <AppLayout navigation={<span data-testid="nav-content">Navigation</span>}>
          <p>Content</p>
        </AppLayout>
      );

      const nav = screen.getByRole('navigation');
      expect(nav).toBeInTheDocument();
      expect(nav).toContainElement(screen.getByTestId('nav-content'));
    });

    it('does not render nav element when navigation prop is undefined', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    });
  });

  describe('actions slot', () => {
    it('renders actions slot content when provided', () => {
      render(
        <AppLayout actions={<button>Action Button</button>}>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('button', { name: 'Action Button' })).toBeInTheDocument();
    });

    it('does not render actions element when actions prop is undefined', () => {
      const { container } = render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      // Check that only brand exists in header (no actions div)
      const header = container.querySelector('header');
      const actionsDiv = header?.querySelector('[class*="actions"]');
      expect(actionsDiv).not.toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('header has role="banner"', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('banner')).toBeInTheDocument();
    });

    it('main has role="main"', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('main')).toBeInTheDocument();
    });

    it('main has id="main-content" for skip links', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      const main = screen.getByRole('main');
      expect(main).toHaveAttribute('id', 'main-content');
    });

    it('navigation has aria-label="Main navigation"', () => {
      render(
        <AppLayout navigation={<button>Nav</button>}>
          <p>Content</p>
        </AppLayout>
      );

      const nav = screen.getByRole('navigation');
      expect(nav).toHaveAttribute('aria-label', 'Main navigation');
    });

    it('has a skip link that targets main-content', () => {
      render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      const skipLink = screen.getByRole('link', { name: /skip to main content/i });
      expect(skipLink).toBeInTheDocument();
      expect(skipLink).toHaveAttribute('href', '#main-content');
    });

    it('skip link is the first focusable element', () => {
      const { container } = render(
        <AppLayout navigation={<button>Nav</button>}>
          <p>Content</p>
        </AppLayout>
      );

      // Get all focusable elements
      const focusableElements = container.querySelectorAll(
        'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])'
      );

      // Skip link should be first
      expect(focusableElements[0]).toHaveAttribute('href', '#main-content');
      expect(focusableElements[0]).toHaveTextContent(/skip to main content/i);
    });
  });

  describe('structure', () => {
    it('header is rendered before main in DOM order', () => {
      const { container } = render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      const rootDiv = container.firstChild as HTMLElement;
      // header and main are no longer direct siblings - main is inside contentWrapper
      // but header still comes before contentWrapper in the direct children
      const children = Array.from(rootDiv.children);
      const headerIndex = children.findIndex((el) => el.tagName === 'HEADER');
      const contentWrapperIndex = children.findIndex((el) => el.querySelector('main'));

      expect(headerIndex).toBeLessThan(contentWrapperIndex);
    });

    it('has correct element hierarchy (div > header + main)', () => {
      const { container } = render(
        <AppLayout>
          <p>Content</p>
        </AppLayout>
      );

      const rootDiv = container.firstChild as HTMLElement;
      expect(rootDiv.tagName).toBe('DIV');

      const header = rootDiv.querySelector('header');
      const main = rootDiv.querySelector('main');
      const contentWrapper = main?.parentElement;

      expect(header).toBeInTheDocument();
      expect(main).toBeInTheDocument();
      expect(header?.parentElement).toBe(rootDiv);
      // main is inside contentWrapper, which is inside rootDiv
      expect(contentWrapper?.parentElement).toBe(rootDiv);
    });
  });

  describe('props', () => {
    it('preserves existing classes when adding custom className', () => {
      const { container } = render(
        <AppLayout className="custom-class">
          <p>Content</p>
        </AppLayout>
      );

      const root = container.firstChild as HTMLElement;
      // Should have both the module CSS class and custom class
      expect(root.classList.length).toBeGreaterThanOrEqual(2);
      expect(root).toHaveClass('custom-class');
    });
  });

  describe('edge cases', () => {
    it('handles empty title', () => {
      render(
        <AppLayout title="">
          <p>Content</p>
        </AppLayout>
      );

      const heading = screen.getByRole('heading', { level: 1 });
      expect(heading).toBeInTheDocument();
      expect(heading.textContent).toBe('');
    });

    it('handles complex nested children', () => {
      render(
        <AppLayout>
          <div>
            <section>
              <article>
                <p data-testid="nested-content">Deeply nested</p>
              </article>
            </section>
          </div>
        </AppLayout>
      );

      expect(screen.getByTestId('nested-content')).toBeInTheDocument();
    });

    it('handles multiple children', () => {
      render(
        <AppLayout>
          <div data-testid="child-1">First</div>
          <div data-testid="child-2">Second</div>
          <div data-testid="child-3">Third</div>
        </AppLayout>
      );

      expect(screen.getByTestId('child-1')).toBeInTheDocument();
      expect(screen.getByTestId('child-2')).toBeInTheDocument();
      expect(screen.getByTestId('child-3')).toBeInTheDocument();
    });

    it('renders both navigation and actions when provided', () => {
      render(
        <AppLayout
          navigation={<button>Nav</button>}
          actions={<button>Action</button>}
        >
          <p>Content</p>
        </AppLayout>
      );

      expect(screen.getByRole('button', { name: 'Nav' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument();
    });
  });
});
