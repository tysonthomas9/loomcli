/**
 * AuthorVersionPanel — author a custom workflow version from source. Submits the
 * entrypoint file to POST …/versions, which builds it with Flue and registers an
 * immutable INACTIVE version (DEV-V5-33 D5: approve + activate are separate).
 */

import { useState } from "react";

import type { CreateWorkflowVersionInput } from "@/api";

import styles from "./WorkflowsDashboard.module.css";

export interface AuthorVersionPanelProps {
  workflowName: string;
  pending: boolean;
  onAuthor: (input: CreateWorkflowVersionInput) => void;
}

const DEFAULT_SOURCE = `export async function run(ctx) {
  return { status: "completed" };
}
`;

export function AuthorVersionPanel({
  workflowName,
  pending,
  onAuthor,
}: AuthorVersionPanelProps): JSX.Element {
  const [source, setSource] = useState(DEFAULT_SOURCE);
  const entrypoint = `workflows/${workflowName}.ts`;

  const handleBuild = () => {
    onAuthor({ entrypoint, files: { [entrypoint]: source } });
  };

  return (
    <details className={styles.authorPanel} data-testid="author-version-panel">
      <summary>Author a new version</summary>
      <p className={styles.subtle}>
        Builds <code>{entrypoint}</code> with Flue and registers an inactive,
        unapproved version. Approve and activate it separately.
      </p>
      <textarea
        className={styles.sourceInput}
        value={source}
        onChange={(event) => setSource(event.target.value)}
        spellCheck={false}
        rows={10}
        aria-label="Workflow source"
        data-testid="author-source"
      />
      <button
        type="button"
        className={styles.primaryButton}
        onClick={handleBuild}
        disabled={pending || source.trim() === ""}
        data-testid="author-build"
      >
        Build version
      </button>
    </details>
  );
}
