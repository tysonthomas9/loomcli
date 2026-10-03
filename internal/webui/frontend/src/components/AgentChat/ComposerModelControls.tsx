import type { Agent, UpdateAgentBody } from "@/api/agentsv1";
import { useAgentModel } from "@/hooks/agents/useAgentModel";
import { CompactComposerControlsMenu } from "./CompactComposerControlsMenu";
import { ProviderModelPicker } from "./ProviderModelPicker";
import {
  TraitsMenuContent,
  TraitsPicker,
  type TraitValue,
} from "./TraitsPicker";
import styles from "./ModelPicker.module.css";

/** The composer width under which effort moves into the "…" menu (T3's). */
export const COMPOSER_FOOTER_COMPACT_BREAKPOINT_PX = 620;

/**
 * The composer bar's model and effort pickers, the same for every harness:
 * they read the agent's harness catalog, and a pick PATCHes the agent, which
 * applies it from the next turn. update rejects on failure after the chat
 * shows the error, so nothing else is needed here.
 */
export function ComposerModelControls({
  workspaceId,
  agent,
  compact,
  update,
}: {
  workspaceId: string;
  agent: Agent | null;
  compact: boolean;
  update: (body: UpdateAgentBody) => Promise<void>;
}) {
  const { models, providers, model, descriptors, disabledReason, refresh } =
    useAgentModel(workspaceId, agent);
  if (!agent) return null;
  const hint = agent.running_turn_id ? "Applies from the next turn" : null;
  const onModelChange = (id: string) =>
    void update({ model: id }).catch(() => {});
  const onTraitChange = (id: string, value: TraitValue) =>
    void update({ options: [{ id, value }] }).catch(() => {});

  return (
    <div className={styles.controls} data-testid="composer-model-controls">
      <ProviderModelPicker
        harness={agent.harness}
        models={models}
        providers={providers}
        model={model}
        modelId={agent.model}
        compact={compact}
        disabledReason={disabledReason}
        hint={hint}
        onModelChange={onModelChange}
        onOpen={refresh}
      />
      {compact ? (
        descriptors.length > 0 && (
          <CompactComposerControlsMenu disabledReason={disabledReason}>
            {(close) => (
              <TraitsMenuContent
                descriptors={descriptors}
                onChange={onTraitChange}
                onPicked={close}
              />
            )}
          </CompactComposerControlsMenu>
        )
      ) : (
        <TraitsPicker
          descriptors={descriptors}
          disabledReason={disabledReason}
          hint={hint}
          onChange={onTraitChange}
        />
      )}
      {disabledReason && (
        <span className={styles.disabledReason} role="note">
          {disabledReason}
        </span>
      )}
    </div>
  );
}
