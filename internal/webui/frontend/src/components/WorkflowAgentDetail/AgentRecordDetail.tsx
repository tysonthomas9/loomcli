import { useCallback, useEffect, useState } from "react";

import type { AgentRecordSummary, TriggerBinding } from "@/api";
import { useToast } from "@/hooks/ui/useToast";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { bindingCadenceLabel, bindingKindLabel } from "@/utils/bindingDisplay";

import styles from "./WorkflowAgentDetail.module.css";

function triggerCountLabel(count: number): string {
  if (count === 0) return "No trigger bindings";
  return `${count} trigger ${count === 1 ? "binding" : "bindings"}`;
}

export function AgentRecordHeader({
  record,
  bindings,
}: {
  record: AgentRecordSummary;
  bindings: TriggerBinding[];
}): JSX.Element {
  const avatarBg = getAvatarColor(record.id);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#171717";
  return (
    <div className={styles.header} data-testid="agent-record-header">
      <span
        className={styles.avatar}
        style={{ backgroundColor: avatarBg, color: avatarFg }}
        aria-hidden="true"
      >
        {getCompactAvatarInitials(record.name || record.id)}
      </span>
      <div className={styles.headText}>
        <h1 className={styles.title}>{record.name || record.id}</h1>
        <p className={styles.sub}>
          <span>Autonomous agent</span>
          <span aria-hidden="true">·</span>
          <span>{triggerCountLabel(bindings.length)}</span>
        </p>
      </div>
      <span
        className={styles.statusPill}
        data-enabled={record.enabled}
        data-state={record.enabled ? "idle" : "off"}
        data-testid="agent-record-status-pill"
      >
        {record.enabled ? "Enabled" : "Disabled"}
      </span>
    </div>
  );
}

export function AgentRecordActionBar({
  record,
  onSetEnabled,
}: {
  record: AgentRecordSummary;
  onSetEnabled: (agentId: string, enabled: boolean) => Promise<void>;
}): JSX.Element {
  const { showToast } = useToast();
  const [busy, setBusy] = useState(false);
  const handleToggle = useCallback(async () => {
    setBusy(true);
    try {
      await onSetEnabled(record.id, !record.enabled);
      showToast(record.enabled ? "Agent disabled" : "Agent enabled", {
        type: "success",
      });
    } catch (err) {
      showToast(`Failed to update: ${(err as Error).message}`, {
        type: "error",
      });
    } finally {
      setBusy(false);
    }
  }, [onSetEnabled, record.enabled, record.id, showToast]);

  return (
    <div className={styles.buttonBar}>
      <button
        type="button"
        className={styles.btn}
        onClick={() => void handleToggle()}
        disabled={busy}
        data-testid="agent-record-toggle-enabled"
      >
        {record.enabled ? "Disable" : "Enable"}
      </button>
    </div>
  );
}

export function AgentRecordInfoPane({
  record,
  bindings,
  onSelectBinding,
  onDelete,
  onDeleted,
}: {
  record: AgentRecordSummary;
  bindings: TriggerBinding[];
  onSelectBinding: (bindingId: string) => void;
  onDelete: (agentId: string) => Promise<void>;
  onDeleted: () => void;
}): JSX.Element {
  const { showToast } = useToast();
  const [busy, setBusy] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setBusy(false);
    setConfirmingDelete(false);
    setError(null);
  }, [record.id]);

  const handleDelete = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      await onDelete(record.id);
      showToast(`Archived ${record.name || record.id}`, { type: "success" });
      onDeleted();
    } catch (err) {
      setError((err as Error).message || "Failed to archive agent");
      setBusy(false);
    }
  }, [onDelete, onDeleted, record.id, record.name, showToast]);

  return (
    <div className={styles.scroll}>
      <section className={styles.card} data-testid="agent-record-info">
        <h2 className={styles.cardLabel}>Agent identity</h2>
        <dl className={styles.configGrid}>
          <div>
            <dt>Name</dt>
            <dd>{record.name || "—"}</dd>
          </div>
          <div>
            <dt>Record ID</dt>
            <dd>
              <code>{record.id}</code>
            </dd>
          </div>
          <div>
            <dt>Role</dt>
            <dd>{record.behavior?.role_name || "—"}</dd>
          </div>
          <div>
            <dt>State</dt>
            <dd>{record.enabled ? "Enabled" : "Disabled"}</dd>
          </div>
        </dl>
      </section>

      <section className={styles.card} data-testid="agent-record-trigger-list">
        <h2 className={styles.cardLabel}>Trigger bindings</h2>
        {bindings.length === 0 ? (
          <p className={styles.emptyText}>
            No trigger bindings are configured. This record cannot run until a
            trigger is attached.
          </p>
        ) : (
          <>
            <p className={styles.emptyText}>
              Choose a trigger to inspect or edit its trigger-specific
              configuration.
            </p>
            <ul className={styles.recordBindingList}>
              {bindings.map((binding) => (
                <li key={binding.binding_id}>
                  <button
                    type="button"
                    className={styles.recordBindingRow}
                    onClick={() => onSelectBinding(binding.binding_id)}
                    data-testid={`agent-record-trigger-${binding.binding_id}`}
                  >
                    <span className={styles.recordBindingName}>
                      {binding.name?.trim() || binding.binding_id}
                    </span>
                    <span className={styles.recordBindingMeta}>
                      {bindingKindLabel(binding)} ·{" "}
                      {bindingCadenceLabel(binding) || binding.binding_id}
                    </span>
                    <code className={styles.recordBindingId}>
                      {binding.binding_id}
                    </code>
                  </button>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      {bindings.length === 0 ? (
        <section className={styles.card} data-testid="agent-record-recovery">
          <h2 className={styles.cardLabel}>Recovery</h2>
          <p className={styles.emptyText}>
            This unconfigured record can be archived safely. Its historical runs
            remain attributable to the durable record ID.
          </p>
          {error ? (
            <div className={styles.errorText} role="alert">
              {error}
            </div>
          ) : null}
          <div className={styles.dangerZone}>
            {confirmingDelete ? (
              <div
                className={styles.confirmRow}
                data-testid="agent-record-archive-confirm"
              >
                <span className={styles.confirmText}>
                  Archive this unconfigured agent record?
                </span>
                <div className={styles.confirmActions}>
                  <button
                    type="button"
                    className={styles.btnDanger}
                    onClick={() => void handleDelete()}
                    disabled={busy}
                    data-testid="agent-record-archive-confirm-yes"
                  >
                    Archive
                  </button>
                  <button
                    type="button"
                    className={styles.btn}
                    onClick={() => setConfirmingDelete(false)}
                    disabled={busy}
                    data-testid="agent-record-archive-cancel"
                  >
                    Cancel
                  </button>
                </div>
              </div>
            ) : (
              <button
                type="button"
                className={styles.btnDangerOutline}
                onClick={() => setConfirmingDelete(true)}
                disabled={busy}
                data-testid="agent-record-archive"
              >
                Archive agent
              </button>
            )}
          </div>
        </section>
      ) : null}
    </div>
  );
}
