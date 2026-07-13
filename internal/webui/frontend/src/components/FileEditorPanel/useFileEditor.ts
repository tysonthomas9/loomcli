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
  type UseFileTreeReturn,
  type UseFileContentReturn,
} from "@/hooks";
import { writeWorktreeFile } from "@/hooks/api";
import type { FileReadData } from "@/api/workspace";
import { detectLanguage } from "@/utils/detectLanguage";

interface PendingAction {
  type: "switch";
  path: string;
}

export interface UseFileEditorOptions {
  /** Browse workspace primary repo (lead agents). Disables saving. */
  useWorkspaceTree?: boolean;
  /** Primary repo label for UI hints. */
  workspaceRepoLabel?: string | null;
}

export interface UseFileEditorReturn {
  /** File tree state (passthrough from useFileTree) */
  tree: UseFileTreeReturn;
  /** File content state (passthrough from useFileContent) */
  fileContent: UseFileContentReturn;
  /** Whether the panel is browsing the workspace primary repo */
  isWorkspaceTree: boolean;
  /** Primary repo name when browsing workspace tree */
  workspaceRepoLabel: string | null;
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

export interface UseFileEditorBufferOptions {
  path: string | null;
  fileData: FileReadData | null;
  isActive: boolean;
  canSave: boolean;
  writeFile: (path: string, content: string) => Promise<void>;
  onDirtyChange?: ((path: string, isDirty: boolean) => void) | undefined;
  onSaved?: (() => void) | undefined;
  onSaveError?: ((err: unknown) => void) | undefined;
  onBlockedSave?: (() => void) | undefined;
  resetKey?: unknown;
}

export interface UseFileEditorBufferReturn {
  content: string;
  language: string | undefined;
  isDirty: boolean;
  isSaving: boolean;
  handleContentChange: (value: string) => void;
  save: () => Promise<void>;
}

export function useFileEditorBuffer({
  path,
  fileData,
  isActive,
  canSave,
  writeFile,
  onDirtyChange,
  onSaved,
  onSaveError,
  onBlockedSave,
  resetKey,
}: UseFileEditorBufferOptions): UseFileEditorBufferReturn {
  const [content, setContent] = useState<string>("");
  const [isSaving, setIsSaving] = useState<boolean>(false);

  const savedContentRef = useRef<string>("");
  const mountedRef = useRef<boolean>(true);
  const resetKeyRef = useRef(resetKey);

  const isDirty = content !== savedContentRef.current;
  const language = path ? detectLanguage(path) : undefined;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (!path) {
      setContent("");
      savedContentRef.current = "";
      setIsSaving(false);
    }
  }, [path]);

  useEffect(() => {
    if (fileData && !fileData.binary) {
      const newContent = fileData.content ?? "";
      setContent(newContent);
      savedContentRef.current = newContent;
    }
  }, [fileData]);

  useEffect(() => {
    if (resetKeyRef.current === resetKey) return;
    resetKeyRef.current = resetKey;
    setContent("");
    savedContentRef.current = "";
    setIsSaving(false);
  }, [resetKey]);

  useEffect(() => {
    if (path) {
      onDirtyChange?.(path, isDirty);
    }
  }, [path, isDirty, onDirtyChange]);

  const handleContentChange = useCallback((value: string) => {
    setContent(value);
  }, []);

  const save = useCallback(async () => {
    if (!canSave) {
      onBlockedSave?.();
      return;
    }
    if (!path || !isDirty || isSaving) return;

    setIsSaving(true);
    try {
      await writeFile(path, content);
      if (mountedRef.current) {
        savedContentRef.current = content;
        onSaved?.();
      }
    } catch (err) {
      if (mountedRef.current) {
        onSaveError?.(err);
      }
    } finally {
      if (mountedRef.current) {
        setIsSaving(false);
      }
    }
  }, [
    canSave,
    path,
    isDirty,
    isSaving,
    writeFile,
    content,
    onSaved,
    onSaveError,
    onBlockedSave,
  ]);

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
    content,
    language,
    isDirty,
    isSaving,
    handleContentChange,
    save,
  };
}

export function useFileEditor(
  agentName: string,
  isActive: boolean,
  options?: UseFileEditorOptions,
): UseFileEditorReturn {
  const useWorkspaceTree = options?.useWorkspaceTree ?? false;
  const workspaceRepoLabel = options?.workspaceRepoLabel ?? null;
  const { workspaceId } = useWorkspaceContext();
  const tree = useFileTree(agentName, { useWorkspaceTree });
  const fileContent = useFileContent(agentName);
  const { showToast } = useToast();

  const [pendingAction, setPendingAction] = useState<PendingAction | null>(
    null,
  );
  const [resetVersion, setResetVersion] = useState(0);
  const previousAgentNameRef = useRef(agentName);

  const editorBuffer = useFileEditorBuffer({
    path: tree.selectedPath,
    fileData: fileContent.fileData,
    isActive,
    canSave: !useWorkspaceTree,
    writeFile: (path, nextContent) =>
      writeWorktreeFile(workspaceId, agentName, path, nextContent),
    onBlockedSave: () =>
      showToast("Workspace repository files are read-only for lead agents", {
        type: "info",
      }),
    onSaved: () => showToast("File saved", { type: "success" }),
    onSaveError: (err) =>
      showToast(
        `Failed to save: ${err instanceof Error ? err.message : String(err)}`,
        { type: "error" },
      ),
    resetKey: resetVersion,
  });
  const { content, language, isDirty, isSaving, handleContentChange, save } =
    editorBuffer;

  // Reset state when agent changes
  useEffect(() => {
    if (previousAgentNameRef.current === agentName) return;
    previousAgentNameRef.current = agentName;
    setPendingAction(null);
    setResetVersion((version) => version + 1);
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

  const confirmDiscard = useCallback(() => {
    if (!pendingAction) return;
    const { path } = pendingAction;
    setPendingAction(null);
    setResetVersion((version) => version + 1);
    executeSwitch(path);
  }, [pendingAction, executeSwitch]);

  const cancelDiscard = useCallback(() => {
    setPendingAction(null);
  }, []);

  return {
    tree,
    fileContent,
    isWorkspaceTree: useWorkspaceTree,
    workspaceRepoLabel,
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
