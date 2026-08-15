import { useCallback, useEffect, useState } from "react";

import {
  useRolePrompt,
  type RolePromptDTO,
  type RoleSourceKind,
} from "@/hooks/workspace/useRolePrompt";
import { ApiError } from "@/types/common";

import styles from "./RolePromptCard.module.css";

export interface RolePromptCardProps {
  workspaceId: string;
  roleName: string;
}

const sourceLabels: Record<RoleSourceKind, string> = {
  builtinTemplate: "Built-in template — read-only",
  managed: "Managed — read-only",
  file: "File-backed prompt",
  inline: "Inline prompt",
  builtinSelector: "Built-in selector — editable override",
};

export function RolePromptCard({
  workspaceId,
  roleName,
}: RolePromptCardProps): JSX.Element {
  const [role, setRole] = useState<RolePromptDTO | null>(null);
  const [draft, setDraft] = useState("");
  const [baseline, setBaseline] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const { get: getRolePrompt, update: updateRolePromptBody } = useRolePrompt(
    workspaceId,
    roleName,
  );

  const load = useCallback(
    async (preserveDraft: boolean): Promise<void> => {
      setLoading(true);
      setError(null);
      try {
        const next = await getRolePrompt();
        setRole(next);
        setBaseline(next.sourceBody);
        if (!preserveDraft) setDraft(next.sourceBody);
        setStale(false);
      } catch (err) {
        setError(apiErrorMessage(err, "Prompt could not be loaded."));
      } finally {
        setLoading(false);
      }
    },
    [getRolePrompt],
  );

  useEffect(() => {
    setRole(null);
    setDraft("");
    setBaseline("");
    setStale(false);
    void load(false);
  }, [load]);

  const dirty = role?.editable === true && draft !== baseline;

  const save = async (): Promise<void> => {
    if (!role || !dirty || saving) return;
    setSaving(true);
    setError(null);
    setStale(false);
    try {
      const next = await updateRolePromptBody({
        prompt: draft,
        expectedRevision: role.revision,
      });
      setRole(next);
      setDraft(next.sourceBody);
      setBaseline(next.sourceBody);
    } catch (err) {
      if (apiErrorCode(err) === "stale_revision") {
        setStale(true);
      } else {
        setError(apiErrorMessage(err, "Prompt could not be saved."));
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className={styles.card} data-testid="role-prompt-card">
      <div className={styles.headingRow}>
        <div>
          <h2 className={styles.title}>Role prompt</h2>
          <p className={styles.roleName}>{roleName}</p>
        </div>
        {role ? (
          <span
            className={styles.sourceLabel}
            data-source-kind={role.sourceKind}
          >
            {sourceLabels[role.sourceKind]}
          </span>
        ) : null}
      </div>

      {loading && !role ? (
        <p className={styles.muted}>Loading prompt…</p>
      ) : null}

      {error ? (
        <p className={styles.error} role="alert">
          {error}
        </p>
      ) : null}

      {role?.sourceError ? (
        <p className={styles.error} role="alert">
          {role.sourceError}
        </p>
      ) : null}

      {role ? (
        <>
          {role.editable ? (
            <textarea
              className={styles.editor}
              data-testid="role-prompt-editor"
              aria-label={`Prompt for ${roleName}`}
              value={draft}
              spellCheck={false}
              onChange={(event) => setDraft(event.target.value)}
            />
          ) : (
            <pre className={styles.sourceBody} data-testid="role-prompt-source">
              {role.sourceBody}
            </pre>
          )}

          {stale ? (
            <div className={styles.stale} role="alert">
              <span>
                Prompt changed elsewhere — reload to pick up the latest.
              </span>
              <button type="button" onClick={() => void load(true)}>
                Reload latest revision
              </button>
            </div>
          ) : null}

          <p className={styles.note}>{role.activationNote}</p>

          {role.editable ? (
            <div className={styles.actions}>
              <button
                type="button"
                className={styles.saveButton}
                disabled={!dirty || saving}
                onClick={() => void save()}
              >
                {saving ? "Saving…" : "Save prompt"}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

function apiErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.message) return error.message;
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

function apiErrorCode(error: unknown): string | undefined {
  if (!(error instanceof ApiError) || !isRecord(error.body)) return undefined;
  if (typeof error.body.code === "string") return error.body.code;
  const nested = error.body.error;
  if (isRecord(nested) && typeof nested.code === "string") return nested.code;
  return undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
