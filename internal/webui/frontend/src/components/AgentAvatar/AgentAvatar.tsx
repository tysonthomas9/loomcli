import type { LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, parseLoomStatus } from "@/types";
import { getStatusDotColor } from "@/utils/agent";
import { agentCompactAvatarLabel } from "@/utils/agentDisplay";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./AgentAvatar.module.css";

export interface AgentAvatarProps {
  /** Canonical agent or owner name used for initials and deterministic color. */
  name: string;
  /** Live agent data enables the shared status dot and reviewer label. */
  agent?: LoomAgentStatus | undefined;
  /** Match the 26px avatar used by compact sidebar agent cards. */
  compact?: boolean;
  /** Owners use a neutral outline; agents keep their identity color. */
  variant?: "agent" | "owner";
  title?: string | undefined;
  testId?: string | undefined;
}

/** Shared avatar presentation for agent lists, cards, and ownership badges. */
export function AgentAvatar({
  name,
  agent,
  compact = false,
  variant = "agent",
  title,
  testId,
}: AgentAvatarProps): JSX.Element {
  const parsed = agent
    ? parseLoomStatus(effectiveAgentStatus(agent))
    : undefined;
  const avatarColor = getAvatarColor(name);
  const label =
    (agent ? agentCompactAvatarLabel(agent) : "") ||
    getCompactAvatarInitials(name);
  const textColor = shouldUseWhiteText(avatarColor) ? "#fff" : "#1f2937";

  return (
    <span
      className={styles.root}
      data-size={compact ? "compact" : "default"}
      data-variant={variant}
      data-active={
        parsed?.type === "working" || parsed?.type === "planning"
          ? true
          : undefined
      }
      title={title}
      data-testid={testId}
    >
      <span
        className={styles.avatar}
        style={
          variant === "agent"
            ? { backgroundColor: avatarColor, color: textColor }
            : undefined
        }
        aria-label={`${name} avatar`}
      >
        {label}
      </span>
      {parsed && (
        <span
          className={styles.statusDot}
          style={{ backgroundColor: getStatusDotColor(parsed.type) }}
          aria-hidden="true"
        />
      )}
    </span>
  );
}
