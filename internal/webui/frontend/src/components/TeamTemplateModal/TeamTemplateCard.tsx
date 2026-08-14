import type {
  TeamTemplate,
  TeamTemplateDisplayLabel,
} from "@/types/teamTemplate";

import styles from "./TeamTemplateModal.module.css";

const DISPLAY_LABELS: readonly TeamTemplateDisplayLabel[] = [
  "Developer",
  "QA",
  "Architecture",
];

const INTERACTIVE_TOOLTIP =
  "Interactive agent role — no background agent. It runs when you open its terminal from the agent rail.";

export interface TeamTemplateCardProps {
  teamTemplate: TeamTemplate;
  selected: boolean;
  onSelect: () => void;
}

export function TeamTemplateCard({
  teamTemplate,
  selected,
  onSelect,
}: TeamTemplateCardProps): JSX.Element {
  return (
    <button
      type="button"
      className={styles.card}
      data-selected={selected ? "true" : undefined}
      aria-pressed={selected}
      onClick={onSelect}
    >
      <span className={styles.cardTitle}>{teamTemplate.label}</span>
      <span className={styles.cardDescription}>{teamTemplate.description}</span>
      <span className={styles.agentRoleGroups}>
        {DISPLAY_LABELS.map((displayLabel) => {
          const agentRoles = teamTemplate.roles.filter(
            (agentRole) => agentRole.display_label === displayLabel,
          );
          if (agentRoles.length === 0) return null;
          return (
            <span className={styles.agentRoleGroup} key={displayLabel}>
              <span className={styles.agentRoleGroupLabel}>{displayLabel}</span>
              <span className={styles.agentRoleChips}>
                {agentRoles.map((agentRole) => {
                  const interactive = agentRole.kind === "interactive";
                  return (
                    <span
                      className={styles.agentRoleChip}
                      key={agentRole.name}
                      title={interactive ? INTERACTIVE_TOOLTIP : undefined}
                    >
                      {agentRole.name}
                      {interactive ? (
                        <span
                          aria-hidden="true"
                          className={styles.terminalMark}
                        >
                          ⌨
                        </span>
                      ) : null}
                    </span>
                  );
                })}
              </span>
            </span>
          );
        })}
      </span>
      <span className={styles.cardCounts}>
        {teamTemplate.roles.length} agent roles · {teamTemplate.agents.length}{" "}
        agents configured to run
      </span>
    </button>
  );
}
