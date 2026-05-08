/**
 * WorkspaceRepoWizard — fixed-path workspace+repo wizard for the
 * onboarding flow.
 *
 * Unlike CreateWorkspaceModal (three workspace types, optional repo
 * later), this wizard requires a repo source up front. Per the spec,
 * first-run users should never end up with an empty workspace they
 * can't run an agent against. CreateWorkspaceModal stays for advanced
 * flows elsewhere in the app.
 *
 * Two source choices:
 *   - Local repo path (sync — workspace + attached repo land
 *     immediately)
 *   - Git URL (async — clone job is started; the wizard polls for
 *     completion before closing)
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { createWorkspace, addWorkspaceRepos } from "@/hooks/api";
import type {
  CreateWorkspaceRequest,
  WorkspaceData,
} from "@/api/workspace";
import { useJobPolling } from "@/hooks/agents/useJobPolling";

import styles from "./WorkspaceRepoWizard.module.css";

export type RepoSource = "local" | "clone";

export type WizardMode =
  | { kind: "create-workspace" }
  | {
      kind: "add-repo";
      workspaceId: string;
      workspaceName: string;
    };

export interface WorkspaceRepoWizardProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (
    data: WorkspaceData,
    createdName: string,
    warnings?: string[],
  ) => void;
  /**
   * Mode controls whether the wizard creates a new workspace (default,
   * onboarding step 1 from no-workspace state) or attaches a repo to
   * an existing workspace (onboarding step 1 when the workspace is
   * present but has no repos).
   */
  mode?: WizardMode;
}

export function WorkspaceRepoWizard({
  isOpen,
  onClose,
  onSuccess,
  mode = { kind: "create-workspace" },
}: WorkspaceRepoWizardProps): JSX.Element | null {
  const isAddRepo = mode.kind === "add-repo";
  const initialName = isAddRepo ? mode.workspaceName : "";

  const [name, setName] = useState(initialName);
  const [source, setSource] = useState<RepoSource>("local");
  const [localPath, setLocalPath] = useState("");
  const [cloneUrl, setCloneUrl] = useState("");
  const [branch, setBranch] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  const job = useJobPolling(name, {
    onSuccess,
    onClose,
    onFinish: () => setIsSubmitting(false),
  });

  const nameRef = useRef<HTMLInputElement>(null);
  const pathRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!isOpen) return;
    setName(initialName);
    setSource("local");
    setLocalPath("");
    setCloneUrl("");
    setBranch("");
    setError("");
    setIsSubmitting(false);
    job.reset();
    // Focus the name field on open for create flow; the path field
    // for add-repo (the workspace name is locked).
    window.setTimeout(() => {
      if (isAddRepo) {
        pathRef.current?.focus();
      } else {
        nameRef.current?.focus();
      }
    }, 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- job.reset is stable
  }, [isOpen, initialName, isAddRepo]);

  const canSubmit =
    !isSubmitting &&
    !job.isPolling &&
    name.trim().length > 0 &&
    (source === "local"
      ? localPath.trim().length > 0
      : cloneUrl.trim().length > 0);

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setError("");
      setIsSubmitting(true);

      try {
        if (mode.kind === "add-repo") {
          // Existing-workspace path: attach a repo without going through
          // the workspace job machinery.
          const data = await addWorkspaceRepos(mode.workspaceId, {
            ...(source === "local"
              ? { repos: [localPath.trim()] }
              : { clone_urls: [cloneUrl.trim()] }),
            ...(branch.trim() ? { branch: branch.trim() } : {}),
          });
          setIsSubmitting(false);
          try {
            onSuccess(data, name.trim());
          } catch {
            // ignore
          }
          onClose();
          return;
        }

        const req: CreateWorkspaceRequest =
          source === "local"
            ? { name: name.trim(), type: "empty", repos: [localPath.trim()] }
            : {
                name: name.trim(),
                type: "clone",
                clone_urls: [cloneUrl.trim()],
                ...(branch.trim() ? { branch: branch.trim() } : {}),
              };

        const result = await createWorkspace(req);
        if (result.kind === "async") {
          job.startJob(result.jobId);
          return;
        }
        setIsSubmitting(false);
        try {
          onSuccess(result.data, req.name, result.warnings);
        } catch {
          // navigation errors must not block close
        }
        onClose();
      } catch (err: unknown) {
        setIsSubmitting(false);
        const message =
          err instanceof Error
            ? err.message
            : isAddRepo
              ? "Failed to add repo"
              : "Failed to create workspace";
        setError(message);
      }
    },
    [
      canSubmit,
      mode,
      name,
      source,
      localPath,
      cloneUrl,
      branch,
      onSuccess,
      onClose,
      job,
      isAddRepo,
    ],
  );

  if (!isOpen) return null;

  const polling = job.isPolling;

  return (
    <div
      className={styles.backdrop}
      role="dialog"
      aria-modal="true"
      aria-labelledby="workspace-wizard-heading"
      data-testid="workspace-repo-wizard"
      onClick={polling ? undefined : onClose}
    >
      <div
        className={styles.dialog}
        onClick={(e) => e.stopPropagation()}
        role="document"
      >
        <header>
          <h2 id="workspace-wizard-heading" className={styles.title}>
            {polling
              ? "Cloning repository…"
              : isAddRepo
                ? `Add a repo to ${mode.kind === "add-repo" ? mode.workspaceName : ""}`
                : "Create a workspace"}
          </h2>
          <p className={styles.subtitle}>
            {isAddRepo
              ? "Attach a local repo path or git URL to this workspace."
              : "A workspace ties together one or more repos and the agents that work in them."}
          </p>
        </header>

        {polling ? (
          <p className={styles.progress} data-testid="wizard-progress">
            Cloning repository — this can take a moment.
          </p>
        ) : (
          <form className={styles.form} onSubmit={handleSubmit}>
            {isAddRepo ? null : (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="wizard-name">
                  Workspace name
                </label>
                <input
                  ref={nameRef}
                  id="wizard-name"
                  type="text"
                  className={styles.input}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="my-project"
                  data-testid="wizard-name"
                />
              </div>
            )}

            <div className={styles.fieldGroup}>
              <span className={styles.label}>Repo source</span>
              <div className={styles.sourceRow} role="radiogroup" aria-label="Repo source">
                <button
                  type="button"
                  className={`${styles.sourceOption} ${source === "local" ? styles.active : ""}`}
                  onClick={() => setSource("local")}
                  role="radio"
                  aria-checked={source === "local"}
                  data-testid="wizard-source-local"
                >
                  Local path
                </button>
                <button
                  type="button"
                  className={`${styles.sourceOption} ${source === "clone" ? styles.active : ""}`}
                  onClick={() => setSource("clone")}
                  role="radio"
                  aria-checked={source === "clone"}
                  data-testid="wizard-source-clone"
                >
                  Git URL
                </button>
              </div>
            </div>

            {source === "local" ? (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="wizard-local-path">
                  Local repo path
                </label>
                <input
                  ref={pathRef}
                  id="wizard-local-path"
                  type="text"
                  className={styles.input}
                  value={localPath}
                  onChange={(e) => setLocalPath(e.target.value)}
                  placeholder="/Users/you/code/my-project"
                  data-testid="wizard-local-path"
                />
                <span className={styles.hint}>
                  Must be an absolute path to an existing git repository on this
                  machine.
                </span>
              </div>
            ) : (
              <>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="wizard-clone-url">
                    Git clone URL
                  </label>
                  <input
                    id="wizard-clone-url"
                    type="text"
                    className={styles.input}
                    value={cloneUrl}
                    onChange={(e) => setCloneUrl(e.target.value)}
                    placeholder="git@github.com:org/repo.git"
                    data-testid="wizard-clone-url"
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="wizard-branch">
                    Branch (optional)
                  </label>
                  <input
                    id="wizard-branch"
                    type="text"
                    className={styles.input}
                    value={branch}
                    onChange={(e) => setBranch(e.target.value)}
                    placeholder="main"
                    data-testid="wizard-branch"
                  />
                </div>
              </>
            )}

            {error ? (
              <p className={styles.error} role="alert" data-testid="wizard-error">
                {error}
              </p>
            ) : null}

            <div className={styles.actions}>
              <button
                type="button"
                className={styles.cancel}
                onClick={onClose}
                disabled={isSubmitting}
                data-testid="wizard-cancel"
              >
                Cancel
              </button>
              <button
                type="submit"
                className={styles.submit}
                disabled={!canSubmit}
                data-testid="wizard-submit"
              >
                {isSubmitting ? "Creating…" : "Create workspace"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
