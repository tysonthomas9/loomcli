import { useState, useCallback, useEffect } from "react";
import { ErrorDisplay } from "@/components/ErrorDisplay/ErrorDisplay";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks/common/useStoreContext";
import { useFileTree } from "@/hooks/common/useFileTree";
import { useFileContent } from "@/hooks/common/useFileContent";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import { FileTree } from "./FileTree";
import { FileViewer } from "./FileViewer";
import styles from "./FileExplorer.module.css";

export function FileExplorer() {
  const agentStore = useAgentStoreInstance();
  const monitorAgents = useStore(agentStore, (s) => s.agents);
  const { agents: workspaceAgents } = useWorkspaceContext();
  const agents = [
    ...monitorAgents.map((agent) => ({ name: agent.name })),
    ...workspaceAgents
      .filter(
        (agent) =>
          !monitorAgents.some(
            (monitorAgent) => monitorAgent.name === agent.name,
          ),
      )
      .map((agent) => ({ name: agent.name })),
  ];
  const [selectedAgent, setSelectedAgent] = useState<string>("");

  // Auto-select first agent when agents load
  useEffect(() => {
    const first = agents[0];
    if (first && !selectedAgent) {
      setSelectedAgent(first.name);
    }
  }, [agents, selectedAgent]);

  const {
    expanded,
    treeData,
    selectedPath,
    isLoading,
    error,
    filterText,
    debouncedFilterText,
    toggle,
    selectFile,
    setFilterText,
  } = useFileTree(selectedAgent);

  const {
    fileData,
    isLoading: isFileLoading,
    error: fileError,
    fetchFile,
    clearFile,
  } = useFileContent(selectedAgent);

  // Fetch file content when a file is selected
  useEffect(() => {
    if (selectedPath) {
      fetchFile(selectedPath);
    }
  }, [selectedPath, fetchFile]);

  const handleClose = useCallback(() => {
    selectFile(null);
    clearFile();
  }, [selectFile, clearFile]);

  const isViewerOpen = selectedPath !== null;

  return (
    <div className={styles.container}>
      <div className={styles.treePanel}>
        <div className={styles.toolbar}>
          <select
            className={styles.agentSelect}
            value={selectedAgent}
            onChange={(e) => setSelectedAgent(e.target.value)}
            aria-label="Select agent"
          >
            {agents.map((a) => (
              <option key={a.name} value={a.name}>
                {a.name}
              </option>
            ))}
          </select>
          <input
            className={styles.filterInput}
            type="text"
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            placeholder="Filter files..."
            aria-label="Filter files"
          />
        </div>
        {isLoading ? (
          <div className={styles.treeScroll}>
            {Array.from({ length: 6 }, (_, i) => (
              <div
                key={i}
                style={{
                  padding: "4px 8px",
                  paddingLeft: `${(i % 3) * 16 + 8}px`,
                }}
              >
                <LoadingSkeleton
                  shape="text"
                  width={100 + (i % 4) * 20}
                  height={12}
                />
              </div>
            ))}
          </div>
        ) : error ? (
          <div className={styles.treeScroll}>
            <ErrorDisplay
              variant="fetch-error"
              title="Failed to load file tree"
              error={new Error(error)}
              showDetails
            />
          </div>
        ) : agents.length === 0 ? (
          <div className={styles.empty}>No agents configured</div>
        ) : (
          <div className={styles.treeScroll}>
            <FileTree
              treeData={treeData}
              expanded={expanded}
              selectedPath={selectedPath}
              filterText={debouncedFilterText}
              onToggle={toggle}
              onSelectFile={selectFile}
            />
          </div>
        )}
      </div>
      <FileViewer
        isOpen={isViewerOpen}
        path={selectedPath}
        fileData={fileData}
        isLoading={isFileLoading}
        error={fileError}
        onClose={handleClose}
      />
    </div>
  );
}
