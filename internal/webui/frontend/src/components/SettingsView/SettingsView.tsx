/**
 * SettingsView component.
 * Displays project backend configuration with a dropdown and per-agent overrides table.
 */

import { useEffect, useState } from "react";

import { ErrorDisplay } from "@/components/ErrorDisplay";
import { LoadingSkeleton } from "@/components/LoadingSkeleton";
import type { ViewMode } from "@/types";
import {
  useBackendConfig,
  useLocalSettings,
  useWorkspaceContext,
} from "@/hooks/workspace";
import {
  useTerminalFont,
  FONT_FAMILY_OPTIONS,
  FONT_SIZE_OPTIONS,
  CUSTOM_FONT_SENTINEL,
  DEFAULT_FONT_FAMILY,
} from "@/hooks/terminal";
import { useToast } from "@/hooks/ui";

import styles from "./SettingsView.module.css";

export interface SettingsViewProps {
  className?: string;
  onNavigate?: (view: ViewMode) => void;
}

interface RedisFormState {
  enabled: boolean;
  url: string;
  addr: string;
  password: string;
  db: string;
  tls: boolean;
}

const EMPTY_REDIS_FORM: RedisFormState = {
  enabled: false,
  url: "",
  addr: "",
  password: "",
  db: "0",
  tls: false,
};

export function SettingsView({
  className,
  onNavigate,
}: SettingsViewProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const {
    config,
    isLoading,
    error,
    isSaving,
    isCached,
    updateBackend,
    refetch,
  } = useBackendConfig(workspaceId);
  const { showToast } = useToast();
  const [selectedBackend, setSelectedBackend] = useState<string | null>(null);
  const {
    settings: localSettings,
    isSaving: isSavingLocalSettings,
    error: localSettingsError,
    updateRedis,
  } = useLocalSettings();
  const redisSettings = localSettings?.fleetdb_redis;
  const [redisForm, setRedisForm] = useState<RedisFormState>(EMPTY_REDIS_FORM);

  const { fontFamily, fontSize, setFontFamily, setFontSize } =
    useTerminalFont();

  // Track whether the user has explicitly opened the custom font input
  const [customFontMode, setCustomFontMode] = useState(false);

  // Determine if current fontFamily matches a preset option
  const isPresetFont = FONT_FAMILY_OPTIONS.some(
    (opt) => opt.value !== CUSTOM_FONT_SENTINEL && opt.value === fontFamily,
  );
  const fontSelectValue =
    isPresetFont && !customFontMode ? fontFamily : CUSTOM_FONT_SENTINEL;
  const showCustomFontInput = customFontMode || !isPresetFont;

  useEffect(() => {
    if (!redisSettings) return;
    setRedisForm({
      enabled: redisSettings.enabled,
      addr: redisSettings.addr ?? "",
      db: String(redisSettings.db ?? 0),
      tls: redisSettings.tls,
      password: "",
      url: "",
    });
  }, [redisSettings]);

  const rootClassName = [styles.settingsView, className]
    .filter(Boolean)
    .join(" ");

  // Loading state
  if (isLoading && !config) {
    return (
      <div className={rootClassName} data-testid="settings-view">
        <h2 className={styles.pageTitle}>Settings</h2>
        <div className={styles.panel}>
          <div className={styles.panelHeader}>
            <h3 className={styles.panelTitle}>Project Default Backend</h3>
          </div>
          <div className={styles.panelContent}>
            <LoadingSkeleton shape="text" lines={3} />
          </div>
        </div>
      </div>
    );
  }

  // Error state (no data at all)
  if (error && !config) {
    return (
      <div className={rootClassName} data-testid="settings-view">
        <h2 className={styles.pageTitle}>Settings</h2>
        <ErrorDisplay
          variant="fetch-error"
          title="Backend configuration unavailable"
          error={new Error(error)}
          showDetails
          onRetry={refetch}
          isRetrying={isLoading}
        />
      </div>
    );
  }

  if (!config) return <div className={rootClassName} />;

  // Determine the current dropdown value
  const currentValue = selectedBackend ?? config.backend;
  const hasChanges = currentValue !== config.backend;

  // Check if any agents have backend overrides
  const agentsWithOverrides = config.agents.filter((a) => a.backend !== "");

  const handleSelectChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setSelectedBackend(e.target.value);
  };

  const handleSave = async () => {
    if (!hasChanges || isSaving) return;

    const ok = await updateBackend(currentValue);
    if (ok) {
      setSelectedBackend(null);
      showToast("Backend updated successfully", { type: "success" });
    } else {
      showToast("Failed to update backend", { type: "error" });
    }
  };

  const handleRedisUrlChange = (value: string) => {
    const trimmed = value.trim();
    setRedisForm((current) => ({
      ...current,
      url: value,
      tls:
        current.tls ||
        trimmed.startsWith("rediss://") ||
        /(^|\s)--tls(\s|$)/.test(value),
    }));
  };

  const handleRedisSave = async () => {
    if (isSavingLocalSettings) return;
    const db = Number(redisForm.db);
    if (!Number.isInteger(db) || db < 0) {
      showToast("Redis DB must be 0 or greater", { type: "error" });
      return;
    }
    const payload = {
      enabled: redisForm.enabled,
      db,
      tls: redisForm.tls,
      ...(redisForm.url.trim() ? { redis_url: redisForm.url.trim() } : {}),
      ...(!redisForm.url.trim() && redisForm.addr.trim()
        ? { addr: redisForm.addr.trim() }
        : {}),
      ...(redisForm.password ? { password: redisForm.password } : {}),
    };
    const ok = await updateRedis(payload);
    if (ok) {
      setRedisForm((current) => ({ ...current, password: "", url: "" }));
      showToast("Redis settings saved. Restart Loom to apply them.", {
        type: "success",
      });
    } else {
      showToast("Failed to save Redis settings", { type: "error" });
    }
  };

  return (
    <div className={rootClassName} data-testid="settings-view">
      <h2 className={styles.pageTitle}>Settings</h2>

      {/* Project Default Backend */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>
            Project Default Backend
            {isCached && (
              <span className={styles.cachedBadge} data-testid="cached-badge">
                (cached)
              </span>
            )}
          </h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="backend-select">
              Default Backend
            </label>
            <p className={styles.description}>
              The AI backend used for new agents unless overridden.
            </p>
            <div className={styles.selectRow}>
              <select
                id="backend-select"
                className={styles.select}
                value={currentValue}
                onChange={handleSelectChange}
                data-testid="backend-select"
              >
                {config.available.map((b) => (
                  <option key={b} value={b}>
                    {b}
                  </option>
                ))}
              </select>
              <span className={styles.sourceTag}>
                {config.source === "fleetdb" ? "From FleetDB" : "Default"}
              </span>
            </div>
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={!hasChanges || isSaving || isCached}
              onClick={handleSave}
              data-testid="save-button"
            >
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      </div>

      {/* Agent Backend Overrides */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Agent Backend Overrides</h3>
        </div>
        <div className={styles.panelContent}>
          {agentsWithOverrides.length > 0 ? (
            <table
              className={styles.agentTable}
              data-testid="agent-overrides-table"
            >
              <thead>
                <tr>
                  <th>Worktree</th>
                  <th>Role</th>
                  <th>Backend</th>
                </tr>
              </thead>
              <tbody>
                {agentsWithOverrides.map((agent) => (
                  <tr key={agent.worktree}>
                    <td>{agent.worktree}</td>
                    <td>{agent.role}</td>
                    <td>{agent.backend}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p
              className={styles.emptyMessage}
              data-testid="no-overrides-message"
            >
              No per-agent overrides configured.
            </p>
          )}
        </div>
      </div>

      {/* FleetDB Redis */}
      <div className={styles.panel} data-testid="fleetdb-redis-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>FleetDB Redis</h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.checkboxLabel}>
              <input
                type="checkbox"
                checked={redisForm.enabled}
                onChange={(e) =>
                  setRedisForm((current) => ({
                    ...current,
                    enabled: e.target.checked,
                  }))
                }
                data-testid="redis-enabled-checkbox"
              />
              Use external Redis for FleetDB
            </label>
            <p className={styles.description}>
              Stores workspace, task, repo, and agent state in a managed Redis
              instance instead of local embedded Redis.
            </p>
          </div>
          {redisForm.enabled && (
            <>
              <div className={styles.formGroup}>
                <label className={styles.label} htmlFor="redis-url-input">
                  Redis URL
                </label>
                <p className={styles.description}>
                  Paste a redis://, rediss://, or redis-cli -u command. The
                  saved password is never returned to the browser.
                </p>
                <input
                  id="redis-url-input"
                  type="password"
                  className={styles.input}
                  value={redisForm.url}
                  onChange={(e) => handleRedisUrlChange(e.target.value)}
                  placeholder="redis://default:password@host:6379"
                  data-testid="redis-url-input"
                />
              </div>
              <div className={styles.fieldGrid}>
                <div className={styles.formGroup}>
                  <label className={styles.label} htmlFor="redis-addr-input">
                    Address
                  </label>
                  <input
                    id="redis-addr-input"
                    type="text"
                    className={styles.input}
                    value={redisForm.addr}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        addr: e.target.value,
                      }))
                    }
                    placeholder="host:6379"
                    data-testid="redis-addr-input"
                  />
                </div>
                <div className={styles.formGroup}>
                  <label className={styles.label} htmlFor="redis-db-input">
                    Database index
                  </label>
                  <input
                    id="redis-db-input"
                    type="number"
                    min={0}
                    className={styles.input}
                    value={redisForm.db}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        db: e.target.value,
                      }))
                    }
                    data-testid="redis-db-input"
                  />
                </div>
                <div className={styles.formGroup}>
                  <label
                    className={styles.label}
                    htmlFor="redis-password-input"
                  >
                    Password
                  </label>
                  <input
                    id="redis-password-input"
                    type="password"
                    className={styles.input}
                    value={redisForm.password}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        password: e.target.value,
                      }))
                    }
                    placeholder={
                      redisSettings?.password_set
                        ? "Saved password unchanged"
                        : "Optional"
                    }
                    data-testid="redis-password-input"
                  />
                </div>
              </div>
              <div className={styles.formGroup}>
                <label className={styles.checkboxLabel}>
                  <input
                    type="checkbox"
                    checked={redisForm.tls}
                    onChange={(e) =>
                      setRedisForm((current) => ({
                        ...current,
                        tls: e.target.checked,
                      }))
                    }
                    data-testid="redis-tls-checkbox"
                  />
                  Use TLS
                </label>
              </div>
            </>
          )}
          {redisSettings?.enabled && (
            <p className={styles.description} data-testid="redis-current">
              Current: {redisSettings.addr || "not configured"} | database{" "}
              {redisSettings.db} | {redisSettings.tls ? "TLS" : "no TLS"} |{" "}
              {redisSettings.password_set ? "password saved" : "no password"}
            </p>
          )}
          {localSettingsError && (
            <p className={styles.errorText} data-testid="redis-settings-error">
              {localSettingsError}
            </p>
          )}
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={isSavingLocalSettings}
              onClick={handleRedisSave}
              data-testid="redis-save-button"
            >
              {isSavingLocalSettings ? "Saving..." : "Save Redis Settings"}
            </button>
            <p className={styles.restartNote}>
              Restart the local runtime after changing Redis settings.
            </p>
          </div>
        </div>
      </div>

      {/* Terminal Font */}
      <div className={styles.panel} data-testid="terminal-font-panel">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Terminal Font</h3>
        </div>
        <div className={styles.panelContent}>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="font-family-select">
              Font Family
            </label>
            <p className={styles.description}>
              The font used in terminal views.
            </p>
            <select
              id="font-family-select"
              className={styles.select}
              value={fontSelectValue}
              onChange={(e) => {
                const val = e.target.value;
                if (val === CUSTOM_FONT_SENTINEL) {
                  setCustomFontMode(true);
                } else {
                  setCustomFontMode(false);
                  setFontFamily(val);
                }
              }}
              data-testid="font-family-select"
            >
              {FONT_FAMILY_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            {showCustomFontInput && (
              <input
                type="text"
                className={styles.input}
                value={isPresetFont ? "" : fontFamily}
                onChange={(e) => setFontFamily(e.target.value)}
                onBlur={(e) => {
                  if (!e.target.value.trim()) {
                    setCustomFontMode(false);
                    setFontFamily(DEFAULT_FONT_FAMILY);
                  }
                }}
                placeholder='e.g. "Fira Code", monospace'
                data-testid="font-family-custom-input"
              />
            )}
          </div>
          <div className={styles.formGroup}>
            <label className={styles.label} htmlFor="font-size-select">
              Font Size
            </label>
            <p className={styles.description}>Terminal text size in pixels.</p>
            <select
              id="font-size-select"
              className={styles.select}
              value={fontSize}
              onChange={(e) => setFontSize(Number(e.target.value))}
              data-testid="font-size-select"
            >
              {FONT_SIZE_OPTIONS.map((size) => (
                <option key={size} value={size}>
                  {size}px
                </option>
              ))}
            </select>
          </div>
        </div>
      </div>

      {/* Observability Navigation */}
      {onNavigate && (
        <div className={styles.panel} data-testid="observability-nav-panel">
          <div className={styles.panelHeader}>
            <h3 className={styles.panelTitle}>Observability</h3>
          </div>
          <div className={styles.panelContent}>
            <p className={styles.description}>
              View system metrics and agent activity.
            </p>
            <button
              type="button"
              className={styles.navButton}
              onClick={() => onNavigate("observability")}
              data-testid="observability-nav-button"
            >
              Open Observability
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
