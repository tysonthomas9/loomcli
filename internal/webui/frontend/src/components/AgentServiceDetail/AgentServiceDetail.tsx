import type { AgentServiceDTO, DriverRunDTO } from "@/api/agentServices";
import { useAgentServiceRuns } from "@/hooks/workspace";
import {
  agentServiceDotState,
  agentServiceHealthLabel,
  bindingCadenceLabel,
  firstEnabledCronBinding,
  formatFireTime,
} from "@/utils/bindingDisplay";
import { formatStatusLabel } from "@/utils/issue";

import styles from "./AgentServiceDetail.module.css";

export interface AgentServiceDetailProps {
  workspaceId: string;
  service: AgentServiceDTO;
}

function behaviorLabel(service: AgentServiceDTO): string {
  if (service.kind === "scripted") return "Scripted autonomous agent";
  if (service.kind === "prompt") return "Prompt autonomous agent";
  return "Autonomous agent";
}

function runTimestamp(run: DriverRunDTO): string {
  return formatFireTime(run.startedAt ?? run.createdAt);
}

function runSummary(run: DriverRunDTO): string {
  return run.summary?.trim() || run.errorClass?.trim() || "No run summary";
}

export function AgentServiceDetail({
  workspaceId,
  service,
}: AgentServiceDetailProps): JSX.Element {
  const { runs, total, loading, initialized, error, notFound } =
    useAgentServiceRuns(workspaceId, service.id);
  const nextFire = formatFireTime(service.nextFireAt);
  const nextFireBinding = firstEnabledCronBinding(service);
  const healthLabel = agentServiceHealthLabel(service);

  if (notFound) {
    return (
      <div className={styles.empty} data-testid="agent-service-not-found">
        This autonomous agent no longer exists.
      </div>
    );
  }

  return (
    <div className={styles.detail} data-testid="agent-service-detail">
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Autonomous</p>
          <h1 className={styles.name}>{service.name.trim() || service.id}</h1>
          <p className={styles.subtitle}>{behaviorLabel(service)}</p>
        </div>
        <span
          className={styles.healthPill}
          data-state={agentServiceDotState(service)}
        >
          <span className={styles.healthDot} aria-hidden="true" />
          {healthLabel}
        </span>
      </header>

      <div className={styles.scrollArea}>
        {service.errors.length > 0 ? (
          <section
            className={`${styles.card} ${styles.warningCard}`}
            role="alert"
            data-testid="agent-service-health-errors"
          >
            <h2 className={styles.cardTitle}>Health unavailable</h2>
            <ul className={styles.errorList}>
              {service.errors.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          </section>
        ) : null}

        <section className={styles.card}>
          <h2 className={styles.cardTitle}>Record</h2>
          <dl className={styles.definitionGrid}>
            <div>
              <dt>ID</dt>
              <dd>{service.id}</dd>
            </div>
            <div>
              <dt>Kind</dt>
              <dd>{formatStatusLabel(service.kind)}</dd>
            </div>
            <div>
              <dt>Desired state</dt>
              <dd>{service.enabled ? "Enabled" : "Disabled"}</dd>
            </div>
            <div>
              <dt>Last run</dt>
              <dd>
                {service.lastRunStatus
                  ? formatStatusLabel(service.lastRunStatus)
                  : "No runs"}
              </dd>
            </div>
            <div>
              <dt>Consecutive failures</dt>
              <dd>{service.consecutiveFailures}</dd>
            </div>
            <div>
              <dt>Next fire</dt>
              <dd>{nextFire || "Not scheduled"}</dd>
            </div>
            {service.behavior.roleName ? (
              <div>
                <dt>Role</dt>
                <dd>{service.behavior.roleName}</dd>
              </div>
            ) : null}
            {service.behavior.driverId ? (
              <div>
                <dt>Driver</dt>
                <dd>{service.behavior.driverId}</dd>
              </div>
            ) : null}
            {service.behavior.driverVersionId ? (
              <div>
                <dt>Driver version</dt>
                <dd>{service.behavior.driverVersionId}</dd>
              </div>
            ) : null}
          </dl>
        </section>

        <section className={styles.card}>
          <h2 className={styles.cardTitle}>Bindings</h2>
          {service.bindings.length === 0 ? (
            <p className={styles.emptyText}>No bindings configured.</p>
          ) : (
            <div className={styles.bindingList}>
              {service.bindings.map((binding) => (
                <article className={styles.bindingRow} key={binding.id}>
                  <div className={styles.rowMain}>
                    <strong>{bindingCadenceLabel(binding)}</strong>
                    <span>{binding.routeKey || binding.id}</span>
                  </div>
                  <div className={styles.rowMeta}>
                    <span>{binding.enabled ? "Enabled" : "Disabled"}</span>
                    <span>
                      {binding.id === nextFireBinding?.id && nextFire
                        ? `Next ${nextFire}`
                        : "No next fire"}
                    </span>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>

        <section className={styles.card} data-testid="agent-service-runs">
          <div className={styles.cardHeadingRow}>
            <h2 className={styles.cardTitle}>Recent runs</h2>
            {initialized && !error ? (
              <span className={styles.total}>{total}</span>
            ) : null}
          </div>
          {error ? (
            <p className={styles.errorText} role="alert">
              Run history unavailable: {error.message}
            </p>
          ) : loading && !initialized ? (
            <p className={styles.emptyText}>Loading run history…</p>
          ) : runs.length === 0 ? (
            <p className={styles.emptyText}>No runs yet.</p>
          ) : (
            <div className={styles.runList}>
              {runs.map((run) => (
                <article
                  className={styles.runRow}
                  data-testid={`agent-service-run-${run.runId}`}
                  key={run.runId}
                >
                  <span className={styles.runStatus} data-status={run.status}>
                    {formatStatusLabel(run.status)}
                  </span>
                  <div className={styles.rowMain}>
                    <strong>{runSummary(run)}</strong>
                    <span>{run.runId}</span>
                  </div>
                  <div className={styles.runTimes}>
                    <time dateTime={run.startedAt ?? run.createdAt}>
                      Started {runTimestamp(run)}
                    </time>
                    {run.finishedAt && formatFireTime(run.finishedAt) ? (
                      <time dateTime={run.finishedAt}>
                        Finished {formatFireTime(run.finishedAt)}
                      </time>
                    ) : null}
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
