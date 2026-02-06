/**
 * TypeIcon component for displaying issue type icons.
 * Renders an SVG icon based on the issue type (bug, feature, task, epic, chore).
 */

import type { IssueType, KnownIssueType } from '@/types';
import { isKnownIssueType } from '@/types';

import styles from './TypeIcon.module.css';

/**
 * Props for the TypeIcon component.
 */
export interface TypeIconProps {
  /** Issue type to display icon for */
  type: IssueType | undefined;
  /** Icon size in pixels (default: 16) */
  size?: number;
  /** Additional CSS class name */
  className?: string;
  /** Custom aria-label for accessibility */
  'aria-label'?: string;
}

/**
 * SVG path data for each issue type icon.
 * Icons are designed to be recognizable at small sizes.
 */
const ICON_PATHS: Record<KnownIssueType | 'unknown', string> = {
  // Bug icon - ladybug shape
  bug: 'M14 12h-4v-2h4v2zm0 4h-4v-2h4v2zm3-9.95l-1.41-1.41-.71.71a6.97 6.97 0 0 0-5.76 0l-.71-.71-1.41 1.41.71.71c-.49.75-.81 1.6-.93 2.52h-1.78v2h1.78c.12.92.44 1.77.93 2.52l-.71.71 1.41 1.41.71-.71a6.97 6.97 0 0 0 5.76 0l.71.71 1.41-1.41-.71-.71c.49-.75.81-1.6.93-2.52h1.78v-2h-1.78c-.12-.92-.44-1.77-.93-2.52l.71-.71zM12 18c-2.21 0-4-1.79-4-4s1.79-4 4-4 4 1.79 4 4-1.79 4-4 4zm0-14a2 2 0 0 0-2 2h4a2 2 0 0 0-2-2z',
  // Feature icon - sparkle/star
  feature:
    'M12 2l2.09 6.26L20 9.27l-4.91 3.78L16.18 20 12 16.77 7.82 20l1.09-6.95L4 9.27l5.91-1.01L12 2z',
  // Task icon - checkbox with check
  task: 'M19 3H5c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zm0 16H5V5h14v14zM17.99 9l-1.41-1.42-6.59 6.59-2.58-2.57-1.42 1.41 4 3.99 8-8z',
  // Epic icon - mountain/large scope
  epic: 'M12 2L2 22h20L12 2zm0 4.5l6.62 13.5H5.38L12 6.5z',
  // Chore icon - wrench/gear
  chore:
    'M22.7 19l-9.1-9.1c.9-2.3.4-5-1.5-6.9-2-2-5-2.4-7.4-1.3L9 6 6 9 1.6 4.7C.4 7.1.9 10.1 2.9 12.1c1.9 1.9 4.6 2.4 6.9 1.5l9.1 9.1c.4.4 1 .4 1.4 0l2.3-2.3c.5-.4.5-1.1.1-1.4z',
  // Unknown/fallback icon - circle
  unknown:
    'M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8z',
};

/**
 * TypeIcon displays an icon representing the issue type.
 * Provides visual recognition for bug, feature, task, epic, and chore types.
 */
export function TypeIcon({
  type,
  size = 16,
  className,
  'aria-label': ariaLabel,
}: TypeIconProps): JSX.Element {
  // Determine which icon to display
  const iconType = type && isKnownIssueType(type) ? type : 'unknown';
  const label = ariaLabel ?? (type ? `${type} type` : 'unknown type');

  const rootClassName = className ? `${styles.typeIcon} ${className}` : styles.typeIcon;

  return (
    <svg
      className={rootClassName}
      data-type={iconType}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      role="img"
      aria-label={label}
    >
      <path d={ICON_PATHS[iconType]} />
    </svg>
  );
}
