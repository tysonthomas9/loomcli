/**
 * GraphLegend component displays a visual key explaining node and edge styles
 * in the dependency graph. Shows priority levels, status states, and edge types.
 */

import styles from './GraphLegend.module.css';

/**
 * Props for the GraphLegend component.
 */
export interface GraphLegendProps {
  /** Whether the legend is collapsed (default: true) */
  collapsed?: boolean;
  /** Callback when collapse/expand is toggled */
  onToggle?: () => void;
  /** Additional CSS class name */
  className?: string;
}

/** Priority legend items */
const PRIORITY_ITEMS = [
  { level: 0, label: 'Critical', color: 'var(--color-priority-0)' },
  { level: 1, label: 'High', color: 'var(--color-priority-1)' },
  { level: 2, label: 'Medium', color: 'var(--color-priority-2)' },
  { level: 3, label: 'Normal', color: 'var(--color-priority-3)' },
  { level: 4, label: 'Low', color: 'var(--color-priority-4)' },
] as const;

/** Status legend items */
const STATUS_ITEMS = [
  { status: 'open', label: 'Open', color: 'var(--color-status-open)' },
  { status: 'in_progress', label: 'In Progress', color: 'var(--color-status-in-progress)' },
  { status: 'blocked', label: 'Blocked', color: 'var(--color-blocked)' },
  { status: 'closed', label: 'Closed', color: 'var(--color-status-closed)' },
] as const;

/** Edge type legend items */
const EDGE_ITEMS = [
  { type: 'blocking', label: 'Blocking', style: 'dashed' as const },
  { type: 'normal', label: 'Dependency', style: 'solid' as const },
] as const;

/** Special indicator legend items */
const INDICATOR_ITEMS = [
  { type: 'rootBlocker', label: 'Root Blocker', description: 'Blocks others, not blocked' },
  { type: 'blockedBadge', label: 'Blocked Count', description: 'Number blocked by this' },
] as const;

/**
 * GraphLegend renders a collapsible panel explaining graph visual elements.
 *
 * @example
 * ```tsx
 * function GraphView() {
 *   const [collapsed, setCollapsed] = useState(true);
 *
 *   return (
 *     <div className={styles.graphView}>
 *       <ReactFlow nodes={nodes} edges={edges} />
 *       <GraphLegend
 *         collapsed={collapsed}
 *         onToggle={() => setCollapsed(!collapsed)}
 *       />
 *     </div>
 *   );
 * }
 * ```
 */
export function GraphLegend({
  collapsed = true,
  onToggle,
  className,
}: GraphLegendProps): JSX.Element {
  const rootClassName = className ? `${styles.graphLegend} ${className}` : styles.graphLegend;

  return (
    <aside className={rootClassName} data-testid="graph-legend">
      <button
        className={styles.header}
        onClick={onToggle}
        aria-expanded={!collapsed}
        aria-controls="legend-content"
        type="button"
      >
        <span>Legend</span>
        <span className={styles.chevron} data-collapsed={collapsed}>
          ▼
        </span>
      </button>

      {!collapsed && (
        <div id="legend-content" className={styles.content}>
          {/* Priority section */}
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>Priority</h4>
            <dl className={styles.list}>
              {PRIORITY_ITEMS.map((item) => (
                <div key={item.level} className={styles.legendItem}>
                  <dt
                    className={styles.swatch}
                    style={{ backgroundColor: item.color }}
                    aria-hidden="true"
                  />
                  <dd>
                    P{item.level} - {item.label}
                  </dd>
                </div>
              ))}
            </dl>
          </section>

          {/* Status section */}
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>Status</h4>
            <dl className={styles.list}>
              {STATUS_ITEMS.map((item) => (
                <div key={item.status} className={styles.legendItem}>
                  <dt
                    className={styles.swatch}
                    style={{ backgroundColor: item.color }}
                    aria-hidden="true"
                  />
                  <dd>{item.label}</dd>
                </div>
              ))}
            </dl>
          </section>

          {/* Edge types section */}
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>Edges</h4>
            <dl className={styles.list}>
              {EDGE_ITEMS.map((item) => (
                <div key={item.type} className={styles.legendItem}>
                  <dt className={styles.edgeSwatch} data-style={item.style} aria-hidden="true" />
                  <dd>{item.label}</dd>
                </div>
              ))}
            </dl>
          </section>

          {/* Indicator types section */}
          <section className={styles.section}>
            <h4 className={styles.sectionTitle}>Indicators</h4>
            <dl className={styles.list}>
              {INDICATOR_ITEMS.map((item) => (
                <div key={item.type} className={styles.legendItem}>
                  <dt className={styles.indicatorSwatch} data-type={item.type} aria-hidden="true" />
                  <dd title={item.description}>{item.label}</dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      )}
    </aside>
  );
}
