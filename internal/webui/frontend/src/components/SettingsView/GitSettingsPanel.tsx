/**
 * Git section of workspace Settings (D29): delivery mode and the two lead
 * permissions. Values are read from and saved to the server on change.
 */
import { useGitSettings, type GitSettings } from "@/hooks/workspace";

import styles from "./SettingsView.module.css";

export interface GitSettingsPanelProps {
  workspaceId: string;
  categoryHidden?: true | undefined;
  searchHidden?: true | undefined;
}

export function GitSettingsPanel({
  workspaceId,
  categoryHidden,
  searchHidden,
}: GitSettingsPanelProps): JSX.Element {
  const { settings, error, warning, isSaving, update } =
    useGitSettings(workspaceId);
  const disabled = !settings || isSaving;

  return (
    <div
      className={styles.panel}
      id="settings-git"
      data-settings-panel="git"
      data-testid="git-settings-panel"
      data-category-hidden={categoryHidden}
      data-search-hidden={searchHidden}
    >
      <div className={styles.panelHeader}>
        <h3 className={styles.panelTitle}>Git</h3>
      </div>
      <div className={styles.panelContent}>
        {error && (
          <p className={styles.errorText} role="alert">
            {error}
          </p>
        )}
        <div className={styles.formGroup}>
          <label className={styles.label} htmlFor="git-delivery-mode">
            Delivery mode
          </label>
          <p className={styles.description}>
            Stacked PRs base each task&apos;s PR on the one below. PR per task
            opens each task&apos;s own PR to trunk. Switching leaves open PRs as
            they are.
          </p>
          <select
            id="git-delivery-mode"
            className={styles.select}
            value={settings?.delivery_mode ?? ""}
            disabled={disabled}
            onChange={(event) =>
              void update({
                delivery_mode: event.target
                  .value as GitSettings["delivery_mode"],
              })
            }
            data-testid="git-delivery-mode"
          >
            {!settings && <option value="">Loading…</option>}
            <option value="stack">Stacked PRs</option>
            <option value="trunk">PR per task</option>
          </select>
        </div>
        <div className={styles.formGroup}>
          <label className={styles.label} htmlFor="git-lead-may-approve">
            Lead may approve
          </label>
          <p className={styles.description}>
            Lets the lead approve a task and open its PR. Only you can change
            this.
          </p>
          <select
            id="git-lead-may-approve"
            className={styles.select}
            value={
              settings ? (settings.lead_may_approve_publish ? "on" : "off") : ""
            }
            disabled={disabled}
            onChange={(event) =>
              void update({
                lead_may_approve_publish: event.target.value === "on",
              })
            }
            data-testid="git-lead-may-approve"
          >
            {!settings && <option value="">Loading…</option>}
            <option value="on">On</option>
            <option value="off">Off</option>
          </select>
        </div>
        <div className={styles.formGroup}>
          <label className={styles.label} htmlFor="git-lead-may-merge">
            Lead may merge
          </label>
          <p className={styles.description}>
            When green, the lead merges PRs whose checks and reviews pass,
            bottom-up. Only you can change this.
          </p>
          <select
            id="git-lead-may-merge"
            className={styles.select}
            value={settings?.lead_may_merge ?? ""}
            disabled={disabled}
            onChange={(event) =>
              void update({
                lead_may_merge: event.target
                  .value as GitSettings["lead_may_merge"],
              })
            }
            data-testid="git-lead-may-merge"
          >
            {!settings && <option value="">Loading…</option>}
            <option value="off">Off</option>
            <option value="when_green">When green</option>
          </select>
          {warning && settings?.lead_may_merge === "when_green" && (
            <p className={styles.description} data-testid="git-merge-warning">
              Warning: {warning}
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
