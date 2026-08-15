import { useSkillCapabilities } from "@/hooks";

import styles from "../FileExplorer.module.css";

function Notice({
  loading,
  loadingMessage,
  error,
  fallback,
  onRetry,
}: {
  loading: boolean;
  loadingMessage: string;
  error: string | null;
  fallback: string;
  onRetry: () => void;
}) {
  if (!loading && !error) return null;
  return (
    <div className={styles.capabilitiesNotice} role="status">
      <span>{loading ? loadingMessage : (error ?? fallback)}</span>
      {error && (
        <button type="button" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}

export function CapabilityNotices({
  workspaceId,
  filesLoading,
  filesError,
  retryFiles,
}: {
  workspaceId: string;
  filesLoading: boolean;
  filesError: string | null;
  retryFiles: () => void;
}) {
  const skills = useSkillCapabilities(workspaceId);
  return (
    <>
      <Notice
        loading={filesLoading}
        loadingMessage="Checking file permissions..."
        error={
          filesError
            ? "File permissions unavailable. Editing is disabled."
            : null
        }
        fallback="File permissions unavailable. Editing is disabled."
        onRetry={retryFiles}
      />
      <Notice
        loading={skills.status === "loading"}
        loadingMessage="Checking skill permissions..."
        error={skills.status === "error" ? skills.error : null}
        fallback="Skill permissions unavailable. Editing is disabled."
        onRetry={() => void skills.retry()}
      />
    </>
  );
}
