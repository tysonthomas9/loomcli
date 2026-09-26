import { useSkillCapabilities } from "@/hooks";

import type { FileBrowserModeCapabilities } from "../treeRoots";
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

// Its own component so the hook — which loads the skill capabilities, not just
// reads them — is never called by a section that has no skills on it.
function SkillCapabilityNotice({ workspaceId }: { workspaceId: string }) {
  const skills = useSkillCapabilities(workspaceId);
  return (
    <Notice
      loading={skills.status === "loading"}
      loadingMessage="Checking skill permissions..."
      error={skills.status === "error" ? skills.error : null}
      fallback="Skill permissions unavailable. Editing is disabled."
      onRetry={() => void skills.retry()}
    />
  );
}

/**
 * A section only warns about the permissions it is actually gated by. The
 * Skills section gates editing on skill capabilities alone, so a slow or
 * failing /files/capabilities must not tell it that editing is disabled; the
 * Files and Agents sections show no skills, so a skills API failure must not
 * surface there either.
 */
export function CapabilityNotices({
  workspaceId,
  capabilities,
  filesLoading,
  filesError,
  retryFiles,
}: {
  workspaceId: string;
  capabilities: FileBrowserModeCapabilities;
  filesLoading: boolean;
  filesError: string | null;
  retryFiles: () => void;
}) {
  return (
    <>
      {capabilities.checkouts && (
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
      )}
      {capabilities.skills && (
        <SkillCapabilityNotice workspaceId={workspaceId} />
      )}
    </>
  );
}
