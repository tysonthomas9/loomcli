// Ported from T3 Code apps/web/src/components/chat/ProviderModelPicker.tsx at
// commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: a CSS-module popover above the composer
// in place of Base UI's Popover; the trigger shows the catalog model's name
// and provider icon; a disabled picker shows its reason as the title; no
// instance badges or page scroll lock.

import { memo } from "react";
import type { PickerModel, PickerProvider } from "@/hooks/agents/useAgentModel";
import {
  ComposerControl,
  ComposerControlChevron,
  useComposerPopover,
} from "./ComposerControl";
import { ModelPickerContent } from "./ModelPickerContent";
import { ProviderIcon } from "./ProviderIcon";
import styles from "./ModelPicker.module.css";

export const ProviderModelPicker = memo(function ProviderModelPicker(props: {
  harness: string;
  models: readonly PickerModel[];
  providers: readonly PickerProvider[];
  /** The model in use, or null when it is not in the catalog. */
  model: PickerModel | null;
  /** The agent's saved model id (shown when it is not in the catalog). */
  modelId: string | null;
  compact?: boolean;
  disabledReason?: string | null;
  /** A hint on the trigger, e.g. that a change applies from the next turn. */
  hint?: string | null;
  onModelChange: (modelId: string) => void;
}) {
  const { open, setOpen, rootRef } = useComposerPopover();
  const disabled = Boolean(props.disabledReason);
  const label = props.model?.name ?? props.modelId ?? "Default model";
  const unavailable = !props.model && props.modelId !== null;

  return (
    <div className={styles.popoverRoot} ref={rootRef}>
      <ComposerControl
        aria-label={`Model: ${label}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        data-chat-provider-model-picker="true"
        className={
          props.compact ? styles.pickerTriggerCompact : styles.pickerTrigger
        }
        disabled={disabled}
        title={props.disabledReason ?? props.hint ?? label}
        onClick={() => setOpen(!open)}
      >
        {props.model && (
          <ProviderIcon
            providerId={props.model.providerId}
            providerName={props.model.providerName}
          />
        )}
        <span className={styles.triggerLabel}>{label}</span>
        {unavailable && <span className={styles.badge}>Unavailable</span>}
        <ComposerControlChevron />
      </ComposerControl>
      {open && !disabled && (
        <div
          className={styles.popover}
          role="dialog"
          aria-label="Choose a model"
        >
          <ModelPickerContent
            harness={props.harness}
            models={props.models}
            providers={props.providers}
            modelId={props.model?.id ?? props.modelId}
            onRequestClose={() => setOpen(false)}
            onSelect={(id) => {
              setOpen(false);
              if (id !== props.model?.id) props.onModelChange(id);
            }}
          />
        </div>
      )}
    </div>
  );
});
