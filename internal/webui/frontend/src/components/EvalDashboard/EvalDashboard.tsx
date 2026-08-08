import { useMemo, useState, type ReactNode } from "react";

import { ErrorDisplay, LoadingSkeleton } from "@/components";
import { useEvalCron, useEvalRollup } from "@/hooks/evals";
import type {
  EvalInsight,
  EvalInsightCategories,
  EvalRollupData,
  EvalScoreAverages,
} from "@/types";

import styles from "./EvalDashboard.module.css";
import {
  buildScoreTrendGeometry,
  SCORE_DIMENSIONS,
  type ScoreTrendBucketGeometry,
} from "./scoreGeometry";

type EvalWindowDays = 7 | 30;
type InsightCategory = keyof EvalInsightCategories;

const WINDOW_OPTIONS: Array<{ value: EvalWindowDays; label: string }> = [
  { value: 7, label: "7d" },
  { value: 30, label: "30d" },
];

const INSIGHT_CATEGORIES: Array<{ key: InsightCategory; label: string }> = [
  { key: "harness", label: "Harness" },
  { key: "linter", label: "Linter" },
  { key: "prompt", label: "Prompt" },
  { key: "skill", label: "Skill" },
];

function formatScore(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return value.toFixed(0);
}

function sortedInsights(items: EvalInsight[]): EvalInsight[] {
  return [...items].sort((a, b) => {
    const aTime = new Date(a.created_at).getTime();
    const bTime = new Date(b.created_at).getTime();
    return bTime - aTime;
  });
}

function Panel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <section className={styles.panel} aria-label={title}>
      <div className={styles.panelHeader}>
        <h3 className={styles.panelTitle}>{title}</h3>
      </div>
      <div className={styles.panelContent}>{children}</div>
    </section>
  );
}

function ScoreAverageTiles({
  averages,
  evalCount,
}: {
  averages: EvalScoreAverages;
  evalCount: number;
}): JSX.Element {
  return (
    <div className={styles.metricGrid} data-testid="eval-score-tiles">
      {SCORE_DIMENSIONS.map((dimension) => (
        <div key={dimension.key} className={styles.metricCard}>
          <div className={styles.metricLabel}>{dimension.label}</div>
          <div className={styles.metricValue}>
            {formatScore(averages[dimension.key])}
          </div>
          <div className={styles.metricSub}>{evalCount} evals</div>
        </div>
      ))}
    </div>
  );
}

function ScoreTrend({
  geometry,
}: {
  geometry: ScoreTrendBucketGeometry[];
}): JSX.Element {
  if (
    geometry.length === 0 ||
    geometry.every((bucket) => bucket.evalCount === 0)
  ) {
    return <div className={styles.emptyState}>No evals in this window</div>;
  }

  return (
    <div className={styles.trendWrap} data-testid="eval-score-trend">
      <div className={styles.legend}>
        {SCORE_DIMENSIONS.map((dimension) => (
          <span
            key={dimension.key}
            className={styles.legendItem}
            data-dimension={dimension.key}
          >
            <span className={styles.legendSwatch} />
            {dimension.shortLabel}
          </span>
        ))}
      </div>
      <div
        className={styles.trendGrid}
        style={{
          gridTemplateColumns: `repeat(${geometry.length}, minmax(var(--space-8), 1fr))`,
        }}
      >
        {geometry.map((bucket) => (
          <div key={bucket.bucketStart} className={styles.trendBucket}>
            <div className={styles.trendBars}>
              {bucket.bars.map((bar) => (
                <div
                  key={bar.key}
                  className={styles.trendBar}
                  data-dimension={bar.key}
                  style={{ height: `${bar.heightPct}%` }}
                  title={`${bar.label}: ${formatScore(bar.value)}`}
                />
              ))}
            </div>
            <div className={styles.bucketLabel}>{bucket.label}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function FrequencyBars({
  rows,
  emptyLabel,
}: {
  rows: Array<{ label: string; count: number }>;
  emptyLabel: string;
}): JSX.Element {
  if (rows.length === 0) {
    return <div className={styles.emptyState}>{emptyLabel}</div>;
  }
  const max = Math.max(...rows.map((row) => row.count), 1);
  return (
    <div className={styles.frequencyList}>
      {rows.map((row) => {
        const widthPct = (row.count / max) * 100;
        return (
          <div key={row.label} className={styles.frequencyRow}>
            <span className={styles.frequencyLabel} title={row.label}>
              {row.label}
            </span>
            <div className={styles.frequencyTrack}>
              <div
                className={styles.frequencyFill}
                style={{ width: `${widthPct}%` }}
              />
            </div>
            <span className={styles.frequencyCount}>{row.count}</span>
          </div>
        );
      })}
    </div>
  );
}

function ImprovementInsights({
  insights,
}: {
  insights: EvalInsightCategories;
}): JSX.Element {
  const [activeCategory, setActiveCategory] =
    useState<InsightCategory>("harness");
  const [showAll, setShowAll] = useState(false);
  const active = INSIGHT_CATEGORIES.find(
    (category) => category.key === activeCategory,
  );
  const items = sortedInsights(insights[activeCategory] ?? []);
  const visibleItems = showAll ? items : items.slice(0, 10);

  return (
    <div className={styles.insights} data-testid="eval-insights">
      <div className={styles.categoryTabs} role="tablist">
        {INSIGHT_CATEGORIES.map((category) => (
          <button
            key={category.key}
            type="button"
            role="tab"
            className={styles.categoryTab}
            data-active={activeCategory === category.key || undefined}
            onClick={() => {
              setActiveCategory(category.key);
              setShowAll(false);
            }}
          >
            {category.label}
            <span className={styles.categoryCount}>
              {insights[category.key]?.length ?? 0}
            </span>
          </button>
        ))}
      </div>
      {visibleItems.length === 0 ? (
        <div className={styles.emptyState}>
          No {active?.label.toLowerCase() ?? "category"} insights
        </div>
      ) : (
        <div className={styles.insightList}>
          {visibleItems.map((insight) => (
            <article
              key={`${insight.eval_id}-${insight.text}`}
              className={styles.insightItem}
            >
              <p className={styles.insightText}>{insight.text}</p>
              <div className={styles.insightMeta}>{insight.session_id}</div>
            </article>
          ))}
          {items.length > 10 && (
            <button
              type="button"
              className={styles.showMoreButton}
              onClick={() => setShowAll((current) => !current)}
            >
              {showAll ? "Show less" : `Show ${items.length - 10} more`}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function EvalPanels({ rollup }: { rollup: EvalRollupData }): JSX.Element {
  const geometry = useMemo(
    () => buildScoreTrendGeometry(rollup.score_buckets ?? []),
    [rollup.score_buckets],
  );
  const tagRows = [...(rollup.tag_frequencies ?? [])]
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag))
    .map((row) => ({ label: row.tag, count: row.count }));
  const failureRows = [...(rollup.failure_classes ?? [])]
    .sort(
      (a, b) => b.count - a.count || a.error_class.localeCompare(b.error_class),
    )
    .map((row) => ({ label: row.error_class, count: row.count }));

  return (
    <>
      <ScoreAverageTiles
        averages={rollup.score_averages}
        evalCount={rollup.eval_count}
      />
      <Panel title="Score Trend">
        <ScoreTrend geometry={geometry} />
      </Panel>
      <div className={styles.twoColumnRow}>
        <Panel title="Error Tags">
          <FrequencyBars
            rows={tagRows}
            emptyLabel="No error tags in this window"
          />
        </Panel>
        <Panel title="Failure Classes">
          <FrequencyBars
            rows={failureRows}
            emptyLabel="No eval failures in this window"
          />
        </Panel>
      </div>
      <Panel title="Improvement Insights">
        <ImprovementInsights insights={rollup.insights} />
      </Panel>
    </>
  );
}

export function EvalDashboard(): JSX.Element {
  const [windowDays, setWindowDays] = useState<EvalWindowDays>(7);
  const { cron, isLoading: cronLoading, error: cronError } = useEvalCron();
  const rollupEnabled = Boolean(cron?.provisioned);
  const { rollup, isLoading, error, refetch, lastUpdated } = useEvalRollup(
    windowDays,
    {
      enabled: rollupEnabled,
      pollInterval: 60000,
    },
  );

  if (cronLoading && !cron) {
    return (
      <Panel title="Session Evals">
        <LoadingSkeleton shape="text" lines={2} />
      </Panel>
    );
  }

  if (cronError && !cron) {
    return (
      <Panel title="Session Evals">
        <ErrorDisplay variant="fetch-error" error={cronError} showDetails />
      </Panel>
    );
  }

  if (cron && !cron.provisioned) {
    return (
      <Panel title="Session Evals">
        <div className={styles.ctaPanel}>
          Session evals are not enabled — enable in Settings
        </div>
      </Panel>
    );
  }

  return (
    <div className={styles.dashboard} data-testid="eval-dashboard">
      <div className={styles.windowHeader}>
        <div>
          <h2 className={styles.sectionTitle}>Session Evals</h2>
          {lastUpdated && (
            <div className={styles.lastUpdated}>
              Updated {lastUpdated.toLocaleTimeString()}
            </div>
          )}
        </div>
        <div className={styles.segmented} role="group" aria-label="Eval window">
          {WINDOW_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={styles.segment}
              data-active={windowDays === option.value || undefined}
              onClick={() => setWindowDays(option.value)}
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>

      {isLoading && !rollup && (
        <Panel title="Session Evals">
          <LoadingSkeleton shape="text" lines={4} />
        </Panel>
      )}
      {error && !rollup && (
        <Panel title="Session Evals">
          <ErrorDisplay
            variant="fetch-error"
            error={error}
            showDetails
            onRetry={() => void refetch()}
          />
        </Panel>
      )}
      {rollup && <EvalPanels rollup={rollup} />}
    </div>
  );
}
