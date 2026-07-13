import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { useRouteView, useWorkspaceContext } from "@/hooks";
import type { FileScopeRef } from "@/hooks/api";
import styles from "./FilesPage.module.css";

const WorkspaceFileBrowser = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.WorkspaceFileBrowser,
  })),
);

export function FilesPage() {
  const { view: activeView } = useRouteView();
  const { workspaceId, repos, agents } = useWorkspaceContext();
  const scopeOptions = useMemo(() => {
    const options: Array<{
      value: string;
      label: string;
      scopeRef: FileScopeRef;
    }> = [
      {
        value: "workspace:",
        label: "Workspace",
        scopeRef: { scope: "workspace" },
      },
    ];
    for (const repo of repos.filter((r) => !r.is_linked_worktree)) {
      options.push({
        value: `repo:${repo.name}`,
        label: `Repo: ${repo.name}`,
        scopeRef: { scope: "repo", target: repo.name },
      });
    }
    for (const agent of agents) {
      options.push({
        value: `agent:${agent.name}`,
        label: `Agent: ${agent.name}`,
        scopeRef: { scope: "agent", target: agent.name },
      });
    }
    return options;
  }, [repos, agents]);

  const [selectedScope, setSelectedScope] = useState("workspace:");

  useEffect(() => {
    if (!scopeOptions.some((option) => option.value === selectedScope)) {
      setSelectedScope("workspace:");
    }
  }, [scopeOptions, selectedScope]);

  const selectedOption =
    scopeOptions.find((option) => option.value === selectedScope) ??
    scopeOptions[0];

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <div className={styles.page}>
        <div className={styles.scopeBar}>
          <label className={styles.scopeLabel} htmlFor="file-browser-scope">
            Scope
          </label>
          <select
            id="file-browser-scope"
            className={styles.scopeSelect}
            value={selectedOption?.value ?? "workspace:"}
            onChange={(event) => setSelectedScope(event.target.value)}
            aria-label="File browser scope"
          >
            {scopeOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </div>
        <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
          {selectedOption && (
            <WorkspaceFileBrowser
              key={`${workspaceId}:${selectedOption.value}`}
              scopeRef={selectedOption.scopeRef}
            />
          )}
        </Suspense>
      </div>
    </ErrorBoundary>
  );
}
