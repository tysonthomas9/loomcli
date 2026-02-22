/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EpicProgress component.
 *
 * Tests verify progress bar rendering, percentage calculation,
 * "Ready" badge display, edge cases, and ARIA attributes.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import { EpicProgress } from '../EpicProgress';

describe('EpicProgress', () => {
  describe('progress bar rendering', () => {
    it('renders progress bar with correct percentage', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={5} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toBeInTheDocument();
      expect(progressBar).toHaveAttribute('aria-valuenow', '50');
    });

    it('renders correct label text', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={3} eligibleForClose={false} />
      );

      expect(screen.getByText('3/10')).toBeInTheDocument();
    });

    it('calculates 100% when all children are closed', () => {
      render(
        <EpicProgress totalChildren={8} closedChildren={8} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '100');
      expect(screen.getByText('8/8')).toBeInTheDocument();
    });

    it('calculates 0% when no children are closed', () => {
      render(
        <EpicProgress totalChildren={5} closedChildren={0} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '0');
      expect(screen.getByText('0/5')).toBeInTheDocument();
    });

    it('rounds percentage to nearest integer', () => {
      // 1/3 = 33.33... should round to 33
      render(
        <EpicProgress totalChildren={3} closedChildren={1} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '33');
    });
  });

  describe('Ready badge', () => {
    it('shows "Ready" badge when eligible_for_close is true', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={10} eligibleForClose={true} />
      );

      expect(screen.getByText('Ready')).toBeInTheDocument();
    });

    it('hides badge when not eligible', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={5} eligibleForClose={false} />
      );

      expect(screen.queryByText('Ready')).not.toBeInTheDocument();
    });

    it('Ready badge has correct aria-label', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={10} eligibleForClose={true} />
      );

      expect(screen.getByLabelText('Ready to close')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles 0/0 edge case (no children)', () => {
      render(
        <EpicProgress totalChildren={0} closedChildren={0} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '0');
      expect(screen.getByText('0/0')).toBeInTheDocument();
    });

    it('handles single child completed', () => {
      render(
        <EpicProgress totalChildren={1} closedChildren={1} eligibleForClose={true} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '100');
      expect(screen.getByText('1/1')).toBeInTheDocument();
      expect(screen.getByText('Ready')).toBeInTheDocument();
    });

    it('handles large numbers', () => {
      render(
        <EpicProgress totalChildren={1000} closedChildren={750} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuenow', '75');
      expect(screen.getByText('750/1000')).toBeInTheDocument();
    });
  });

  describe('ARIA attributes', () => {
    it('has proper progressbar role', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={5} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toBeInTheDocument();
    });

    it('has aria-valuemin set to 0', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={5} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuemin', '0');
    });

    it('has aria-valuemax set to 100', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={5} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute('aria-valuemax', '100');
    });

    it('has descriptive aria-label', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={7} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute(
        'aria-label',
        'Epic completion: 7 of 10 done'
      );
    });

    it('has correct aria-label for 0/0 case', () => {
      render(
        <EpicProgress totalChildren={0} closedChildren={0} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      expect(progressBar).toHaveAttribute(
        'aria-label',
        'Epic completion: 0 of 0 done'
      );
    });
  });

  describe('progress fill width', () => {
    it('sets correct width style on progress fill', () => {
      render(
        <EpicProgress totalChildren={4} closedChildren={1} eligibleForClose={false} />
      );

      // The progress fill span has inline style width
      const progressBar = screen.getByRole('progressbar');
      const fill = progressBar.querySelector('span');
      expect(fill).toHaveStyle({ width: '25%' });
    });

    it('sets 0% width when no children closed', () => {
      render(
        <EpicProgress totalChildren={10} closedChildren={0} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      const fill = progressBar.querySelector('span');
      expect(fill).toHaveStyle({ width: '0%' });
    });

    it('sets 100% width when all children closed', () => {
      render(
        <EpicProgress totalChildren={5} closedChildren={5} eligibleForClose={false} />
      );

      const progressBar = screen.getByRole('progressbar');
      const fill = progressBar.querySelector('span');
      expect(fill).toHaveStyle({ width: '100%' });
    });
  });
});
