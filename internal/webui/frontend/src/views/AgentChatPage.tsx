import { useParams } from "react-router-dom";
import { AgentChat } from "@/components/AgentChat";
import { AgentList } from "@/components/AgentList";

/** The one chat route for Agent API agents, the same for every harness. */
export function AgentChatPage(): JSX.Element {
  const { workspaceId = "", agentId = "" } = useParams();
  return (
    <div style={{ display: "flex", height: "100%", minHeight: 0, padding: 8 }}>
      <div style={{ width: 240, flexShrink: 0 }}>
        <AgentList workspaceId={workspaceId} activeId={agentId} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <AgentChat key={agentId} workspaceId={workspaceId} agentId={agentId} />
      </div>
    </div>
  );
}
