/**
 * EmbeddedTerminal component.
 * Wraps TerminalInstance with a header bar showing backend info,
 * connection state, worktree breadcrumb, and optional git actions.
 * Designed to be rendered inside terminal-type tabs in IssueDetailPanel.
 */

import { forwardRef, useState } from "react";

import { useGitActions } from "@/hooks/useGitActions";
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
}

export const EmbeddedTerminal = forwardRef<
  TerminalInstanceHandle,
  EmbeddedTerminalProps
>(function EmbeddedTerminal(
  { sessionName, backend, agentName, worktreePath, isActive },
  ref,
) {
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");

  const gitActions = useGitActions({ agentName });

  return (
    <div className={styles.container} data-testid="embedded-terminal">
      <TerminalHeader
        backend={backend}
        worktreePath={worktreePath}
        agentName={agentName}
        connectionState={connectionState}
        gitActions={agentName !== null ? gitActions : undefined}
      />
      <div className={styles.terminalContainer}>
        <TerminalInstance
          ref={ref}
          sessionName={sessionName}
          isActive={isActive}
          onConnectionStateChange={setConnectionState}
        />
      </div>
    </div>
  );
});
