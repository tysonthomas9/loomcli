/**
 * StatBadge - Individual statistic badge component.
 * Displays a label and numeric value with color variant styling.
 */

import { memo } from 'react';

import styles from './StatsHeader.module.css';

/**
 * Props for the StatBadge component.
 */
export interface StatBadgeProps {
  /** Label text (e.g., "Open") */
  label: string;
  /** Numeric value to display */
  value: number;
  /** Visual variant for coloring */
  variant: 'open' | 'progress' | 'ready' | 'closed';
  /** Additional CSS class */
  className?: string;
}

/**
 * Individual stat badge showing a label and value with variant coloring.
 */
export const StatBadge = memo(function StatBadge({
  label,
  value,
  variant,
  className,
}: StatBadgeProps): JSX.Element {
  const classNames = [styles.statBadge, styles[variant], className].filter(Boolean).join(' ');

  return (
    <div className={classNames} title={`${value} ${label}`}>
      <span className={styles.value}>{value}</span>
      <span className={styles.label}>{label}</span>
    </div>
  );
});
