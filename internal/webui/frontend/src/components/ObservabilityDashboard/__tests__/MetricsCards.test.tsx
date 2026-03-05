/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MetricsCards component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import '@testing-library/jest-dom';

import { MetricsCards } from '../MetricsCards';

const defaultProps = {
  tasksPerHour: 12,
  avgDurationSec: 95,
  linesPerHour: 340,
  errorRatePct: 3.5,
};

describe('MetricsCards', () => {
  describe('rendering all 4 values', () => {
    it('renders tasks per hour', () => {
      render(<MetricsCards {...defaultProps} />);

      expect(screen.getByText('12')).toBeInTheDocument();
      expect(screen.getByText('Tasks / Hour')).toBeInTheDocument();
    });

    it('renders average duration formatted as minutes and seconds', () => {
      render(<MetricsCards {...defaultProps} />);

      // 95s = 1m 35s
      expect(screen.getByText('1m 35s')).toBeInTheDocument();
      expect(screen.getByText('Avg Duration')).toBeInTheDocument();
    });

    it('renders lines per hour', () => {
      render(<MetricsCards {...defaultProps} />);

      expect(screen.getByText('340')).toBeInTheDocument();
      expect(screen.getByText('Lines / Hour')).toBeInTheDocument();
    });

    it('renders error rate with one decimal', () => {
      render(<MetricsCards {...defaultProps} />);

      expect(screen.getByText('3.5%')).toBeInTheDocument();
      expect(screen.getByText('Error Rate')).toBeInTheDocument();
    });

    it('renders all 4 cards at once', () => {
      render(<MetricsCards {...defaultProps} />);

      expect(screen.getByText('Tasks / Hour')).toBeInTheDocument();
      expect(screen.getByText('Avg Duration')).toBeInTheDocument();
      expect(screen.getByText('Lines / Hour')).toBeInTheDocument();
      expect(screen.getByText('Error Rate')).toBeInTheDocument();
    });
  });

  describe('duration formatting', () => {
    it('formats seconds under 60 as just seconds', () => {
      render(<MetricsCards {...defaultProps} avgDurationSec={45} />);

      expect(screen.getByText('45s')).toBeInTheDocument();
    });

    it('formats exact minutes without seconds', () => {
      render(<MetricsCards {...defaultProps} avgDurationSec={120} />);

      expect(screen.getByText('2m')).toBeInTheDocument();
    });

    it('formats minutes and seconds', () => {
      render(<MetricsCards {...defaultProps} avgDurationSec={150} />);

      expect(screen.getByText('2m 30s')).toBeInTheDocument();
    });

    it('formats zero seconds', () => {
      render(<MetricsCards {...defaultProps} avgDurationSec={0} />);

      expect(screen.getByText('0s')).toBeInTheDocument();
    });
  });

  describe('error rate styling', () => {
    it('does not apply error class when error rate is 10% or below', () => {
      const { container } = render(<MetricsCards {...defaultProps} errorRatePct={10.0} />);

      const errorValue = screen.getByText('10.0%');
      // Should not have the error class
      expect(errorValue.className).not.toMatch(/error/);
    });

    it('applies error class when error rate is above 10%', () => {
      const { container } = render(<MetricsCards {...defaultProps} errorRatePct={10.1} />);

      const errorValue = screen.getByText('10.1%');
      // Should have the error class
      expect(errorValue.className).toMatch(/error/);
    });

    it('applies error class for high error rates', () => {
      render(<MetricsCards {...defaultProps} errorRatePct={25.0} />);

      const errorValue = screen.getByText('25.0%');
      expect(errorValue.className).toMatch(/error/);
    });

    it('does not apply error class when error rate is 0', () => {
      render(<MetricsCards {...defaultProps} errorRatePct={0} />);

      const errorValue = screen.getByText('0.0%');
      expect(errorValue.className).not.toMatch(/error/);
    });
  });

  describe('edge cases', () => {
    it('handles zero values for all metrics', () => {
      render(
        <MetricsCards tasksPerHour={0} avgDurationSec={0} linesPerHour={0} errorRatePct={0} />
      );

      expect(screen.getByText('0s')).toBeInTheDocument();
      expect(screen.getByText('0.0%')).toBeInTheDocument();
    });

    it('handles large values', () => {
      render(
        <MetricsCards
          tasksPerHour={9999}
          avgDurationSec={3661}
          linesPerHour={50000}
          errorRatePct={99.9}
        />
      );

      expect(screen.getByText('9999')).toBeInTheDocument();
      expect(screen.getByText('61m 1s')).toBeInTheDocument();
      expect(screen.getByText('50000')).toBeInTheDocument();
      expect(screen.getByText('99.9%')).toBeInTheDocument();
    });
  });
});
