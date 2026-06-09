import { useCallback, useEffect, useState } from "react";

import {
  ensureAgentTerminalSession,
  getAgentLogArchive,
  getAgentTerminalInfo,
} from "@/hooks/api";
import { EmbeddedTerminal } from "@/components/EmbeddedTerminal";
import type { ConnectionState } from "@/components/TerminalView";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./AgentDetailPanel.module.css";

interface AgentLogsTabProps {
  agentName: string;
  isActive: boolean;
}

// LogViewState widens ConnectionState with "empty": an archive fetch that
// succeeded (or 404'd — see getAgentLogArchive) but returned no lines. We
// surface that distinctly instead of showing the misleading "connected" label
// over a blank viewer, which is what a daemon-mode agent with no archived log
// used to render.
type LogViewState = ConnectionState | "empty";

export function AgentLogsTab({
  agentName,
  isActive,
}: AgentLogsTabProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const [mode, setMode] = useState<"tmux" | "archive" | null>(null);
  const [terminalSession, setTerminalSession] = useState<{
    sessionName: string;
    backend: string;
    agentName: string;
  } | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const [state, setState] = useState<LogViewState>("connecting");

  const load = useCallback(async () => {
    setState("connecting");
    setTerminalSession(null);
    try {
      const nextMode = await getAgentTerminalInfo(workspaceId, agentName);
      if (nextMode === "tmux") {
        const meta = await ensureAgentTerminalSession(workspaceId, agentName);
        setTerminalSession({
          sessionName: meta.session_name,
          backend: meta.backend ?? "agent",
          agentName: meta.agent_id ?? agentName,
        });
        setMode("tmux");
        setLines([]);
        // tmux connection state is driven by EmbeddedTerminal below.
      } else {
        const archive = await getAgentLogArchive(workspaceId, agentName);
        setMode("archive");
        setLines(archive.lines);
        // "connected" only when there is actually something to show; an empty
        // archive reports "empty" so the UI never claims a populated log when
        // there isn't one.
        setState(archive.lines.length > 0 ? "connected" : "empty");
      }
    } catch {
      setState("disconnected");
    }
  }, [agentName, workspaceId]);

  useEffect(() => {
    if (!isActive) return;
    void load();
  }, [isActive, load]);

  return (
    <div className={styles.scrollableContent}>
      <div className={styles.section}>
        <h3 className={styles.sectionTitle}>
          {mode === "tmux" ? "Live terminal" : "Archive snapshot"}
        </h3>
        <button type="button" onClick={load}>
          Refresh
        </button>
        <div data-testid="log-viewer">
          <span data-state={state}>{state === "empty" ? "no logs" : state}</span>
          {mode === "tmux" && terminalSession ? (
            <EmbeddedTerminal
              sessionName={terminalSession.sessionName}
              backend={terminalSession.backend}
              agentName={terminalSession.agentName}
              isActive={isActive}
              onConnectionStateChange={setState}
            />
          ) : state === "empty" ? (
            <p data-testid="archive-empty" className={styles.emptyState}>
              No logs available for this agent yet.
            </p>
          ) : (
            <pre data-testid="terminal-container">{lines.join("\n")}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
