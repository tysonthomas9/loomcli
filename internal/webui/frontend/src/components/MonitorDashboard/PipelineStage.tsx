/**
 * PipelineStage represents a single stage in the work pipeline.
 * Shows stage name, count badge, and oldest item preview.
 */

import type { LoomTaskInfo } from '@/types';
import styles from './WorkPipelinePanel.module.css';

export interface PipelineStageProps {
  /** Stage identifier */
  id: string;
  /** Display label */
  label: string;
  /** Stage icon */
  icon?: string | undefined;
  /** Number of items in this stage */
  count: number;
  /** The oldest task in this stage */
  oldestItem?: LoomTaskInfo | undefined;
  /** Click handler */
  onClick: (stageId: string) => void;
  /** Visual variant */
  variant?: 'default' | 'blocked';
}

export function PipelineStage({
  id,
  label,
  icon,
  count,
  oldestItem,
  onClick,
  variant = 'default',
}: PipelineStageProps): JSX.Element {
  const handleClick = () => {
    if (count > 0) {
      onClick(id);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.key === 'Enter' || e.key === ' ') && count > 0) {
      e.preventDefault();
      onClick(id);
    }
  };

  const stageClassName = [
    styles.stage,
    variant === 'blocked' && styles.stageBlocked,
    count === 0 && styles.stageEmpty,
    count > 0 && styles.stageClickable,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div
      className={stageClassName}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      role={count > 0 ? 'button' : undefined}
      tabIndex={count > 0 ? 0 : undefined}
      aria-label={`${label}: ${count} items`}
      data-testid={`pipeline-stage-${id}`}
    >
      <div className={styles.stageHeader}>
        {icon && <span className={styles.stageIcon}>{icon}</span>}
        <span className={styles.stageLabel}>{label}</span>
        <span
          className={styles.stageCount}
          data-highlight={count > 0}
        >
          {count}
        </span>
      </div>
      {oldestItem && (
        <div className={styles.stagePreview}>
          <span className={styles.previewId}>{oldestItem.id}</span>
          <span className={styles.previewTitle} title={oldestItem.title}>
            {oldestItem.title}
          </span>
        </div>
      )}
    </div>
  );
}
