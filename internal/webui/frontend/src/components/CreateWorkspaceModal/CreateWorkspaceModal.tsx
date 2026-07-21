/**
 * CreateWorkspaceModal — clone, local-repo, or empty workspace creation dialog.
 * Renders via AetherModal portal above all other content.
 */

import {
  useState,
  useRef,
  useCallback,
  useEffect,
  type KeyboardEvent,
} from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { createWorkspace } from "@/hooks/api";
import type { CreateWorkspaceRequest, WorkspaceData } from "@/api/workspace";
import { useRegisterEscapeLayer, LAYER_MODAL, useJobPolling } from "@/hooks";
import { useFocusTrap, useFocusReturn } from "@/hooks/ui";
import styles from "./CreateWorkspaceModal.module.css";

export type WorkspaceType = "clone" | "local" | "empty";

export interface CreateWorkspaceModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (
    data: WorkspaceData,
    createdName: string,
    warnings?: string[],
  ) => void;
  initialValues?: {
    name?: string;
    cloneUrls?: string[];
    urlInput?: string;
  };
}

const WORKSPACE_NAME_RE = /^[A-Za-z0-9_-]+$/;
const LINE_SPLIT_RE = /\r?\n/;
const WORKSPACE_MODES: { id: WorkspaceType; label: string }[] = [
  { id: "clone", label: "Clone" },
  { id: "local", label: "Local repos" },
  { id: "empty", label: "Empty" },
];

function splitLineInput(value: string): string[] {
  return value
    .split(LINE_SPLIT_RE)
    .map((item) => item.trim())
    .filter(Boolean);
}

function appendUnique(existing: string[], incoming: string[]): string[] {
  const next = [...existing];
  for (const item of incoming) {
    if (!next.includes(item)) {
      next.push(item);
    }
  }
  return next;
}

export function CreateWorkspaceModal({
  isOpen,
  onClose,
  onSuccess,
  initialValues,
}: CreateWorkspaceModalProps): JSX.Element | null {
  const [name, setName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [workspaceType, setWorkspaceType] = useState<WorkspaceType>("clone");
  const [cloneUrls, setCloneUrls] = useState<string[]>([]);
  const [urlInput, setUrlInput] = useState("");
  const [localRepos, setLocalRepos] = useState<string[]>([]);
  const [pathInput, setPathInput] = useState("");

  const {
    isPolling,
    progress: jobProgress,
    elapsed: jobElapsed,
    error: jobError,
    startJob,
    reset: resetJob,
  } = useJobPolling(name, {
    onSuccess,
    onClose,
    onFinish: () => setIsSubmitting(false),
  });

  const dialogRef = useRef<HTMLDivElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }

    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    setName(initialValues?.name ?? "");
    setIsSubmitting(false);
    setError("");
    setWorkspaceType("clone");
    setCloneUrls(initialValues?.cloneUrls ?? []);
    setUrlInput(initialValues?.urlInput ?? "");
    setLocalRepos([]);
    setPathInput("");
    resetJob();
  }, [
    isOpen,
    initialValues?.name,
    initialValues?.cloneUrls,
    initialValues?.urlInput,
    resetJob,
  ]);

  useEffect(() => {
    if (isOpen && nameRef.current) {
      nameRef.current.focus();
    }
  }, [isOpen]);

  useRegisterEscapeLayer(LAYER_MODAL, onClose, isOpen && !isPolling);
  useFocusTrap(dialogRef, isOpen, { initialFocus: nameRef });
  useFocusReturn(isOpen);

  const addCloneUrl = () => {
    const urls = splitLineInput(urlInput);
    if (urls.length > 0) {
      setCloneUrls((prev) => appendUnique(prev, urls));
      setUrlInput("");
    }
  };

  const removeCloneUrl = (url: string) => {
    setCloneUrls((prev) => prev.filter((u) => u !== url));
  };

  const addLocalRepo = () => {
    const paths = splitLineInput(pathInput);
    if (paths.length > 0) {
      setLocalRepos((prev) => appendUnique(prev, paths));
      setPathInput("");
    }
  };

  const removeLocalRepo = (path: string) => {
    setLocalRepos((prev) => prev.filter((repo) => repo !== path));
  };

  const selectWorkspaceType = (next: WorkspaceType): void => {
    setWorkspaceType(next);
    setError("");
  };

  const handleModeKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    const currentIndex = WORKSPACE_MODES.findIndex(
      (mode) => mode.id === workspaceType,
    );
    let nextIndex = -1;

    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % WORKSPACE_MODES.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex =
        (currentIndex - 1 + WORKSPACE_MODES.length) % WORKSPACE_MODES.length;
    }

    if (nextIndex >= 0) {
      event.preventDefault();
      const next = WORKSPACE_MODES[nextIndex];
      if (next) selectWorkspaceType(next.id);
    }
  };

  const hasPendingUrl = urlInput.trim() !== "";
  const hasPendingPath = pathInput.trim() !== "";
  const nameError =
    name.trim() !== "" && !WORKSPACE_NAME_RE.test(name.trim())
      ? "Use letters, numbers, hyphens, or underscores."
      : "";
  const hasRequiredRepos =
    workspaceType === "empty" ||
    (workspaceType === "clone"
      ? cloneUrls.length > 0 || hasPendingUrl
      : localRepos.length > 0 || hasPendingPath);
  const canSubmit =
    name.trim() !== "" && nameError === "" && !isSubmitting && hasRequiredRepos;

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setIsSubmitting(true);
      setError("");

      let req: CreateWorkspaceRequest;
      if (workspaceType === "clone") {
        let finalCloneUrls = cloneUrls;
        if (urlInput.trim()) {
          finalCloneUrls = appendUnique(cloneUrls, splitLineInput(urlInput));
          setUrlInput("");
        }
        req = {
          name: name.trim(),
          type: "clone",
          clone_urls: finalCloneUrls,
        };
      } else if (workspaceType === "local") {
        let finalLocalRepos = localRepos;
        if (pathInput.trim()) {
          finalLocalRepos = appendUnique(localRepos, splitLineInput(pathInput));
          setPathInput("");
        }
        req = {
          name: name.trim(),
          type: "empty",
          repos: finalLocalRepos,
        };
      } else {
        req = {
          name: name.trim(),
          type: "empty",
        };
      }

      try {
        const result = await createWorkspace(req);
        if (result.kind === "async") {
          startJob(result.jobId);
          return;
        }
        const warnings = result.warnings;
        setIsSubmitting(false);
        try {
          onSuccess(result.data, req.name, warnings);
        } catch {
          // onSuccess errors must not block close
        }
        onClose();
      } catch (err: unknown) {
        setIsSubmitting(false);
        const message =
          err instanceof Error ? err.message : "Failed to create workspace";
        if (
          typeof err === "object" &&
          err !== null &&
          "body" in err &&
          typeof (err as { body: unknown }).body === "object" &&
          (err as { body: { error?: string } }).body?.error
        ) {
          setError((err as { body: { error: string } }).body.error);
        } else {
          setError(message);
        }
      }
    },
    [
      canSubmit,
      name,
      workspaceType,
      cloneUrls,
      urlInput,
      localRepos,
      pathInput,
      onSuccess,
      onClose,
      startJob,
    ],
  );

  const modalFooter = isPolling ? undefined : (
    <>
      <button
        type="button"
        className={aetherModalStyles.linkButton}
        onClick={onClose}
        disabled={isSubmitting}
        data-testid="create-workspace-cancel"
      >
        Cancel
      </button>
      <button
        type="submit"
        form="create-workspace-form"
        className={`${aetherModalStyles.primaryButton}${isSubmitting ? ` ${aetherModalStyles.submitting}` : ""}`}
        disabled={!canSubmit}
        data-testid="create-workspace-submit"
      >
        {isSubmitting && (
          <span
            className={styles.spinner}
            aria-hidden="true"
            data-testid="create-workspace-spinner"
          />
        )}
        {isSubmitting ? "Creating..." : "Create Workspace"}
      </button>
    </>
  );

  return (
    <AetherModal
      isOpen={isOpen}
      title={isPolling ? "Creating Workspace" : "New Workspace"}
      ariaLabel="New Workspace"
      onClose={onClose}
      disableOverlayDismiss={isPolling}
      dialogRef={dialogRef}
      overlayTestId="create-workspace-overlay"
      closeTestId="create-workspace-close"
      showCloseButton={!isPolling}
      footer={modalFooter}
    >
      {isPolling ? (
        <div
          className={styles.progressContainer}
          data-testid="create-workspace-progress"
        >
          <div className={styles.progressSpinner} aria-hidden="true" />
          <p className={styles.progressMessage}>{jobProgress}</p>
          <p className={styles.progressElapsed}>{jobElapsed}</p>
        </div>
      ) : (
        <form id="create-workspace-form" onSubmit={handleSubmit}>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="ws-name">
              Name
            </label>
            <input
              ref={nameRef}
              id="ws-name"
              className={styles.input}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-workspace"
              disabled={isSubmitting}
              data-testid="create-workspace-name"
            />
            {nameError && <p className={styles.fieldHint}>{nameError}</p>}
          </div>

          <div
            className={styles.modeTabs}
            role="tablist"
            aria-label="Workspace creation mode"
            onKeyDown={handleModeKeyDown}
          >
            {WORKSPACE_MODES.map((mode) => {
              const isActive = workspaceType === mode.id;
              return (
                <button
                  key={mode.id}
                  type="button"
                  role="tab"
                  className={styles.modeTab}
                  data-active={isActive || undefined}
                  data-testid={`create-workspace-mode-${mode.id}`}
                  aria-selected={isActive}
                  tabIndex={isActive ? 0 : -1}
                  onClick={() => selectWorkspaceType(mode.id)}
                  disabled={isSubmitting}
                >
                  {mode.label}
                </button>
              );
            })}
          </div>

          {workspaceType === "clone" ? (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="ws-clone-url">
                Repository URL
              </label>
              <div className={styles.addRow}>
                <textarea
                  id="ws-clone-url"
                  className={styles.input}
                  value={urlInput}
                  onChange={(e) => setUrlInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addCloneUrl();
                    }
                  }}
                  placeholder="https://github.com/... or git@..."
                  rows={2}
                  disabled={isSubmitting}
                  data-testid="create-workspace-clone-url"
                />
                <button
                  type="button"
                  className={styles.addButton}
                  onClick={addCloneUrl}
                  disabled={isSubmitting || !urlInput.trim()}
                >
                  Add
                </button>
              </div>
              {cloneUrls.length > 0 && (
                <div className={styles.chipList}>
                  {cloneUrls.map((url) => (
                    <span key={url} className={styles.chip}>
                      <span className={styles.chipText}>{url}</span>
                      <button
                        type="button"
                        className={styles.chipRemove}
                        onClick={() => removeCloneUrl(url)}
                        aria-label={`Remove ${url}`}
                      >
                        &times;
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>
          ) : workspaceType === "local" ? (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="ws-local-path">
                Local repository path
              </label>
              <div className={styles.addRow}>
                <textarea
                  id="ws-local-path"
                  className={styles.input}
                  value={pathInput}
                  onChange={(e) => setPathInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addLocalRepo();
                    }
                  }}
                  placeholder="/path/to/repository"
                  rows={2}
                  disabled={isSubmitting}
                  data-testid="create-workspace-local-path"
                />
                <button
                  type="button"
                  className={styles.addButton}
                  onClick={addLocalRepo}
                  disabled={isSubmitting || !pathInput.trim()}
                >
                  Add
                </button>
              </div>
              {localRepos.length > 0 && (
                <div className={styles.chipList}>
                  {localRepos.map((path) => (
                    <span key={path} className={styles.chip}>
                      <span className={styles.chipText}>{path}</span>
                      <button
                        type="button"
                        className={styles.chipRemove}
                        onClick={() => removeLocalRepo(path)}
                        aria-label={`Remove ${path}`}
                      >
                        &times;
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>
          ) : (
            <p className={styles.emptyModeHint}>
              Create the workspace now and add repositories later.
            </p>
          )}

          {(error || jobError) && (
            <p className={styles.error} data-testid="create-workspace-error">
              {error || jobError}
            </p>
          )}
        </form>
      )}
    </AetherModal>
  );
}
