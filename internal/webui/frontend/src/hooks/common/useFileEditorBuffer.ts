import { useCallback, useEffect, useRef, useState } from "react";

import type { FileReadData } from "@/api/workspace";
import { detectLanguage } from "@/utils/detectLanguage";

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
        void saveRef.current();
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
