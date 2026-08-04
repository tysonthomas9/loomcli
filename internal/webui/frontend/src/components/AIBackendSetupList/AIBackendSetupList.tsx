import type { BackendInfo } from "@/utils/workspace";
import type { AIBackendSetupAction } from "@/utils/cliSetup";

import styles from "./AIBackendSetupList.module.css";

export type { AIBackendSetupAction } from "@/utils/cliSetup";

export interface AIBackendSetupListProps {
  backends: BackendInfo[];
  defaultBackend?: string | undefined;
  variant?: "list" | "matrix";
  isLoading?: boolean;
  error?: string | null;
  isSavingDefault?: boolean;
  onAction?: (backend: BackendInfo, action: AIBackendSetupAction) => void;
}

interface BackendReadiness {
  label: string;
  tone: "ready" | "warning" | "missing" | "neutral";
  action: AIBackendSetupAction;
  actionLabel: string;
}

function readinessFor(backend: BackendInfo): BackendReadiness {
  if (backend.available) {
    return {
      label: "Ready",
      tone: "ready",
      action: "test",
      actionLabel: "Test",
    };
  }
  if (backend.installed === false) {
    return {
      label: "Not installed",
      tone: "missing",
      action: "install",
      actionLabel: "Install",
    };
  }
  if (backend.apiKeySet === false) {
    return {
      label: "Login needed",
      tone: "warning",
      action: "login",
      actionLabel: "Login",
    };
  }
  return {
    label: "Unavailable",
    tone: "neutral",
    action: "configure",
    actionLabel: "Configure",
  };
}

function authLabel(backend: BackendInfo): string {
  if (backend.available) return "Ready";
  if (backend.apiKeySet === true) return "Ready";
  if (backend.apiKeySet === false) return "Needed";
  return "-";
}

function installedLabel(backend: BackendInfo): string {
  if (backend.installed === true) return "Yes";
  if (backend.installed === false) return "No";
  return "-";
}

function sortedBackends(
  backends: BackendInfo[],
  defaultBackend?: string,
): BackendInfo[] {
  return [...backends]
    .filter((backend) => backend.name !== "shell")
    .sort((a, b) => {
      if (a.name === defaultBackend) return -1;
      if (b.name === defaultBackend) return 1;
      if (a.available !== b.available) return a.available ? -1 : 1;
      return a.displayName.localeCompare(b.displayName);
    });
}

export function AIBackendSetupList({
  backends,
  defaultBackend,
  variant = "list",
  isLoading = false,
  error = null,
  isSavingDefault = false,
  onAction,
}: AIBackendSetupListProps): JSX.Element {
  const visibleBackends = sortedBackends(backends, defaultBackend);
  const rootClassName = [
    styles.root,
    variant === "matrix" ? styles.matrixVariant : styles.listVariant,
  ].join(" ");

  if (isLoading && visibleBackends.length === 0) {
    return (
      <div className={rootClassName} data-testid="ai-backend-setup-list">
        <p className={styles.emptyText}>Checking AI CLIs...</p>
      </div>
    );
  }

  if (error && visibleBackends.length === 0) {
    return (
      <div className={rootClassName} data-testid="ai-backend-setup-list">
        <p className={styles.emptyText}>CLI status unavailable.</p>
      </div>
    );
  }

  if (visibleBackends.length === 0) {
    return (
      <div className={rootClassName} data-testid="ai-backend-setup-list">
        <p className={styles.emptyText}>No AI CLIs detected.</p>
      </div>
    );
  }

  if (variant === "matrix") {
    return (
      <div className={rootClassName} data-testid="ai-backend-setup-list">
        <table className={styles.matrix}>
          <thead>
            <tr>
              <th scope="col">CLI</th>
              <th scope="col">Installed</th>
              <th scope="col">Auth</th>
              <th scope="col">Default CLI</th>
              <th scope="col">Action</th>
            </tr>
          </thead>
          <tbody>
            {visibleBackends.map((backend) => {
              const readiness = readinessFor(backend);
              const isDefault = backend.name === defaultBackend;
              const action =
                backend.available && !isDefault
                  ? "set-default"
                  : readiness.action;
              const actionLabel =
                backend.available && !isDefault
                  ? "Set Default"
                  : readiness.actionLabel;
              return (
                <tr key={backend.name}>
                  <td>
                    <div className={styles.backendName}>
                      <span
                        className={styles.backendDot}
                        style={{ backgroundColor: backend.brandColor }}
                        aria-hidden="true"
                      />
                      <span>{backend.displayName}</span>
                    </div>
                  </td>
                  <td>{installedLabel(backend)}</td>
                  <td>{authLabel(backend)}</td>
                  <td>{isDefault ? "Yes" : "-"}</td>
                  <td>
                    <button
                      type="button"
                      className={styles.actionButton}
                      onClick={() => onAction?.(backend, action)}
                      disabled={isSavingDefault}
                    >
                      {isDefault && backend.available ? "Test" : actionLabel}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="ai-backend-setup-list">
      {visibleBackends.map((backend) => {
        const readiness = readinessFor(backend);
        const isDefault = backend.name === defaultBackend;
        const action =
          backend.available && !isDefault ? "set-default" : readiness.action;
        const actionLabel =
          backend.available && !isDefault ? "Use" : readiness.actionLabel;
        return (
          <div key={backend.name} className={styles.cliRow}>
            <div className={styles.cliMain}>
              <div className={styles.cliTitleRow}>
                <span
                  className={styles.backendDot}
                  style={{ backgroundColor: backend.brandColor }}
                  aria-hidden="true"
                />
                <span className={styles.cliName}>{backend.displayName}</span>
                <span
                  className={`${styles.statusBadge} ${styles[readiness.tone]}`}
                >
                  {isDefault && backend.available ? "Default" : readiness.label}
                </span>
              </div>
            </div>
            <button
              type="button"
              className={styles.actionButton}
              onClick={() => onAction?.(backend, action)}
              disabled={isSavingDefault}
            >
              {isDefault && backend.available ? "Test" : actionLabel}
            </button>
          </div>
        );
      })}
    </div>
  );
}
