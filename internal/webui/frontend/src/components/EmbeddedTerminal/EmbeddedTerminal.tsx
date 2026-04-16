/**
 * EmbeddedTerminal component.
 * Wraps TerminalInstance with a header bar showing backend info,
 * connection state, worktree breadcrumb, and optional git actions.
 * Designed to be rendered inside terminal-type tabs in IssueDetailPanel.
 *
 * Selection, copy, and paste are handled natively by wterm's DOM renderer
 * + the browser's built-in clipboard API, so no JS interception layer is
 * needed here.
 */

import { forwardRef, useState, useCallback } from "react";

import { useGitActions } from "@/hooks/workspace";

import {
  TerminalInstance,
  type ConnectionState,
  type TerminalInstanceHandle,
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

  const handleConnectionStateChange = useCallback(
    (state: ConnectionState) => {
      setConnectionState(state);
      onExternalStateChange?.(state);
    },
    [onExternalStateChange],
  );

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
          ref={ref}
          sessionName={sessionName}
          isActive={isActive}
          onConnectionStateChange={handleConnectionStateChange}
        />
      </div>
    </div>
  );
});
