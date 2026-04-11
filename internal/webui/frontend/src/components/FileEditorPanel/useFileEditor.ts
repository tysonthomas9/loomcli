/**
 * useFileEditor - Custom hook encapsulating all editor state for FileEditorPanel.
 * Composes useFileTree + useFileContent and adds editing, dirty tracking,
 * save, and discard confirmation logic.
 */

import { useState, useRef, useCallback, useEffect } from "react";
import {
  useFileTree,
  useFileContent,
  useToast,
  useWorkspaceContext,
} from "@/hooks";
import type { UseFileTreeReturn, UseFileContentReturn } from "@/hooks";
import { writeWorktreeFile } from "@/hooks/api";

interface PendingAction {
  type: "switch";
  path: string;
}

function getLanguageFromPath(path: string): string | undefined {
  const ext = path.split(".").pop()?.toLowerCase();
  switch (ext) {
    case "go":
      return "go";
    case "json":
      return "json";
    case "yaml":
    case "yml":
      return "yaml";
    case "md":
    case "markdown":
      return "markdown";
    default:
      return undefined;
  }
}

export interface UseFileEditorReturn {
  /** File tree state (passthrough from useFileTree) */
  tree: UseFileTreeReturn;
  /** File content state (passthrough from useFileContent) */
  fileContent: UseFileContentReturn;
  /** Current editor buffer */
  content: string;
  /** Language derived from file extension */
  language: string | undefined;
  /** Whether content differs from last saved/loaded value */
  isDirty: boolean;
  /** Whether a save is in progress */
  isSaving: boolean;
  /** Pending action when switching files with unsaved changes */
  pendingAction: PendingAction | null;
  /** Handle file selection (with dirty check) */
  handleFileSelect: (path: string) => void;
  /** Handle content changes from the editor */
  handleContentChange: (value: string) => void;
  /** Save current content to the file */
  save: () => Promise<void>;
  /** Confirm discarding changes and execute pending action */
  confirmDiscard: () => void;
  /** Cancel discard and stay on current file */
  cancelDiscard: () => void;
}

export function useFileEditor(
  agentName: string,
  isActive: boolean,
): UseFileEditorReturn {
  const { workspaceId } = useWorkspaceContext();
  const tree = useFileTree(agentName);
  const fileContent = useFileContent(agentName);
  const { showToast } = useToast();

  const [content, setContent] = useState<string>("");
  const [isSaving, setIsSaving] = useState<boolean>(false);
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(
    null,
  );

  const savedContentRef = useRef<string>("");
  const mountedRef = useRef<boolean>(true);

  const isDirty = content !== savedContentRef.current;
  const language = tree.selectedPath
    ? getLanguageFromPath(tree.selectedPath)
    : undefined;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Sync content when file data loads
  useEffect(() => {
    if (fileContent.fileData && !fileContent.fileData.binary) {
      const newContent = fileContent.fileData.content ?? "";
      setContent(newContent);
      savedContentRef.current = newContent;
    }
  }, [fileContent.fileData]);

  // Reset state when agent changes
  useEffect(() => {
    setContent("");
    savedContentRef.current = "";
    setPendingAction(null);
    setIsSaving(false);
  }, [agentName]);

  const executeSwitch = useCallback(
    (path: string) => {
      tree.selectFile(path);
      fileContent.fetchFile(path);
    },
    [tree, fileContent],
  );

  const handleFileSelect = useCallback(
    (path: string) => {
      if (isDirty) {
        setPendingAction({ type: "switch", path });
      } else {
        executeSwitch(path);
      }
    },
    [isDirty, executeSwitch],
  );

  const handleContentChange = useCallback((value: string) => {
    setContent(value);
  }, []);

  const save = useCallback(async () => {
    if (!tree.selectedPath || !isDirty || isSaving) return;

    setIsSaving(true);
    try {
      await writeWorktreeFile(
        workspaceId,
        agentName,
        tree.selectedPath,
        content,
      );
      if (mountedRef.current) {
        savedContentRef.current = content;
        showToast("File saved", { type: "success" });
      }
    } catch (err) {
      if (mountedRef.current) {
        showToast(
          `Failed to save: ${err instanceof Error ? err.message : String(err)}`,
          { type: "error" },
        );
      }
    } finally {
      if (mountedRef.current) {
        setIsSaving(false);
      }
    }
  }, [
    workspaceId,
    agentName,
    tree.selectedPath,
    content,
    isDirty,
    isSaving,
    showToast,
  ]);

  const confirmDiscard = useCallback(() => {
    if (!pendingAction) return;
    const { path } = pendingAction;
    setPendingAction(null);
    setContent("");
    savedContentRef.current = "";
    executeSwitch(path);
  }, [pendingAction, executeSwitch]);

  const cancelDiscard = useCallback(() => {
    setPendingAction(null);
  }, []);

  // Keyboard shortcuts (Cmd+S)
  const saveRef = useRef(save);
  useEffect(() => {
    saveRef.current = save;
  }, [save]);

  useEffect(() => {
    if (!isActive) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "s") {
        event.preventDefault();
        saveRef.current();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isActive]);

  return {
    tree,
    fileContent,
    content,
    language,
    isDirty,
    isSaving,
    pendingAction,
    handleFileSelect,
    handleContentChange,
    save,
    confirmDiscard,
    cancelDiscard,
  };
}
