/**
 * LoadingSkeleton component for displaying animated placeholder content.
 * Provides visual feedback during async operations with shimmer animation.
 * Supports base shapes (rect, text, circle) and preset variants for IssueCard and StatusColumn.
 */

import styles from "./LoadingSkeleton.module.css";

/**
 * Shape variants for the base skeleton.
 */
export type SkeletonShape = "rect" | "text" | "circle";

/**
 * Props for the LoadingSkeleton component.
 */
export interface LoadingSkeletonProps {
  /** Shape variant: 'rect' (default), 'text', or 'circle' */
  shape?: SkeletonShape;
  /** Width in pixels or CSS value (e.g., '100%') */
  width?: number | string;
  /** Height in pixels or CSS value */
  height?: number | string;
  /** Additional CSS class name */
  className?: string;
  /** Number of skeleton lines for 'text' shape */
  lines?: number;
}

/**
 * Props for preset skeleton variants.
 */
export interface LoadingSkeletonPresetProps {
  /** Additional CSS class name */
  className?: string;
}

export type LoadingSkeletonCardProps = LoadingSkeletonPresetProps;

export interface LoadingSkeletonColumnProps extends LoadingSkeletonPresetProps {
  /** Number of card skeletons to show */
  cardCount?: number;
}

/**
 * Base LoadingSkeleton component.
 * Renders an animated placeholder in the specified shape.
 */
export function LoadingSkeleton({
  shape = "rect",
  width,
  height,
  className,
  lines = 1,
}: LoadingSkeletonProps): JSX.Element {
  // Build class names
  const shapeClass = styles[shape] ?? styles.rect;
  const rootClassName = className
    ? `${styles.skeleton} ${shapeClass} ${className}`
    : `${styles.skeleton} ${shapeClass}`;

  // Build inline styles for custom dimensions
  const style: React.CSSProperties = {};
  if (width !== undefined) {
    style.width = typeof width === "number" ? `${width}px` : width;
  }
  if (height !== undefined) {
    style.height = typeof height === "number" ? `${height}px` : height;
  }

  // For text shape with multiple lines
  if (shape === "text" && lines > 1) {
    return (
      <div className={styles.textContainer} aria-hidden="true">
        {Array.from({ length: lines }, (_, i) => (
          <div
            key={i}
            className={rootClassName}
            style={{
              ...style,
              // Last line is shorter
              width: i === lines - 1 ? "60%" : style.width,
            }}
          />
        ))}
      </div>
    );
  }

  return (
    <div
      className={rootClassName}
      style={Object.keys(style).length > 0 ? style : undefined}
      aria-hidden="true"
    />
  );
}

/**
 * Card skeleton matching IssueCard dimensions.
 * Use when loading cards in the Kanban board.
 */
function Card({ className }: LoadingSkeletonCardProps): JSX.Element {
  const rootClassName = className ? `${styles.card} ${className}` : styles.card;

  return (
    <div className={rootClassName} aria-hidden="true">
      <div className={styles.cardHeader}>
        <LoadingSkeleton shape="text" width={60} height={12} />
        <LoadingSkeleton shape="rect" width={28} height={20} />
      </div>
      <LoadingSkeleton shape="text" lines={2} height={14} />
    </div>
  );
}

/**
 * Column skeleton matching StatusColumn header.
 * Use when loading columns in the Kanban board.
 */
function Column({
  className,
  cardCount = 3,
}: LoadingSkeletonColumnProps): JSX.Element {
  const rootClassName = className
    ? `${styles.column} ${className}`
    : styles.column;

  return (
    <div className={rootClassName} aria-hidden="true">
      <div className={styles.columnHeader}>
        <LoadingSkeleton shape="text" width={80} height={16} />
        <LoadingSkeleton shape="circle" width={24} height={24} />
      </div>
      <div className={styles.columnContent}>
        {Array.from({ length: cardCount }, (_, i) => (
          <Card key={i} />
        ))}
      </div>
    </div>
  );
}

/**
 * Graph skeleton matching GraphView layout.
 * Use when lazy loading the GraphView component.
 * Shows a placeholder with simulated nodes and edges.
 */
function Graph({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.graph} ${className}`
    : styles.graph;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-graph"
    >
      {/* Simulated graph nodes */}
      <div className={styles.graphNodes}>
        <div className={styles.graphNode}>
          <LoadingSkeleton shape="rect" width={120} height={60} />
        </div>
        <div className={styles.graphNode}>
          <LoadingSkeleton shape="rect" width={120} height={60} />
        </div>
        <div className={styles.graphNode}>
          <LoadingSkeleton shape="rect" width={120} height={60} />
        </div>
      </div>
      {/* MiniMap placeholder */}
      <div className={styles.graphMiniMap}>
        <LoadingSkeleton shape="rect" width={150} height={100} />
      </div>
    </div>
  );
}

/**
 * Monitor skeleton matching MonitorDashboard layout.
 * Use when lazy loading the MonitorDashboard component.
 * Shows a 2x2 grid of placeholder panels.
 */
function Monitor({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.monitor} ${className}`
    : styles.monitor;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-monitor"
    >
      {[1, 2, 3, 4].map((i) => (
        <div key={i} className={styles.monitorPanel}>
          <div className={styles.monitorPanelHeader}>
            <LoadingSkeleton shape="text" width={100} height={14} />
          </div>
          <div className={styles.monitorPanelContent}>
            <LoadingSkeleton shape="rect" width="100%" height={120} />
          </div>
        </div>
      ))}
    </div>
  );
}

/**
 * DetailPanel skeleton matching IssueDetailPanel layout.
 * Use when loading issue details in a slide-out panel.
 */
function DetailPanel({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.detailPanel} ${className}`
    : styles.detailPanel;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-detail-panel"
    >
      <div className={styles.detailPanelHeader}>
        <LoadingSkeleton shape="text" width={200} height={18} />
        <LoadingSkeleton shape="rect" width={24} height={24} />
      </div>
      <div className={styles.detailPanelMeta}>
        <LoadingSkeleton shape="rect" width={60} height={22} />
        <LoadingSkeleton shape="rect" width={80} height={22} />
        <LoadingSkeleton shape="rect" width={70} height={22} />
      </div>
      <div className={styles.detailPanelBody}>
        <LoadingSkeleton shape="text" lines={4} />
      </div>
      <div className={styles.detailPanelSection}>
        <LoadingSkeleton shape="text" width={120} height={14} />
        <LoadingSkeleton shape="rect" width="100%" height={60} />
      </div>
    </div>
  );
}

/**
 * Table skeleton matching IssueTable layout.
 * Use when loading the table view.
 */
function Table({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.table} ${className}`
    : styles.table;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-table"
    >
      <div className={styles.tableHeader}>
        <LoadingSkeleton shape="text" width={80} height={14} />
        <LoadingSkeleton shape="text" width={200} height={14} />
        <LoadingSkeleton shape="text" width={60} height={14} />
        <LoadingSkeleton shape="text" width={80} height={14} />
      </div>
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} className={styles.tableRow}>
          <LoadingSkeleton shape="text" width={70} height={12} />
          <LoadingSkeleton shape="text" width={180} height={12} />
          <LoadingSkeleton shape="text" width={50} height={12} />
          <LoadingSkeleton shape="text" width={70} height={12} />
        </div>
      ))}
    </div>
  );
}

/**
 * FileExplorer skeleton matching the two-pane file browser layout.
 * Use when lazy loading the FileExplorer component.
 */
function FileExplorerSkeleton({
  className,
}: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.fileExplorer} ${className}`
    : styles.fileExplorer;

  const treeLevels = [0, 1, 1, 2, 2, 1, 0, 1];

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-file-explorer"
    >
      <div className={styles.fileTree}>
        {treeLevels.map((level, i) => (
          <div
            key={i}
            className={styles.fileTreeItem}
            style={{ paddingLeft: `${level * 16 + 8}px` }}
          >
            <LoadingSkeleton
              shape="text"
              width={100 + (i % 4) * 20}
              height={12}
            />
          </div>
        ))}
      </div>
      <div className={styles.codeArea}>
        {Array.from({ length: 6 }, (_, i) => (
          <LoadingSkeleton
            key={i}
            shape="text"
            width={`${40 + (i % 3) * 20}%`}
            height={12}
          />
        ))}
      </div>
    </div>
  );
}

/**
 * Terminal skeleton matching TerminalView layout.
 * Use when lazy loading the TerminalView component.
 */
function TerminalSkeleton({
  className,
}: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.terminal} ${className}`
    : styles.terminal;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-terminal"
    >
      <div className={styles.terminalTabBar}>
        <LoadingSkeleton shape="rect" width={100} height={28} />
        <LoadingSkeleton shape="rect" width={100} height={28} />
      </div>
      <div className={styles.terminalBody}>
        {Array.from({ length: 4 }, (_, i) => (
          <LoadingSkeleton
            key={i}
            shape="text"
            width={`${30 + (i % 3) * 15}%`}
            height={12}
          />
        ))}
      </div>
    </div>
  );
}

/**
 * Observability skeleton matching ObservabilityDashboard layout.
 * Use when lazy loading the ObservabilityDashboard component.
 */
function Observability({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.observability} ${className}`
    : styles.observability;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-observability"
    >
      <div className={styles.observabilityCards}>
        {[1, 2, 3, 4].map((i) => (
          <div key={i} className={styles.observabilityCard}>
            <LoadingSkeleton shape="text" width={60} height={10} />
            <LoadingSkeleton shape="text" width={80} height={24} />
          </div>
        ))}
      </div>
      <div className={styles.observabilityChart}>
        <LoadingSkeleton shape="text" width={140} height={14} />
        <LoadingSkeleton shape="rect" width="100%" height={160} />
      </div>
    </div>
  );
}

/**
 * AgentDetail skeleton matching AgentDetailPanel layout.
 * Use when loading agent details in a slide-out panel.
 */
function AgentDetail({ className }: LoadingSkeletonPresetProps): JSX.Element {
  const rootClassName = className
    ? `${styles.agentDetail} ${className}`
    : styles.agentDetail;

  return (
    <div
      className={rootClassName}
      aria-hidden="true"
      data-testid="loading-skeleton-agent-detail"
    >
      <div className={styles.agentDetailHeader}>
        <LoadingSkeleton shape="circle" width={40} height={40} />
        <div className={styles.agentDetailInfo}>
          <LoadingSkeleton shape="text" width={120} height={16} />
          <LoadingSkeleton shape="text" width={80} height={12} />
        </div>
      </div>
      <div className={styles.agentDetailTabs}>
        <LoadingSkeleton shape="rect" width={50} height={28} />
        <LoadingSkeleton shape="rect" width={50} height={28} />
        <LoadingSkeleton shape="rect" width={50} height={28} />
      </div>
      <div className={styles.agentDetailContent}>
        <LoadingSkeleton shape="text" lines={3} />
      </div>
    </div>
  );
}

// Attach preset variants as static properties
LoadingSkeleton.Card = Card;
LoadingSkeleton.Column = Column;
LoadingSkeleton.Graph = Graph;
LoadingSkeleton.Monitor = Monitor;
LoadingSkeleton.DetailPanel = DetailPanel;
LoadingSkeleton.Table = Table;
LoadingSkeleton.FileExplorer = FileExplorerSkeleton;
LoadingSkeleton.Terminal = TerminalSkeleton;
LoadingSkeleton.Observability = Observability;
LoadingSkeleton.AgentDetail = AgentDetail;
