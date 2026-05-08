/**
 * BackendSetupPanel — Settings-page workbench that shows AI backend
 * status and surfaces curated install/login/env-var instructions.
 *
 * Loom does not store API keys. The panel reads `installed`,
 * `authenticated`, and `ready` from /api/backends and shows the user
 * the next concrete action: install command (copy-to-clipboard), login
 * command, or the env var to set in their shell. "Refresh status"
 * re-checks binaries and auth files; the canonical lifecycle rule is
 * documented in the spec under "Refresh Semantics".
 */

import { useCallback, useState } from "react";

import type {
  BackendHealthData,
  BackendSetupAction,
  BackendEnvVarHint,
} from "@/api/workspace";
import { useBackendsSetup } from "@/hooks/workspace/useBackendsSetup";

import styles from "./BackendSetupPanel.module.css";

export function BackendSetupPanel(): JSX.Element {
  const { backends, isLoading, error, refresh } = useBackendsSetup();
  const [expanded, setExpanded] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const handleRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await refresh();
    } finally {
      setRefreshing(false);
    }
  }, [refresh]);

  if (isLoading && backends.length === 0) {
    return (
      <section className={styles.container} aria-label="AI backends">
        <p className={styles.subtitle}>Loading backend status…</p>
      </section>
    );
  }

  return (
    <section
      className={styles.container}
      aria-label="AI backends"
      data-testid="backend-setup-panel"
    >
      <header>
        <h3 className={styles.heading}>AI Backends</h3>
        <p className={styles.subtitle}>
          Loom reads backend status from your shell at startup. After
          changing an env var, restart Loom or click Refresh.
        </p>
      </header>

      {error ? <p className={styles.error}>{error}</p> : null}

      <div className={styles.list}>
        {backends.map((b) => (
          <BackendRow
            key={b.name}
            backend={b}
            expanded={expanded === b.name}
            onToggle={() =>
              setExpanded((prev) => (prev === b.name ? null : b.name))
            }
            onRefresh={handleRefresh}
            refreshing={refreshing}
          />
        ))}
      </div>
    </section>
  );
}

interface BackendRowProps {
  backend: BackendHealthData;
  expanded: boolean;
  onToggle: () => void;
  onRefresh: () => void;
  refreshing: boolean;
}

function BackendRow({
  backend,
  expanded,
  onToggle,
  onRefresh,
  refreshing,
}: BackendRowProps): JSX.Element {
  const installed = backend.installed;
  const authenticated = backend.authenticated ?? backend.api_key_set;
  const ready = backend.ready ?? (installed && authenticated);

  return (
    <article
      className={styles.row}
      data-testid={`backend-row-${backend.name}`}
      data-ready={ready}
    >
      <div className={styles.summary}>
        <div className={styles.identity}>
          <span className={styles.name}>
            <span
              className={styles.statusDot}
              data-ready={ready}
              aria-hidden="true"
            />
            {backend.display_name || backend.name}
          </span>
          {backend.description ? (
            <span className={styles.description}>{backend.description}</span>
          ) : null}
        </div>
        <div className={styles.badges}>
          <Badge label="installed" on={installed} />
          <Badge label="authenticated" on={authenticated} />
          <Badge label="ready" on={ready} />
        </div>
      </div>

      <div className={styles.actions}>
        <button
          type="button"
          className={styles.refresh}
          onClick={onRefresh}
          disabled={refreshing}
          data-testid={`backend-refresh-${backend.name}`}
        >
          {refreshing ? "Refreshing…" : "Refresh status"}
        </button>
        <button
          type="button"
          className={`${styles.toggle} ${ready ? styles.toggleSecondary : ""}`}
          onClick={onToggle}
          aria-expanded={expanded}
          data-testid={`backend-toggle-${backend.name}`}
        >
          {expanded ? "Hide setup" : ready ? "View setup" : "Configure"}
        </button>
      </div>

      {expanded ? (
        <div className={styles.detail}>
          {backend.message ? (
            <p className={styles.message}>{backend.message}</p>
          ) : null}

          {backend.install_actions && backend.install_actions.length > 0 ? (
            <CommandSection
              label="Install"
              actions={backend.install_actions}
              testIdPrefix={`backend-install-${backend.name}`}
            />
          ) : null}

          {backend.login_actions && backend.login_actions.length > 0 ? (
            <CommandSection
              label="Authenticate"
              actions={backend.login_actions}
              testIdPrefix={`backend-login-${backend.name}`}
            />
          ) : null}

          {backend.env_vars && backend.env_vars.length > 0 ? (
            <EnvVarSection envVars={backend.env_vars} backendName={backend.name} />
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function Badge({ label, on }: { label: string; on: boolean }): JSX.Element {
  return (
    <span
      className={`${styles.badge} ${on ? styles.on : styles.off}`}
      data-on={on}
    >
      {on ? "✓" : "·"} {label}
    </span>
  );
}

interface CommandSectionProps {
  label: string;
  actions: BackendSetupAction[];
  testIdPrefix: string;
}

function CommandSection({
  label,
  actions,
  testIdPrefix,
}: CommandSectionProps): JSX.Element {
  return (
    <div className={styles.section}>
      <span className={styles.sectionLabel}>{label}</span>
      {actions.map((action) => (
        <CommandRow
          key={action.id}
          action={action}
          testId={`${testIdPrefix}-${action.id}`}
        />
      ))}
    </div>
  );
}

function CommandRow({
  action,
  testId,
}: {
  action: BackendSetupAction;
  testId: string;
}): JSX.Element {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(action.command).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      },
      () => {
        // Clipboard API can fail in jsdom; ignore.
      },
    );
  }, [action.command]);

  return (
    <div className={styles.command} data-testid={testId}>
      <span className={styles.commandText}>{action.command}</span>
      <button type="button" className={styles.copyButton} onClick={handleCopy}>
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

function EnvVarSection({
  envVars,
  backendName,
}: {
  envVars: BackendEnvVarHint[];
  backendName: string;
}): JSX.Element {
  return (
    <div className={styles.section}>
      <span className={styles.sectionLabel}>Environment variables</span>
      {envVars.map((env) => (
        <div
          key={env.name}
          className={styles.envVarRow}
          data-testid={`backend-envvar-${backendName}-${env.name}`}
        >
          <code className={styles.envVar}>{env.name}</code>
          {env.restart_required ? (
            <span className={styles.note}>
              set in your shell, then restart Loom
            </span>
          ) : null}
        </div>
      ))}
    </div>
  );
}
