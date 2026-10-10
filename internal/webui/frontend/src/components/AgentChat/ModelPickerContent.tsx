// Ported from T3 Code apps/web/src/components/chat/ModelPickerContent.tsx and
// apps/web/src/components/chat/ModelListRow.tsx at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
// Changes: models come from the agent's harness catalog (providers in place
// of T3's provider instances); a plain listbox with arrow-key highlight in
// place of Base UI's combobox and LegendList; favorites and recent models
// are kept in localStorage; no locked-provider mode, legacy section or jump
// shortcuts; CSS modules in place of Tailwind. MCS3: the Custom section
// adds workspace custom model ids and its rows remove them (T3 keeps them in
// settings as customModels).

import { memo, useEffect, useMemo, useRef, useState } from "react";
import {
  CUSTOM_PROVIDER,
  type PickerModel,
  type PickerProvider,
} from "@/hooks/agents/useAgentModel";
import {
  buildModelPickerSearchText,
  scoreModelPickerSearch,
} from "./modelPickerSearch";
import { modelPrefKey, useModelPickerPrefs } from "./modelPickerPrefs";
import {
  ModelPickerSidebar,
  providerSection,
  type PickerSection,
} from "./ModelPickerSidebar";
import { ProviderIcon } from "./ProviderIcon";
import styles from "./ModelPicker.module.css";

const CUSTOM_SECTION = providerSection(CUSTOM_PROVIDER.id);

const errorText = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

/** Adds a custom model id; a refused one shows the server's error. */
function AddCustomModel({ onAdd }: { onAdd: (id: string) => Promise<void> }) {
  const [id, setId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const add = () => {
    const v = id.trim();
    if (!v || busy) return;
    setBusy(true);
    setError(null);
    onAdd(v)
      .then(() => setId(""))
      .catch((err) => setError(errorText(err)))
      .finally(() => setBusy(false));
  };
  return (
    // A div, not a form: the picker sits inside the composer's form.
    <div className={styles.customForm} data-model-picker-custom-form="true">
      <div className={styles.customRow}>
        <input
          className={styles.customInput}
          placeholder="provider/model id"
          aria-label="Custom model id"
          value={id}
          onChange={(e) => setId(e.target.value)}
          onKeyDown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
        />
        <button
          type="button"
          onClick={add}
          className={styles.customAdd}
          disabled={busy || !id.trim()}
        >
          Add
        </button>
      </div>
      {error && (
        <div className={styles.customError} role="alert">
          {error}
        </div>
      )}
    </div>
  );
}

export const ModelPickerContent = memo(function ModelPickerContent(props: {
  harness: string;
  models: readonly PickerModel[];
  providers: readonly PickerProvider[];
  /** The agent's current model id, or null for the harness default. */
  modelId: string | null;
  onSelect: (modelId: string) => void;
  onRequestClose: () => void;
  onAddCustom?: ((id: string) => Promise<void>) | undefined;
  onRemoveCustom?: ((id: string) => Promise<void>) | undefined;
}) {
  const { harness, models, providers, modelId, onSelect, onRemoveCustom } =
    props;
  const [removeError, setRemoveError] = useState<string | null>(null);
  const { prefs, toggleFavorite, addRecent } = useModelPickerPrefs();
  const keyOf = (m: PickerModel) => modelPrefKey(harness, m.id);
  const favorites = useMemo(() => new Set(prefs.favorites), [prefs.favorites]);
  const current = models.find((m) => m.id === modelId);
  const openingSection = (): PickerSection => {
    if (models.some((m) => favorites.has(modelPrefKey(harness, m.id)))) {
      return "favorites";
    }
    const p = current?.providerId ?? providers[0]?.id;
    return p ? providerSection(p) : "favorites";
  };
  const [section, setSection] = useState<PickerSection>(openingSection);
  // Opened before the catalog listed any provider: once it does, open on
  // the section it would have opened on, unless one was picked meanwhile.
  const sectionPicked = useRef(false);
  const hadProviders = useRef(providers.length > 0);
  useEffect(() => {
    if (hadProviders.current || providers.length === 0) return;
    hadProviders.current = true;
    if (!sectionPicked.current) setSection(openingSection());
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, on the first providers
  }, [providers]);
  const [query, setQuery] = useState("");
  const [highlighted, setHighlighted] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  useEffect(() => {
    searchRef.current?.focus({ preventScroll: true });
  }, []);

  const searching = query.trim().length > 0;
  const visible = useMemo((): PickerModel[] => {
    const fav = (m: PickerModel) => favorites.has(modelPrefKey(harness, m.id));
    if (searching) {
      return models
        .map((m) => ({
          m,
          score: scoreModelPickerSearch(
            {
              name: m.name,
              id: m.id,
              providerId: m.providerId,
              providerName: m.providerName,
              isFavorite: fav(m),
            },
            query,
          ),
          tie: buildModelPickerSearchText({
            name: m.name,
            id: m.id,
            providerId: m.providerId,
            providerName: m.providerName,
          }),
        }))
        .filter((r): r is typeof r & { score: number } => r.score !== null)
        .sort(
          (a, b) =>
            a.score - b.score ||
            Number(fav(b.m)) - Number(fav(a.m)) ||
            a.tie.localeCompare(b.tie),
        )
        .map((r) => r.m);
    }
    if (section === "favorites") return models.filter(fav);
    if (section === "recent") {
      return prefs.recent
        .map((k) => models.find((m) => modelPrefKey(harness, m.id) === k))
        .filter((m): m is PickerModel => m !== undefined);
    }
    const inProvider = models.filter(
      (m) => providerSection(m.providerId) === section,
    );
    // Favorites first, as T3's sortProviderModelItems groups them.
    return [...inProvider.filter(fav), ...inProvider.filter((m) => !fav(m))];
  }, [models, favorites, harness, searching, query, section, prefs.recent]);

  useEffect(() => setHighlighted(0), [query, section]);
  // T3's list scroll fades: a fade at each end with more to scroll to.
  const [fade, setFade] = useState({ top: false, bottom: false });
  const updateFade = () => {
    const el = listRef.current;
    if (!el) return;
    const rest = el.scrollHeight - el.clientHeight - el.scrollTop;
    setFade((f) =>
      f.top === el.scrollTop > 1 && f.bottom === rest > 1
        ? f
        : { top: el.scrollTop > 1, bottom: rest > 1 },
    );
  };
  useEffect(updateFade, [visible]);
  useEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${highlighted}"]`)
      ?.scrollIntoView?.({ block: "nearest" });
  }, [highlighted]);

  const select = (m: PickerModel) => {
    addRecent(keyOf(m));
    onSelect(m.id);
  };

  return (
    <div className={styles.pickerContent} data-model-picker-content="true">
      {!searching && providers.length > 0 && (
        <ModelPickerSidebar
          selected={section}
          providers={providers}
          onSelect={(s) => {
            sectionPicked.current = true;
            setSection(s);
            searchRef.current?.focus({ preventScroll: true });
          }}
        />
      )}
      <div className={styles.pickerMain}>
        <div className={styles.searchRow}>
          <svg
            aria-hidden
            className={styles.searchGlyph}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            strokeLinecap="round"
          >
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input
            ref={searchRef}
            className={styles.searchInput}
            placeholder="Search models..."
            aria-label="Search models"
            role="combobox"
            aria-expanded="true"
            aria-controls="model-picker-list"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                props.onRequestClose();
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                setHighlighted((i) => Math.min(i + 1, visible.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setHighlighted((i) => Math.max(i - 1, 0));
              } else if (e.key === "Enter") {
                e.preventDefault();
                const m = visible[highlighted];
                if (m) select(m);
              }
            }}
          />
        </div>
        {!searching && section === CUSTOM_SECTION && props.onAddCustom && (
          <AddCustomModel onAdd={props.onAddCustom} />
        )}
        {removeError && (
          <div className={styles.customError} role="alert">
            {removeError}
          </div>
        )}
        <ul
          ref={listRef}
          id="model-picker-list"
          className={styles.modelList}
          data-fade-top={fade.top || undefined}
          data-fade-bottom={fade.bottom || undefined}
          onScroll={updateFade}
          role="listbox"
          aria-label="Models"
        >
          {visible.map((m, index) => {
            const isFavorite = favorites.has(keyOf(m));
            return (
              <li
                key={m.id}
                role="option"
                aria-selected={m.id === current?.id}
                data-index={index}
                data-highlighted={index === highlighted || undefined}
                className={styles.modelRow}
                title={m.id}
                onMouseMove={() => setHighlighted(index)}
                onClick={() => select(m)}
              >
                <div className={styles.modelText}>
                  <div className={styles.modelNameLine}>
                    <span className={styles.modelName}>{m.name}</span>
                    {m.is_default && (
                      <span className={styles.badge}>Default</span>
                    )}
                  </div>
                  <div className={styles.modelProvider}>
                    <ProviderIcon
                      providerId={m.providerId}
                      providerName={m.providerName}
                      size="sm"
                    />
                    <span>{m.providerName}</span>
                  </div>
                </div>
                {m.source === "custom" && onRemoveCustom && (
                  <button
                    type="button"
                    className={styles.favoriteButton}
                    aria-label={`Remove custom model ${m.id}`}
                    title="Remove custom model"
                    onClick={(e) => {
                      e.stopPropagation();
                      setRemoveError(null);
                      onRemoveCustom(m.id).catch((err) =>
                        setRemoveError(errorText(err)),
                      );
                    }}
                  >
                    <svg
                      aria-hidden
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth={2}
                      strokeLinecap="round"
                    >
                      <path d="M6 6l12 12M18 6L6 18" />
                    </svg>
                  </button>
                )}
                <button
                  type="button"
                  className={styles.favoriteButton}
                  data-favorite={isFavorite || undefined}
                  aria-label={
                    isFavorite ? "Remove from favorites" : "Add to favorites"
                  }
                  title={
                    isFavorite ? "Remove from favorites" : "Add to favorites"
                  }
                  onClick={(e) => {
                    e.stopPropagation();
                    toggleFavorite(keyOf(m));
                  }}
                >
                  <svg aria-hidden viewBox="0 0 24 24">
                    <path
                      fill={isFavorite ? "currentColor" : "none"}
                      stroke="currentColor"
                      strokeWidth={2}
                      strokeLinejoin="round"
                      d="M12 2.5l2.9 6.1 6.6.8-4.9 4.6 1.3 6.5L12 17.3l-5.9 3.2 1.3-6.5-4.9-4.6 6.6-.8z"
                    />
                  </svg>
                </button>
              </li>
            );
          })}
        </ul>
        {visible.length === 0 && (
          <div className={styles.empty}>
            {searching
              ? "No models found"
              : section === "favorites"
                ? "No favorites yet: star a model to add it"
                : section === "recent"
                  ? "No recent models"
                  : section === CUSTOM_SECTION
                    ? "No custom models yet: add a model id above"
                    : "No models found"}
          </div>
        )}
      </div>
    </div>
  );
});
