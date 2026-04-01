/**
 * EmbeddedTerminal component.
 * Wraps TerminalInstance with a header bar showing backend info,
 * connection state, worktree breadcrumb, and optional git actions.
 * Designed to be rendered inside terminal-type tabs in IssueDetailPanel.
 */

import { forwardRef, useState, useCallback, useRef } from "react";

import { useGitActions } from "@/hooks/useGitActions";

import {
  TerminalInstance,
  type ConnectionState,
  type ContextMenuEvent,
  type TerminalInstanceHandle,
} from "@/components/TerminalView";
import {
  CopyToast,
  PasteConfirmDialog,
  TerminalContextMenu,
  useClipboard,
} from "@/components/TerminalView";

import { TerminalHeader } from "./TerminalHeader";
import styles from "./EmbeddedTerminal.module.css";

export interface EmbeddedTerminalProps {
  sessionName: string;
  backend: string;
  agentName: string | null;
  worktreePath?: string | undefined;
  isActive: boolean;
  onConnectionStateChange?: ((state: ConnectionState) => void) | undefined;
  onMaximize?: (() => void) | undefined;
  isMaximized?: boolean | undefined;
}

export const EmbeddedTerminal = forwardRef<
  TerminalInstanceHandle,
  EmbeddedTerminalProps
>(function EmbeddedTerminal(
  {
    sessionName,
    backend,
    agentName,
    worktreePath,
    isActive,
    onConnectionStateChange: onExternalStateChange,
    onMaximize,
    isMaximized,
  },
  ref,
) {
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const [contextMenu, setContextMenu] = useState<ContextMenuEvent | null>(null);

  // Set up clipboard hooks — needs instanceRefs and activeTabIdRef
  const localRef = useRef<TerminalInstanceHandle | null>(null);
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const activeTabIdRef = useRef(sessionName);
  activeTabIdRef.current = sessionName;

  // Keep instanceRefs map in sync with the local ref
  const setLocalRef = useCallback(
    (handle: TerminalInstanceHandle | null) => {
      localRef.current = handle;
      if (handle) {
        instanceRefs.current.set(sessionName, handle);
      } else {
        instanceRefs.current.delete(sessionName);
      }
      // Forward to parent ref
      if (typeof ref === "function") {
        ref(handle);
      } else if (ref) {
        (ref as React.MutableRefObject<TerminalInstanceHandle | null>).current =
          handle;
      }
    },
    [sessionName, ref],
  );

  const {
    showCopyToast,
    pendingPasteText,
    handleCopyNotify,
    handlePasteRequest,
    handlePasteConfirm,
    handlePasteCancel,
  } = useClipboard(instanceRefs, activeTabIdRef);

  const handleConnectionStateChange = useCallback(
    (state: ConnectionState) => {
      setConnectionState(state);
      onExternalStateChange?.(state);
    },
    [onExternalStateChange],
  );

  const handleContextMenu = useCallback((event: ContextMenuEvent) => {
    setContextMenu(event);
  }, []);

  const handleContextMenuClose = useCallback(() => {
    setContextMenu(null);
  }, []);

  const handleContextMenuCopy = useCallback(() => {
    const instance = instanceRefs.current.get(sessionName);
    if (instance) {
      const sel = instance.getSelection();
      if (sel) {
        navigator.clipboard
          .writeText(sel)
          .then(() => handleCopyNotify())
          .catch(() => {});
      }
    }
    setContextMenu(null);
    instanceRefs.current.get(sessionName)?.focus();
  }, [sessionName, handleCopyNotify]);

  const handleContextMenuPaste = useCallback(() => {
    setContextMenu(null);
    handlePasteRequest();
  }, [handlePasteRequest]);

  const handleContextMenuSelectAll = useCallback(() => {
    instanceRefs.current.get(sessionName)?.selectAll();
    setContextMenu(null);
  }, [sessionName]);

  const gitActions = useGitActions({ agentName });

  return (
    <div className={styles.container} data-testid="embedded-terminal">
      <TerminalHeader
        backend={backend}
        worktreePath={worktreePath}
        agentName={agentName}
        connectionState={connectionState}
        gitActions={agentName !== null ? gitActions : undefined}
        onMaximize={onMaximize}
        isMaximized={isMaximized}
      />
      <div className={styles.terminalContainer}>
        <TerminalInstance
          ref={setLocalRef}
          sessionName={sessionName}
          isActive={isActive}
          onConnectionStateChange={handleConnectionStateChange}
          onCopyNotify={handleCopyNotify}
          onPasteRequest={handlePasteRequest}
          onContextMenu={handleContextMenu}
        />
      </div>
      <PasteConfirmDialog
        isOpen={pendingPasteText !== null}
        text={pendingPasteText ?? ""}
        onConfirm={handlePasteConfirm}
        onCancel={handlePasteCancel}
      />
      <CopyToast visible={showCopyToast} />
      {contextMenu && (
        <TerminalContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          hasSelection={contextMenu.hasSelection}
          onCopy={handleContextMenuCopy}
          onPaste={handleContextMenuPaste}
          onSelectAll={handleContextMenuSelectAll}
          onClose={handleContextMenuClose}
        />
      )}
    </div>
  );
});
