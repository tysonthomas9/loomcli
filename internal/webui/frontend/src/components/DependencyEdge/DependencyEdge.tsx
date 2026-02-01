/**
 * DependencyEdge component for React Flow dependency graph.
 * Renders edges between issue nodes with visual styles based on dependency type.
 */

import { memo } from 'react';
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type EdgeProps,
} from '@xyflow/react';
import type { DependencyEdge as DependencyEdgeType, DependencyType } from '@/types';
import styles from './DependencyEdge.module.css';

/**
 * Props for the DependencyEdge component.
 * Extends React Flow EdgeProps with our custom data.
 */
export type DependencyEdgeProps = EdgeProps<DependencyEdgeType>;

/**
 * Map dependency type to CSS class name.
 * Returns the appropriate style class for visual distinction.
 */
function getTypeClassName(
  dependencyType: DependencyType | undefined,
  styleModule: Record<string, string>
): string {
  switch (dependencyType) {
    case 'blocks':
      return styleModule.typeBlocks ?? '';
    case 'parent-child':
      return styleModule.typeParentChild ?? '';
    case 'conditional-blocks':
      return styleModule.typeConditionalBlocks ?? '';
    case 'waits-for':
      return styleModule.typeWaitsFor ?? '';
    case 'related':
      return styleModule.typeRelated ?? '';
    default:
      return styleModule.typeDefault ?? '';
  }
}

/**
 * DependencyEdge renders a dependency relationship between two issue nodes.
 * Uses smooth step paths for a clean graph appearance.
 * Edges are styled based on their dependency type.
 */
function DependencyEdgeComponent({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  selected,
}: DependencyEdgeProps): JSX.Element {
  // Use smooth step path for cleaner graph appearance
  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });

  const isHighlighted = data?.isHighlighted ?? false;
  const dependencyType = data?.dependencyType ?? 'blocks';

  // Format label text (display as-is)
  const labelText = dependencyType;

  // Build class names: type-specific + optional highlighted
  let edgeClassName = getTypeClassName(dependencyType, styles);
  if (isHighlighted) {
    edgeClassName = `${edgeClassName} ${styles.highlighted}`;
  }

  const labelClassName = selected
    ? `${styles.edgeLabel} ${styles.selected}`
    : styles.edgeLabel;

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        className={edgeClassName}
        markerEnd="url(#arrow)"
      />
      <EdgeLabelRenderer>
        <div
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
            pointerEvents: 'all',
          }}
          className={labelClassName}
          data-type={dependencyType}
        >
          {labelText}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

// Memoize to prevent unnecessary re-renders
export const DependencyEdge = memo(DependencyEdgeComponent);
