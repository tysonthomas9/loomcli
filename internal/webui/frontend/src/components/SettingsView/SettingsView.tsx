/**
 * SettingsView component.
 * Displays project backend configuration with a dropdown and per-agent overrides table.
 */

import { useState } from 'react';

import { ErrorDisplay } from '@/components/ErrorDisplay';
import { LoadingSkeleton } from '@/components/LoadingSkeleton';
import { useBackendConfig } from '@/hooks/useBackendConfig';
import { useToast } from '@/hooks/useToast';

import styles from './SettingsView.module.css';

export interface SettingsViewProps {
  className?: string;
}

export function SettingsView({ className }: SettingsViewProps): JSX.Element {
  const { config, isLoading, error, isSaving, updateBackend, refetch } = useBackendConfig();
  const { showToast } = useToast();
  const [selectedBackend, setSelectedBackend] = useState<string | null>(null);

  const rootClassName = [styles.settingsView, className].filter(Boolean).join(' ');

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
  const agentsWithOverrides = config.agents.filter((a) => a.backend !== '');

  const handleSelectChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setSelectedBackend(e.target.value);
  };

  const handleSave = async () => {
    if (!hasChanges || isSaving) return;

    const ok = await updateBackend(currentValue);
    if (ok) {
      setSelectedBackend(null);
      showToast('Backend updated successfully', { type: 'success' });
      window.dispatchEvent(new CustomEvent('terminal-backend-changed'));
    } else {
      showToast('Failed to update backend', { type: 'error' });
    }
  };

  return (
    <div className={rootClassName} data-testid="settings-view">
      <h2 className={styles.pageTitle}>Settings</h2>

      {/* Project Default Backend */}
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Project Default Backend</h3>
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
                {config.source === 'project' ? 'From project loom.yaml' : 'Default'}
              </span>
            </div>
          </div>
          <div className={styles.formGroup}>
            <button
              type="button"
              className={styles.saveButton}
              disabled={!hasChanges || isSaving}
              onClick={handleSave}
              data-testid="save-button"
            >
              {isSaving ? 'Saving...' : 'Save'}
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
            <table className={styles.agentTable} data-testid="agent-overrides-table">
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
            <p className={styles.emptyMessage} data-testid="no-overrides-message">
              No per-agent overrides configured.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
