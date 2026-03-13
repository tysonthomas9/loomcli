import { useState, useCallback, useEffect } from "react";
import { useAgents } from "@/hooks";
import { useFileTree } from "@/hooks/useFileTree";
import { useFileContent } from "@/hooks/useFileContent";
import { FileTree } from "./FileTree";
import { FileViewer } from "./FileViewer";
import styles from "./FileExplorer.module.css";

export function FileExplorer() {
  const { agents } = useAgents({ pollInterval: 5000 });
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
          <div className={styles.loading}>Loading...</div>
        ) : error ? (
          <div className={styles.error}>{error}</div>
        ) : agents.length === 0 ? (
          <div className={styles.empty}>No agents running</div>
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
