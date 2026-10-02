import { useParams } from "react-router-dom";
import { AgentChat } from "@/components/AgentChat";

/** The one chat route for Agent API agents, the same for every harness. */
export function AgentChatPage(): JSX.Element {
  const { workspaceId = "", agentId = "" } = useParams();
  return (
    <div style={{ height: "100%", minHeight: 0, padding: 8 }}>
      <AgentChat key={agentId} workspaceId={workspaceId} agentId={agentId} />
    </div>
  );
}
