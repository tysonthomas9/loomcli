// Ported from T3 Code apps/web/src/components/chat/TraitsPicker.tsx
// (TraitsMenuContent, buildTraitsTriggerDisplay and TraitsPicker) at commit
// 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: the descriptors are the agent's catalog
// model's option_descriptors (UI1) with its saved choices; a choice is
// reported through onChange (a PATCH) in place of T3's composer draft store;
// no prompt-injected effort (ultrathink), fast-mode bolt, agent or plan
// options; a CSS-module menu in place of Base UI's Menu.

import { memo } from "react";
import type { OptionDescriptor } from "@/api/agentsv1";
import {
  ComposerControl,
  ComposerControlChevron,
  useComposerPopover,
} from "./ComposerControl";
import styles from "./ModelPicker.module.css";

export type TraitValue = string | boolean;

/** The label of a select descriptor's current choice, or null. */
export function currentChoiceLabel(d: OptionDescriptor): string | null {
  if (d.type !== "select") return null;
  const value =
    typeof d.current_value === "string"
      ? d.current_value
      : d.options?.find((o) => o.is_default)?.id;
  return d.options?.find((o) => o.id === value)?.label ?? value ?? null;
}

/** The trigger text: each option's current choice, joined by " · ". */
export function buildTraitsTriggerLabel(
  descriptors: readonly OptionDescriptor[],
): string {
  return descriptors
    .map((d) =>
      d.type === "boolean"
        ? `${d.label} ${d.current_value === true ? "On" : "Off"}`
        : currentChoiceLabel(d),
    )
    .filter((l): l is string => !!l)
    .join(" · ");
}

/**
 * Each option the model declares, as a labelled radio group: a select's
 * choices, or On and Off for a boolean. Picking one calls onChange and
 * onPicked (which closes the menu).
 */
export const TraitsMenuContent = memo(function TraitsMenuContent(props: {
  descriptors: readonly OptionDescriptor[];
  onChange: (id: string, value: TraitValue) => void;
  onPicked?: () => void;
}) {
  const pick = (d: OptionDescriptor, value: TraitValue) => {
    props.onPicked?.();
    if (value !== currentValue(d)) props.onChange(d.id, value);
  };
  return (
    <>
      {props.descriptors.map((d, index) => (
        <div key={d.id} role="group" aria-label={d.label}>
          {index > 0 && <div className={styles.menuDivider} />}
          <div className={styles.menuLabel}>{d.label}</div>
          {d.type === "select"
            ? (d.options ?? []).map((o) => (
                <button
                  key={o.id}
                  type="button"
                  role="menuitemradio"
                  aria-checked={currentValue(d) === o.id}
                  className={styles.menuItem}
                  onClick={() => pick(d, o.id)}
                >
                  <span className={styles.menuItemLine}>
                    {o.label}
                    {o.is_default && (
                      <span className={styles.badge}>Default</span>
                    )}
                  </span>
                  {o.description && (
                    <span className={styles.menuItemDescription}>
                      {o.description}
                    </span>
                  )}
                </button>
              ))
            : [true, false].map((on) => (
                <button
                  key={String(on)}
                  type="button"
                  role="menuitemradio"
                  aria-checked={currentValue(d) === on}
                  className={styles.menuItem}
                  onClick={() => pick(d, on)}
                >
                  {on ? "On" : "Off"}
                </button>
              ))}
        </div>
      ))}
    </>
  );
});

function currentValue(d: OptionDescriptor): TraitValue | undefined {
  if (d.type === "boolean") return d.current_value === true;
  return typeof d.current_value === "string"
    ? d.current_value
    : d.options?.find((o) => o.is_default)?.id;
}

/**
 * The effort (traits) control: shown only when the model declares options,
 * listing only the choices it declares.
 */
export const TraitsPicker = memo(function TraitsPicker(props: {
  descriptors: readonly OptionDescriptor[];
  disabledReason?: string | null;
  hint?: string | null;
  onChange: (id: string, value: TraitValue) => void;
}) {
  const { open, setOpen, rootRef } = useComposerPopover();
  if (props.descriptors.length === 0) return null;
  const label = buildTraitsTriggerLabel(props.descriptors);
  const disabled = Boolean(props.disabledReason);
  return (
    <div className={styles.popoverRoot} ref={rootRef}>
      <ComposerControl
        aria-label={`${props.descriptors.map((d) => d.label).join(", ")}: ${label}`}
        aria-haspopup="menu"
        aria-expanded={open}
        data-chat-traits-picker="true"
        disabled={disabled}
        title={props.disabledReason ?? props.hint ?? undefined}
        onClick={() => setOpen(!open)}
      >
        <span className={styles.triggerLabel}>{label}</span>
        <ComposerControlChevron />
      </ComposerControl>
      {open && !disabled && (
        <div className={styles.menu} role="menu">
          <TraitsMenuContent
            descriptors={props.descriptors}
            onChange={props.onChange}
            onPicked={() => setOpen(false)}
          />
        </div>
      )}
    </div>
  );
});
