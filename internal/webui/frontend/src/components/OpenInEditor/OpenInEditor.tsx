/**
 * OpenInEditor dropdown component.
 * Lets users open a file/workspace path in any detected external editor.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import { useEditors } from "@/hooks";
import type { EditorInfo } from "@/types";

import styles from "./OpenInEditor.module.css";

export interface OpenInEditorProps {
  /** File or workspace path to open */
  path: string;
  /** Additional CSS class name */
  className?: string;
}

type LaunchState =
  | { status: "idle" }
  | { status: "launching"; editorId: string }
  | { status: "error"; message: string };

export function OpenInEditor({
  path,
  className,
}: OpenInEditorProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [launchState, setLaunchState] = useState<LaunchState>({
    status: "idle",
  });
  const [focusedIndex, setFocusedIndex] = useState<number>(-1);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const launchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const { detectedEditors, isLoading, error, openEditor } = useEditors();

  // Clear launch timer on unmount
  useEffect(() => {
    return () => {
      if (launchTimerRef.current !== null) {
        clearTimeout(launchTimerRef.current);
      }
    };
  }, []);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setFocusedIndex(-1);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Handle escape key to close
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        setFocusedIndex(-1);
        triggerRef.current?.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen]);

  const isDisabled = isLoading || !!error || !path;
  const isLaunching = launchState.status === "launching";

  const handleTriggerClick = useCallback(() => {
    if (isDisabled || isLaunching) return;
    setIsOpen((prev) => !prev);
    if (!isOpen) {
      setFocusedIndex(0);
    }
  }, [isDisabled, isLaunching, isOpen]);

  const handleSelect = useCallback(
    async (editor: EditorInfo) => {
      setIsOpen(false);
      setFocusedIndex(-1);
      setLaunchState({ status: "launching", editorId: editor.id });

      try {
        await openEditor(editor.id, path);
        // Show launching state briefly, then revert
        launchTimerRef.current = setTimeout(() => {
          setLaunchState({ status: "idle" });
        }, 1500);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to launch";
        setLaunchState({ status: "error", message });
        launchTimerRef.current = setTimeout(() => {
          setLaunchState({ status: "idle" });
        }, 2000);
      }
    },
    [openEditor, path],
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (!isOpen) {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          handleTriggerClick();
        }
        return;
      }

      if (detectedEditors.length === 0) {
        // No options to navigate
        return;
      }

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          setFocusedIndex((prev) =>
            Math.min(prev + 1, detectedEditors.length - 1),
          );
          break;
        case "ArrowUp":
          event.preventDefault();
          setFocusedIndex((prev) => Math.max(prev - 1, 0));
          break;
        case "Enter":
        case " ": {
          event.preventDefault();
          const selectedEditor = detectedEditors[focusedIndex];
          if (
            focusedIndex >= 0 &&
            focusedIndex < detectedEditors.length &&
            selectedEditor
          ) {
            handleSelect(selectedEditor);
          }
          break;
        }
        case "Home":
          event.preventDefault();
          setFocusedIndex(0);
          break;
        case "End":
          event.preventDefault();
          setFocusedIndex(detectedEditors.length - 1);
          break;
      }
    },
    [isOpen, focusedIndex, detectedEditors, handleTriggerClick, handleSelect],
  );

  let triggerText = "Open in\u2026";
  if (launchState.status === "launching") {
    triggerText = "Launching\u2026";
  } else if (launchState.status === "error") {
    triggerText = launchState.message;
  }

  const rootClassName = [styles.openInEditor, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        onKeyDown={handleKeyDown}
        disabled={isDisabled}
        data-launching={isLaunching || undefined}
        data-error={launchState.status === "error" || undefined}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label={
          isDisabled
            ? "Open in editor (unavailable)"
            : "Open in editor. Click to select."
        }
        title={error ? "Failed to load editors" : undefined}
        data-testid="open-in-editor-trigger"
      >
        <span className={styles.triggerText}>{triggerText}</span>
        <span
          className={styles.chevron}
          data-open={isOpen || undefined}
          aria-hidden="true"
        >
          ▾
        </span>
      </button>

      {isOpen && (
        <div
          ref={menuRef}
          className={styles.menu}
          role="listbox"
          aria-label="Select editor"
          data-testid="open-in-editor-menu"
        >
          {detectedEditors.length === 0 ? (
            <div className={styles.emptyOption}>No editors detected</div>
          ) : (
            detectedEditors.map((editor, index) => (
              <div
                key={editor.id}
                className={styles.option}
                data-focused={index === focusedIndex || undefined}
                role="option"
                aria-selected={index === focusedIndex}
                onClick={() => handleSelect(editor)}
                data-testid={`editor-option-${editor.id}`}
              >
                <span className={styles.editorName}>{editor.display_name}</span>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}
