/**
 * SettingsView component.
 * Displays project backend configuration with a dropdown and per-agent overrides table.
 */

import { useState } from "react";

import { ErrorDisplay } from "@/components/ErrorDisplay";
import { LoadingSkeleton } from "@/components/LoadingSkeleton";
import type { ViewMode } from "@/components/ViewSwitcher";
import { useBackendConfig } from "@/hooks/workspace";
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

export function SettingsView({
  className,
  onNavigate,
}: SettingsViewProps): JSX.Element {
  const {
    config,
    isLoading,
    error,
    isSaving,
    isCached,
    updateBackend,
    refetch,
  } = useBackendConfig();
  const { showToast } = useToast();
  const [selectedBackend, setSelectedBackend] = useState<string | null>(null);

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
                {config.source === "project"
                  ? "From project loom.yaml"
                  : "Default"}
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
