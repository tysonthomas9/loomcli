import { useEffect } from "react";

import { useUsage } from "@/hooks/agents";
import type { UsageResponse } from "@/types";
import { plural } from "@/utils/plural";
import { formatCost } from "@/utils/sessionUsage";

import { RailCard } from "@/components/HomeRail/RailCard";

import styles from "./HomeRail.module.css";

export interface BudgetCardProps {
  onUsageChange?: (usage: UsageResponse | null) => void;
}

export function BudgetCard({ onUsageChange }: BudgetCardProps): JSX.Element {
  const { data, isLoading } = useUsage({ pollInterval: 30_000 });
  const topAgent = data?.by_agent
    .slice()
    .sort((a, b) => b.total_cost - a.total_cost)[0];

  useEffect(() => {
    onUsageChange?.(data);
  }, [data, onUsageChange]);

  return (
    <RailCard title="Budget" meta={topAgent?.name ?? "—"} testId="rail-budget">
      <div className={styles.budget}>
        <strong>{data ? formatCost(data.total_cost) : "—"}</strong>
        <span>{isLoading && !data ? "Loading spend…" : "spent"}</span>
      </div>
      <div className={styles.budgetMeta}>
        <span>no cap set</span>
        {data && (
          <span>
            {data.session_count}{" "}
            {plural(data.session_count, "session", "sessions")}
          </span>
        )}
      </div>
    </RailCard>
  );
}
