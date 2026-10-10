import type { CSSProperties } from "react";
import { agentColor, agentColorIndex, agentInitials } from "@/hooks";
import tray from "./AgentTray.module.css";

/**
 * An agent's initials on its stable colour (CL1), as the Started marker,
 * the result card and the tray show it. size is the circle's px.
 */
export function AgentBadge({
  id,
  name,
  size,
  className,
}: {
  id: string;
  name: string;
  size: number;
  className?: string | undefined;
}) {
  const style = {
    "--agent-color": agentColor(id),
    width: size,
    height: size,
    fontSize: Math.round(size * 0.47),
  } as CSSProperties;
  return (
    <span
      className={className ? `${tray.badge} ${className}` : tray.badge}
      style={style}
      data-agent-color={agentColorIndex(id)}
      aria-hidden="true"
    >
      {agentInitials(name)}
    </span>
  );
}
