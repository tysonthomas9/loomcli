import { useEffect, useMemo, useState, type FormEvent } from "react";

import type { AgentServiceDTO } from "@/api/agentServices";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useAgentServiceMutations } from "@/hooks/workspace";
import {
  agentServiceMutationError,
  scheduleError,
  SCHEDULE_PRESETS,
} from "@/utils/agentServiceForm";

import styles from "./AgentServiceDetail.module.css";

export interface AgentServiceSettingsProps {
  workspaceId: string;
  service: AgentServiceDTO;
  onChange: (service: AgentServiceDTO) => void;
  onRemoved?: () => void;
}

export function AgentServiceSettings({
  workspaceId,
  service,
  onChange,
  onRemoved,
}: AgentServiceSettingsProps): JSX.Element {
  const mutations = useAgentServiceMutations(workspaceId);
  const cronBinding = useMemo(
    () => service.bindings.find((binding) => binding.sourceKind === "cron"),
    [service.bindings],
  );
  const [name, setName] = useState(service.name);
  const [schedule, setSchedule] = useState(cronBinding?.schedule ?? "");
  const [scheduleTouched, setScheduleTouched] = useState(false);
  const [busy, setBusy] = useState<
    "name" | "state" | "schedule" | "remove" | null
  >(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setName(service.name);
    setSchedule(cronBinding?.schedule ?? "");
    setScheduleTouched(false);
    setError(null);
  }, [cronBinding?.schedule, service.id, service.name]);

  const saveName = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed || trimmed === service.name.trim() || busy) return;
    setBusy("name");
    setError(null);
    try {
      onChange(await mutations.patch(service.id, { name: trimmed }));
    } catch (cause) {
      setError(
        agentServiceMutationError(
          cause,
          "The display name could not be saved.",
        ),
      );
    } finally {
      setBusy(null);
    }
  };

  const toggleState = async (): Promise<void> => {
    if (busy) return;
    setBusy("state");
    setError(null);
    try {
      onChange(
        await mutations.patch(service.id, {
          desiredState: service.enabled ? "stopped" : "running",
        }),
      );
    } catch (cause) {
      setError(
        agentServiceMutationError(
          cause,
          "The agent state could not be changed.",
        ),
      );
    } finally {
      setBusy(null);
    }
  };

  const saveSchedule = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    setScheduleTouched(true);
    const invalid = scheduleError(schedule);
    if (
      invalid ||
      busy ||
      schedule.trim() === (cronBinding?.schedule ?? "").trim()
    )
      return;
    setBusy("schedule");
    setError(null);
    try {
      onChange(
        await mutations.patch(service.id, {
          binding: { schedule: schedule.trim() },
        }),
      );
    } catch (cause) {
      setError(
        agentServiceMutationError(cause, "The schedule could not be saved."),
      );
    } finally {
      setBusy(null);
    }
  };

  const remove = async (): Promise<void> => {
    if (busy) return;
    setBusy("remove");
    setError(null);
    try {
      await mutations.remove(service.id);
      setConfirmRemove(false);
      onRemoved?.();
    } catch (cause) {
      setConfirmRemove(false);
      setError(
        agentServiceMutationError(cause, "The agent could not be removed."),
      );
    } finally {
      setBusy(null);
    }
  };

  const invalidSchedule = scheduleError(schedule);
  const nameDirty = Boolean(name.trim()) && name.trim() !== service.name.trim();
  const scheduleDirty =
    invalidSchedule === null &&
    schedule.trim() !== (cronBinding?.schedule ?? "").trim();

  return (
    <section className={styles.card} data-testid="agent-service-settings">
      <div className={styles.cardHeadingRow}>
        <h2 className={styles.cardTitle}>Settings</h2>
        <button
          type="button"
          className={styles.secondaryButton}
          onClick={() => void toggleState()}
          disabled={busy !== null}
        >
          {busy === "state"
            ? "Saving…"
            : service.enabled
              ? "Disable agent"
              : "Enable agent"}
        </button>
      </div>

      <form className={styles.settingsForm} onSubmit={saveName}>
        <label htmlFor="agent-service-detail-name">Display name</label>
        <div className={styles.settingsRow}>
          <input
            id="agent-service-detail-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={busy !== null}
          />
          <button type="submit" disabled={!nameDirty || busy !== null}>
            {busy === "name" ? "Saving…" : "Save name"}
          </button>
        </div>
      </form>

      <form className={styles.settingsForm} onSubmit={saveSchedule}>
        <label htmlFor="agent-service-detail-schedule">Schedule</label>
        <div className={styles.settingsRow}>
          <input
            id="agent-service-detail-schedule"
            className={styles.monoInput}
            value={schedule}
            onChange={(event) => {
              setSchedule(event.target.value);
              setScheduleTouched(true);
            }}
            onBlur={() => setScheduleTouched(true)}
            aria-invalid={scheduleTouched && invalidSchedule !== null}
            disabled={busy !== null}
          />
          <button type="submit" disabled={!scheduleDirty || busy !== null}>
            {busy === "schedule" ? "Saving…" : "Save schedule"}
          </button>
        </div>
        <div className={styles.schedulePresets} aria-label="Schedule presets">
          {SCHEDULE_PRESETS.map((preset) => (
            <button
              key={preset}
              type="button"
              onClick={() => {
                setSchedule(preset);
                setScheduleTouched(true);
              }}
              disabled={busy !== null}
            >
              {preset}
            </button>
          ))}
        </div>
        {scheduleTouched && invalidSchedule ? (
          <p className={styles.errorText} role="alert">
            {invalidSchedule}
          </p>
        ) : null}
      </form>

      {error ? (
        <p className={styles.errorText} role="alert">
          {error}
        </p>
      ) : null}

      <div className={styles.dangerRow}>
        <div>
          <strong>Remove autonomous agent</strong>
          <p>Removes this instance and its trigger binding.</p>
        </div>
        <button
          type="button"
          className={styles.dangerButton}
          onClick={() => setConfirmRemove(true)}
          disabled={busy !== null}
        >
          Remove agent
        </button>
      </div>

      <ConfirmDialog
        isOpen={confirmRemove}
        title="Remove autonomous agent"
        message={
          <span>
            Remove <strong>{service.id}</strong>? This cannot be undone.
          </span>
        }
        confirmLabel={busy === "remove" ? "Removing…" : "Remove"}
        variant="danger"
        onConfirm={() => void remove()}
        onCancel={() => setConfirmRemove(false)}
      />
    </section>
  );
}
