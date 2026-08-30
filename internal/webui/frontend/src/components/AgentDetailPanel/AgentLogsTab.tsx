import { useCallback, useEffect, useState } from "react";

import { ensureAgentTerminalSession, getAgentTerminalInfo } from "@/hooks/api";
import { EmbeddedTerminal } from "@/components/EmbeddedTerminal";
import type { ConnectionState } from "@/components/TerminalView";
import { useLogStream } from "@/hooks/terminal/useLogStream";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./AgentDetailPanel.module.css";

interface AgentLogsTabProps {
  agentName: string;
  isActive: boolean;
}

// LogViewState widens ConnectionState with "empty" so an idle live stream with
// no replayed bytes does not show a misleading populated/connected state.
type LogViewState = ConnectionState | "reconnecting" | "empty";

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
  const [state, setState] = useState<LogViewState>("connecting");

  const liveStream = useLogStream({
    workspaceId,
    streamPath: `/agents/${encodeURIComponent(agentName)}/logs/stream`,
    enabled: isActive && mode === "archive",
  });

  const load = useCallback(async () => {
    setState("connecting");
    setMode(null);
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
        // tmux connection state is driven by EmbeddedTerminal below.
      } else {
        setMode("archive");
      }
    } catch {
      setState("disconnected");
    }
  }, [agentName, workspaceId]);

  useEffect(() => {
    if (!isActive) return;
    void load();
  }, [isActive, load]);

  const liveState: LogViewState =
    mode === "archive" &&
    liveStream.state === "connected" &&
    liveStream.content === ""
      ? "empty"
      : liveStream.state;
  const viewState = mode === "archive" ? liveState : state;
  const stateLabel = viewState === "empty" ? "no logs" : viewState;

  return (
    <div className={styles.scrollableContent}>
      <div className={styles.section}>
        <h3 className={styles.sectionTitle}>
          {mode === "tmux" ? "Live terminal" : "Live log"}
        </h3>
        <button type="button" onClick={load}>
          Refresh
        </button>
        <div data-testid="log-viewer">
          <span data-state={viewState}>{stateLabel}</span>
          {mode === "tmux" && terminalSession ? (
            <EmbeddedTerminal
              sessionName={terminalSession.sessionName}
              backend={terminalSession.backend}
              agentName={terminalSession.agentName}
              isActive={isActive}
              onConnectionStateChange={setState}
            />
          ) : viewState === "empty" ? (
            <p data-testid="archive-empty" className={styles.emptyState}>
              No logs available for this agent yet.
            </p>
          ) : (
            <pre data-testid="terminal-container">
              {mode === "archive" ? liveStream.content : ""}
            </pre>
          )}
        </div>
      </div>
    </div>
  );
}
