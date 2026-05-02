import { useCallback, useEffect, useState } from "react";

import { getAgentLogArchive, getAgentTerminalInfo } from "@/hooks/api";
import { EmbeddedTerminal } from "@/components/EmbeddedTerminal";
import type { ConnectionState } from "@/components/TerminalView";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./AgentDetailPanel.module.css";

interface AgentLogsTabProps {
  agentName: string;
  isActive: boolean;
}

export function AgentLogsTab({
  agentName,
  isActive,
}: AgentLogsTabProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const [mode, setMode] = useState<"tmux" | "archive" | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const [state, setState] = useState<ConnectionState>("connecting");

  const load = useCallback(async () => {
    setState("connecting");
    try {
      const nextMode = await getAgentTerminalInfo(workspaceId, agentName);
      setMode(nextMode);
      if (nextMode === "tmux") {
        setLines([]);
      } else {
        const archive = await getAgentLogArchive(workspaceId, agentName);
        setLines(archive.lines);
      }
      if (nextMode === "archive") setState("connected");
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
          {mode === "tmux" ? "Live (tmux)" : "Archive snapshot"}
        </h3>
        <button type="button" onClick={load}>
          Refresh
        </button>
        <div data-testid="log-viewer">
          <span data-state={state}>{state}</span>
          {mode === "tmux" ? (
            <EmbeddedTerminal
              sessionName={`agent-${agentName}`}
              backend="agent"
              agentName={agentName}
              isActive={isActive}
              onConnectionStateChange={setState}
            />
          ) : (
            <pre data-testid="terminal-container">{lines.join("\n")}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
