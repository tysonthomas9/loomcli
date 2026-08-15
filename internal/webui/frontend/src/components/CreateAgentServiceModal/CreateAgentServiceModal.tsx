import { useEffect, useRef, useState, type FormEvent } from "react";

import type { AgentServiceDTO } from "@/api/agentServices";
import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import {
  useAgentServiceMutations,
  useInstantiableScriptedRoles,
} from "@/hooks/workspace";
import {
  agentServiceMutationError,
  SCHEDULE_PRESETS,
  scheduleError,
  serviceIDError,
} from "@/utils/agentServiceForm";

import styles from "./CreateAgentServiceModal.module.css";

export interface CreateAgentServiceModalProps {
  isOpen: boolean;
  workspaceId: string;
  onClose: () => void;
  onSuccess: (service: AgentServiceDTO) => void;
}

export function CreateAgentServiceModal({
  isOpen,
  workspaceId,
  onClose,
  onSuccess,
}: CreateAgentServiceModalProps): JSX.Element | null {
  const {
    roles,
    loading,
    error: rolesError,
  } = useInstantiableScriptedRoles(workspaceId, { enabled: isOpen });
  const mutations = useAgentServiceMutations(workspaceId);
  const [roleName, setRoleName] = useState("");
  const [id, setID] = useState("");
  const [name, setName] = useState("");
  const [schedule, setSchedule] = useState("");
  const [timezone, setTimezone] = useState("");
  const [idTouched, setIDTouched] = useState(false);
  const [scheduleTouched, setScheduleTouched] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const idRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!isOpen) return;
    setRoleName("");
    setID("");
    setName("");
    setSchedule("");
    setTimezone("");
    setIDTouched(false);
    setScheduleTouched(false);
    setSubmitting(false);
    setError(null);
    const timeout = window.setTimeout(() => idRef.current?.focus(), 0);
    return () => window.clearTimeout(timeout);
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen || roles.length === 0) return;
    setRoleName((current) =>
      roles.some((role) => role.roleName === current)
        ? current
        : (roles[0]?.roleName ?? ""),
    );
  }, [isOpen, roles]);

  const invalidID = serviceIDError(id);
  const invalidSchedule = scheduleError(schedule);
  const canSubmit =
    Boolean(workspaceId && roleName) &&
    invalidID === null &&
    invalidSchedule === null &&
    !submitting;

  const handleSubmit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    setIDTouched(true);
    setScheduleTouched(true);
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    const trimmedName = name.trim();
    const trimmedTimezone = timezone.trim();
    try {
      const service = await mutations.create({
        id: id.trim(),
        ...(trimmedName ? { name: trimmedName } : {}),
        role: roleName,
        binding: {
          schedule: schedule.trim(),
          ...(trimmedTimezone ? { timezone: trimmedTimezone } : {}),
        },
      });
      onSuccess(service);
      onClose();
    } catch (cause) {
      setError(
        agentServiceMutationError(
          cause,
          "The autonomous agent could not be created.",
        ),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AetherModal
      isOpen={isOpen}
      title="Add autonomous agent"
      ariaLabel="Add autonomous agent"
      onClose={onClose}
      overlayTestId="create-agent-service-overlay"
      closeTestId="create-agent-service-close"
      footer={
        <>
          <button
            type="button"
            className={aetherModalStyles.linkButton}
            onClick={onClose}
            disabled={submitting}
          >
            Cancel
          </button>
          <button
            type="submit"
            form="create-agent-service-form"
            className={aetherModalStyles.primaryButton}
            disabled={!canSubmit}
          >
            {submitting ? "Creating…" : "Add agent"}
          </button>
        </>
      }
    >
      <form
        id="create-agent-service-form"
        className={styles.form}
        onSubmit={handleSubmit}
      >
        <div className={styles.field}>
          <label htmlFor="agent-service-role">Scripted role</label>
          <select
            id="agent-service-role"
            value={roleName}
            onChange={(event) => setRoleName(event.target.value)}
            disabled={submitting || roles.length <= 1}
          >
            {roles.length === 0 ? (
              <option value="">
                {loading ? "Loading roles…" : "No roles"}
              </option>
            ) : (
              roles.map((role) => (
                <option key={role.roleName} value={role.roleName}>
                  {role.displayName}
                </option>
              ))
            )}
          </select>
          {roles.length === 1 ? (
            <p className={styles.hint}>
              The only cron-instantiable role today.
            </p>
          ) : null}
          {rolesError ? (
            <p className={styles.error} role="alert">
              Scripted roles could not be loaded: {rolesError.message}
            </p>
          ) : null}
        </div>

        <div className={styles.field}>
          <label htmlFor="agent-service-id">Instance ID</label>
          <input
            ref={idRef}
            id="agent-service-id"
            value={id}
            onChange={(event) => {
              setID(event.target.value);
              setIDTouched(true);
            }}
            onBlur={() => setIDTouched(true)}
            placeholder="scout-west"
            aria-invalid={idTouched && invalidID !== null}
            aria-describedby={
              idTouched && invalidID ? "agent-service-id-error" : undefined
            }
            disabled={submitting}
          />
          {idTouched && invalidID ? (
            <p
              id="agent-service-id-error"
              className={styles.error}
              role="alert"
            >
              {invalidID}
            </p>
          ) : null}
        </div>

        <div className={styles.field}>
          <label htmlFor="agent-service-name">Display name (optional)</label>
          <input
            id="agent-service-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={id.trim() || "Instance ID"}
            disabled={submitting}
          />
        </div>

        <div className={styles.field}>
          <label htmlFor="agent-service-schedule">Schedule</label>
          <input
            id="agent-service-schedule"
            className={styles.mono}
            value={schedule}
            onChange={(event) => {
              setSchedule(event.target.value);
              setScheduleTouched(true);
            }}
            onBlur={() => setScheduleTouched(true)}
            placeholder="0 9 * * 1-5"
            aria-invalid={scheduleTouched && invalidSchedule !== null}
            aria-describedby={
              scheduleTouched && invalidSchedule
                ? "agent-service-schedule-error"
                : undefined
            }
            disabled={submitting}
          />
          <div className={styles.presets} aria-label="Schedule presets">
            {SCHEDULE_PRESETS.map((preset) => (
              <button
                key={preset}
                type="button"
                onClick={() => {
                  setSchedule(preset);
                  setScheduleTouched(true);
                }}
                disabled={submitting}
              >
                {preset}
              </button>
            ))}
          </div>
          {scheduleTouched && invalidSchedule ? (
            <p
              id="agent-service-schedule-error"
              className={styles.error}
              role="alert"
            >
              {invalidSchedule}
            </p>
          ) : null}
        </div>

        <div className={styles.field}>
          <label htmlFor="agent-service-timezone">Timezone (optional)</label>
          <input
            id="agent-service-timezone"
            className={styles.mono}
            value={timezone}
            onChange={(event) => setTimezone(event.target.value)}
            placeholder="America/Los_Angeles"
            disabled={submitting}
          />
        </div>

        {error ? (
          <p className={styles.errorBox} role="alert">
            {error}
          </p>
        ) : null}
      </form>
    </AetherModal>
  );
}
