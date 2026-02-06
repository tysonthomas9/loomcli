/**
 * BlockingEdge component for BlockingDependenciesCanvas.
 * Renders dashed red arrows for blocking dependency edges.
 */

import { BaseEdge, getSmoothStepPath, type EdgeProps } from '@xyflow/react';
import { memo } from 'react';

import type { DependencyEdge as DependencyEdgeType } from '@/types';

import styles from './BlockingEdge.module.css';

export type BlockingEdgeProps = EdgeProps<DependencyEdgeType>;

/**
 * BlockingEdge renders a dashed red arrow for blocking dependencies.
 */
function BlockingEdgeComponent({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
}: BlockingEdgeProps): JSX.Element {
  const [edgePath] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });

  const isHighlighted = data?.isHighlighted ?? false;
  const edgeClassName = isHighlighted
    ? `${styles.blockingEdge} ${styles.highlighted}`
    : styles.blockingEdge;

  return (
    <BaseEdge
      id={id}
      path={edgePath}
      className={edgeClassName}
      {...(markerEnd != null && { markerEnd })}
    />
  );
}

export const BlockingEdge = memo(BlockingEdgeComponent);
