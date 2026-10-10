// Ported from T3 Code apps/web/src/components/chat/ModelPickerSidebar.tsx at
// commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: one rail button per catalog provider (T3
// has one per provider instance), a Recent entry under Favorites, native
// title tooltips in place of Base UI's, and CSS modules in place of Tailwind.
// The selected indicator slides by transform in place of T3's top.

import { memo, useLayoutEffect, useRef, useState } from "react";
import type { PickerProvider } from "@/hooks/agents/useAgentModel";
import { ProviderIcon } from "./ProviderIcon";
import styles from "./ModelPicker.module.css";

/** A rail section: favorites, recent, or provider:<id>. */
export type PickerSection = "favorites" | "recent" | `provider:${string}`;

export const providerSection = (id: string): PickerSection => `provider:${id}`;

function RailButton({
  section,
  selected,
  label,
  onSelect,
  children,
}: {
  section: PickerSection;
  selected: boolean;
  label: string;
  onSelect: (s: PickerSection) => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={styles.railButton}
      data-model-picker-provider={section}
      data-selected={selected || undefined}
      aria-label={label}
      aria-pressed={selected}
      title={label}
      onClick={() => onSelect(section)}
    >
      {children}
    </button>
  );
}

export const ModelPickerSidebar = memo(function ModelPickerSidebar(props: {
  selected: PickerSection;
  providers: readonly PickerProvider[];
  onSelect: (section: PickerSection) => void;
}) {
  const railRef = useRef<HTMLDivElement>(null);
  const [indicatorTop, setIndicatorTop] = useState<number | null>(null);
  useLayoutEffect(() => {
    const item = [
      ...(railRef.current?.querySelectorAll<HTMLElement>(
        "[data-model-picker-provider]",
      ) ?? []),
    ].find((el) => el.dataset.modelPickerProvider === props.selected);
    setIndicatorTop(item ? item.offsetTop + item.offsetHeight / 2 - 10 : null);
  }, [props.selected, props.providers]);
  return (
    <div ref={railRef} className={styles.rail} data-model-picker-sidebar="true">
      {indicatorTop !== null && (
        <div
          className={styles.railIndicator}
          data-model-picker-selected-indicator="true"
          style={{ transform: `translateY(${indicatorTop}px)` }}
        />
      )}
      <RailButton
        section="favorites"
        selected={props.selected === "favorites"}
        label="Favorites"
        onSelect={props.onSelect}
      >
        <svg aria-hidden className={styles.railGlyph} viewBox="0 0 24 24">
          <path
            fill="currentColor"
            d="M12 2.5l2.9 6.1 6.6.8-4.9 4.6 1.3 6.5L12 17.3l-5.9 3.2 1.3-6.5-4.9-4.6 6.6-.8z"
          />
        </svg>
      </RailButton>
      <RailButton
        section="recent"
        selected={props.selected === "recent"}
        label="Recent"
        onSelect={props.onSelect}
      >
        <svg
          aria-hidden
          className={styles.railGlyph}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          strokeLinecap="round"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7v5l3 2" />
        </svg>
      </RailButton>
      <div className={styles.railDivider} aria-hidden="true" />
      {props.providers.map((p) => (
        <RailButton
          key={p.id}
          section={providerSection(p.id)}
          selected={props.selected === providerSection(p.id)}
          label={p.name}
          onSelect={props.onSelect}
        >
          <ProviderIcon providerId={p.id} providerName={p.name} size="lg" />
        </RailButton>
      ))}
    </div>
  );
});
