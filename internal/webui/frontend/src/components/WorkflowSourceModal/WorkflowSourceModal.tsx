/**
 * WorkflowSourceModal — Phase B workflow-plane management surface.
 *
 * Opened for a builtin workflow (e.g. bug-fix-agent / review-loop-agent), it
 * loads the TS source via GET /workflows/{name}/source into an editor and lets
 * the user rebuild it into a new driver version via POST .../versions. It can
 * also list versions and approve/activate them.
 *
 * HONESTY: a rebuild runs the flue toolchain on the serve host and can fail. We
 * surface `build_diagnostics` verbatim and only claim success when the response
 * says the version built AND activated. Because HTTP-built versions are stamped
 * UNTRUSTED, we also tell the user the version must be approved before it runs —
 * we never imply a freshly built version is already live.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { useWorkflowSource } from "@/hooks/workflows/useWorkflowSource";
import { useToast } from "@/hooks/ui";
import type {
  CreateWorkflowVersionResult,
  DriverVersion,
} from "@/api/workflows";
import { ApiError, apiErrorMessage } from "@/types/common";

import styles from "./WorkflowSourceModal.module.css";

export interface WorkflowSourceModalProps {
  isOpen: boolean;
  workspaceId: string;
  workflowName: string;
  onClose: () => void;
}

export function WorkflowSourceModal({
  isOpen,
  workspaceId,
  workflowName,
  onClose,
}: WorkflowSourceModalProps): JSX.Element | null {
  const {
    getSource,
    listVersions,
    saveSource,
    approveVersion,
    activateVersion,
  } = useWorkflowSource(workspaceId);
  const { showToast } = useToast();

  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [files, setFiles] = useState<Record<string, string>>({});
  const [entrypoint, setEntrypoint] = useState("");
  const [selectedFile, setSelectedFile] = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveResult, setSaveResult] =
    useState<CreateWorkflowVersionResult | null>(null);

  const [versions, setVersions] = useState<DriverVersion[]>([]);
  const [activeVersionId, setActiveVersionId] = useState("");
  const [versionsError, setVersionsError] = useState<string | null>(null);
  const [actioningId, setActioningId] = useState<string | null>(null);

  const fileList = useMemo(() => Object.keys(files).sort(), [files]);

  const refreshVersions = useCallback(async () => {
    setVersionsError(null);
    try {
      const res = await listVersions(workflowName);
      setVersions(res.versions ?? []);
      setActiveVersionId(res.active_version_id ?? "");
    } catch (err) {
      // A workflow with no driver built yet is a 404 — that is "no versions",
      // not an error to alarm the user with.
      if (err instanceof ApiError && err.status === 404) {
        setVersions([]);
        setActiveVersionId("");
        return;
      }
      setVersionsError(apiErrorMessage(err, "Failed to load versions"));
    }
  }, [listVersions, workflowName]);

  // Load source + versions each time the modal opens (or the target changes).
  useEffect(() => {
    if (!isOpen || !workflowName) return;
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    setSaveError(null);
    setSaveResult(null);
    getSource(workflowName)
      .then((res) => {
        if (cancelled) return;
        setFiles(res.files ?? {});
        setEntrypoint(res.entrypoint ?? "");
        const keys = Object.keys(res.files ?? {});
        setSelectedFile(
          res.entrypoint && keys.includes(res.entrypoint)
            ? res.entrypoint
            : (keys[0] ?? ""),
        );
      })
      .catch((err) => {
        if (cancelled) return;
        setFiles({});
        setEntrypoint("");
        setSelectedFile("");
        if (err instanceof ApiError && err.status === 404) {
          setLoadError(
            "This workflow has no editable source. Only builtin workflows expose source; custom driver versions persist a compiled bundle, not source text.",
          );
        } else {
          setLoadError(apiErrorMessage(err, "Failed to load workflow source"));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    void refreshVersions();
    return () => {
      cancelled = true;
    };
  }, [isOpen, workflowName, getSource, refreshVersions]);

  const setFileContent = useCallback((path: string, content: string) => {
    setFiles((prev) => ({ ...prev, [path]: content }));
  }, []);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    setSaveResult(null);
    try {
      const result = await saveSource(workflowName, {
        files,
        entrypoint,
        activate: true,
      });
      setSaveResult(result);
      // HONESTY: only claim success when the build activated. A non-activated
      // build is a warning, never a success toast.
      if (result.activated) {
        showToast(`${workflowName} built and activated`, { type: "success" });
      } else {
        showToast(`${workflowName} built but was not activated`, {
          type: "warning",
        });
      }
      await refreshVersions();
    } catch (err) {
      // A build failure surfaces here (ApiError 400) with the redacted flue
      // diagnostics in its message. Never a success toast.
      setSaveError(apiErrorMessage(err, "Build failed"));
    } finally {
      setSaving(false);
    }
  }, [saveSource, workflowName, files, entrypoint, showToast, refreshVersions]);

  const runVersionAction = useCallback(
    async (versionId: string, action: "approve" | "activate") => {
      setActioningId(versionId);
      setVersionsError(null);
      try {
        if (action === "approve") {
          await approveVersion(workflowName, versionId);
          showToast("Version approved", { type: "success" });
        } else {
          await activateVersion(workflowName, versionId);
          showToast("Version activated", { type: "success" });
        }
        await refreshVersions();
      } catch (err) {
        setVersionsError(apiErrorMessage(err, `Failed to ${action} version`));
      } finally {
        setActioningId(null);
      }
    },
    [approveVersion, activateVersion, workflowName, showToast, refreshVersions],
  );

  const busy = saving || actioningId !== null;

  return (
    <AetherModal
      isOpen={isOpen}
      title={`Workflow source · ${workflowName}`}
      ariaLabel={`Source for workflow ${workflowName}`}
      onClose={onClose}
      overlayTestId="workflow-source-overlay"
      closeTestId="workflow-source-close"
      dialogClassName={aetherModalStyles.dialogWide}
      footer={
        <button
          type="button"
          className={aetherModalStyles.linkButton}
          onClick={onClose}
          disabled={busy}
        >
          Close
        </button>
      }
    >
      <div className={styles.form}>
        {loading ? (
          <div className={styles.loading}>Loading source…</div>
        ) : loadError ? (
          <div
            className={styles.error}
            role="alert"
            data-testid="workflow-source-load-error"
          >
            {loadError}
          </div>
        ) : (
          <section className={styles.section}>
            <h3 className={styles.sectionHeader}>Source</h3>
            <p className={styles.sectionHint}>
              Entrypoint:{" "}
              <span className={styles.entrypoint}>{entrypoint || "—"}</span>.
              Saving builds a new driver version with the flue toolchain on the
              serve host.
            </p>
            {fileList.length > 1 && (
              <div className={styles.field}>
                <label className={styles.label} htmlFor="workflow-source-file">
                  File
                </label>
                <select
                  id="workflow-source-file"
                  className={styles.select}
                  value={selectedFile}
                  onChange={(e) => setSelectedFile(e.target.value)}
                  disabled={busy}
                  data-testid="workflow-source-file"
                >
                  {fileList.map((path) => (
                    <option key={path} value={path}>
                      {path}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <div className={styles.field}>
              <label className={styles.label} htmlFor="workflow-source-editor">
                {selectedFile || "source"}
              </label>
              <textarea
                id="workflow-source-editor"
                className={styles.textarea}
                value={selectedFile ? (files[selectedFile] ?? "") : ""}
                onChange={(e) => setFileContent(selectedFile, e.target.value)}
                disabled={busy || !selectedFile}
                spellCheck={false}
                data-testid="workflow-source-editor"
              />
            </div>
            <div className={styles.actionRow}>
              <button
                type="button"
                className={styles.button}
                onClick={handleSave}
                disabled={busy || !selectedFile}
                data-testid="workflow-source-save"
              >
                {saving ? "Building…" : "Build & activate"}
              </button>
            </div>

            {saveResult && (
              <>
                <div
                  className={`${styles.outcome} ${
                    saveResult.activated
                      ? styles.outcomeSuccess
                      : styles.outcomeWarning
                  }`}
                  role="status"
                  data-testid="workflow-source-outcome"
                >
                  {saveResult.activated
                    ? `Built version ${saveResult.version.version} and pointed the workflow at it.`
                    : `Built version ${saveResult.version.version}, but it was NOT activated — the workflow still runs its previous version.`}
                  <p className={styles.caveat}>
                    Versions built here are untrusted until approved. Approve it
                    below before this workflow will actually run the new build.
                  </p>
                </div>
                <p className={styles.diagnosticsLabel}>Build diagnostics</p>
                <pre
                  className={styles.diagnostics}
                  data-testid="workflow-source-diagnostics"
                >
                  {saveResult.build_diagnostics?.trim() ||
                    "(build produced no diagnostics)"}
                </pre>
              </>
            )}
            {saveError && (
              <>
                <div
                  className={styles.error}
                  role="alert"
                  data-testid="workflow-source-save-error"
                >
                  Build failed — the workflow was not changed.
                </div>
                <p className={styles.diagnosticsLabel}>Build diagnostics</p>
                <pre
                  className={styles.diagnostics}
                  data-testid="workflow-source-error-diagnostics"
                >
                  {saveError}
                </pre>
              </>
            )}
          </section>
        )}

        {/* Versions */}
        {!loadError && (
          <section className={styles.section}>
            <h3 className={styles.sectionHeader}>Versions</h3>
            {versions.length === 0 ? (
              <p
                className={styles.emptyHint}
                data-testid="workflow-source-no-versions"
              >
                No driver versions built yet.
              </p>
            ) : (
              <div
                className={styles.versionList}
                data-testid="workflow-source-versions"
              >
                {versions.map((v) => {
                  const isActive = v.version_id === activeVersionId;
                  return (
                    <div key={v.version_id} className={styles.versionItem}>
                      <div className={styles.versionMeta}>
                        <span className={styles.versionName}>
                          Version {v.version}
                        </span>
                        <span className={styles.versionSub}>
                          {v.version_id}
                        </span>
                      </div>
                      {isActive ? (
                        <span
                          className={`${styles.badge} ${styles.badgeActive}`}
                        >
                          Active
                        </span>
                      ) : v.validation_status === "failed" ? (
                        <span
                          className={`${styles.badge} ${styles.badgeFailed}`}
                        >
                          Failed
                        </span>
                      ) : (
                        <span
                          className={`${styles.badge} ${styles.badgePassed}`}
                        >
                          {v.validation_status}
                        </span>
                      )}
                      <button
                        type="button"
                        className={styles.secondaryButton}
                        onClick={() =>
                          runVersionAction(v.version_id, "approve")
                        }
                        disabled={busy}
                        data-testid={`workflow-version-approve-${v.version_id}`}
                      >
                        {actioningId === v.version_id ? "…" : "Approve"}
                      </button>
                      <button
                        type="button"
                        className={styles.secondaryButton}
                        onClick={() =>
                          runVersionAction(v.version_id, "activate")
                        }
                        disabled={busy || isActive}
                        data-testid={`workflow-version-activate-${v.version_id}`}
                      >
                        {isActive ? "Active" : "Activate"}
                      </button>
                    </div>
                  );
                })}
              </div>
            )}
            {versionsError && (
              <div
                className={styles.error}
                role="alert"
                data-testid="workflow-source-versions-error"
              >
                {versionsError}
              </div>
            )}
          </section>
        )}
      </div>
    </AetherModal>
  );
}
