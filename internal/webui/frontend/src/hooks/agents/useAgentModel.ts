import { useCallback, useEffect, useMemo, useState } from "react";
import {
  getCustomModels,
  listHarnessModels,
  setCustomModels,
} from "@/api/agentsv1";
import type {
  Agent,
  CatalogModel,
  ModelCatalog,
  OptionDescriptor,
  OptionValue,
} from "@/api/agentsv1";

/** A catalog model with its provider, flattened for the picker. */
export interface PickerModel extends CatalogModel {
  providerId: string;
  providerName: string;
}

export interface PickerProvider {
  id: string;
  name: string;
}

/** The section workspace custom model ids are listed and added in (MCS3). */
export const CUSTOM_PROVIDER: PickerProvider = { id: "custom", name: "Custom" };

/**
 * The catalog's providers, then Custom when the catalog lists none of its
 * own, so a custom model id can be added from the picker. A catalog with no
 * providers yet (OpenCode still starting) gets none, so the picker still
 * opens on a real provider once they load.
 */
export function pickerProviders(
  catalog: ModelCatalog | null,
): PickerProvider[] {
  const ps = (catalog?.providers ?? []).map((p) => ({
    id: p.id,
    name: p.name,
  }));
  if (ps.length === 0 || ps.some((p) => p.id === CUSTOM_PROVIDER.id)) return ps;
  return [...ps, CUSTOM_PROVIDER];
}

/** The catalog's models in catalog order, each with its provider. */
export function catalogModels(catalog: ModelCatalog | null): PickerModel[] {
  return (catalog?.providers ?? []).flatMap((p) =>
    p.models.map((m) => ({ ...m, providerId: p.id, providerName: p.name })),
  );
}

/**
 * The model options PATCH saved (spec_json's Options, applied from the next
 * turn), as id and value. A malformed spec yields none.
 */
export function savedOptions(specJson: string | undefined): OptionValue[] {
  if (!specJson) return [];
  try {
    const spec = JSON.parse(specJson) as {
      Options?: { ID?: string; Value?: string }[];
    };
    return (spec.Options ?? [])
      .filter((o) => typeof o.ID === "string" && typeof o.Value === "string")
      .map((o) => ({ id: o.ID as string, value: o.Value as string }));
  } catch {
    return [];
  }
}

/**
 * The model's option descriptors with each current_value set to the saved
 * choice, if any; a boolean option's saved "true"/"false" becomes a boolean.
 */
export function descriptorsWithSaved(
  descriptors: readonly OptionDescriptor[],
  saved: readonly OptionValue[],
): OptionDescriptor[] {
  return descriptors.map((d) => {
    const s = saved.find((o) => o.id === d.id);
    if (!s) return d;
    if (d.type === "boolean") {
      return { ...d, current_value: s.value === true || s.value === "true" };
    }
    return { ...d, current_value: String(s.value) };
  });
}

/**
 * Which model the agent runs: its saved model, else the catalog default.
 * traitsModel is the model whose options apply: the same, or with neither
 * saved nor marked default (Claude picks per account), the first model, as
 * the server validates options against it.
 */
export function resolveAgentModel(
  models: readonly PickerModel[],
  agentModel: string | null,
): { model: PickerModel | null; traitsModel: PickerModel | null } {
  if (agentModel) {
    const m = models.find((x) => x.id === agentModel) ?? null;
    return { model: m, traitsModel: m };
  }
  const def = models.find((x) => x.is_default) ?? null;
  return { model: def, traitsModel: def ?? models[0] ?? null };
}

/**
 * Why the model and effort controls are off, or null when they are on. Only
 * the name of an unfinished single task can change (UI1's PATCH refuses the
 * rest). A running turn does not disable them: every harness applies a
 * change from the next turn.
 */
export function modelControlsDisabledReason(
  agent: Agent | null,
): string | null {
  if (!agent) return "Loading the agent";
  if (agent.mode === "single_task" && agent.state !== "finished") {
    return "A single task's model is fixed until it finishes";
  }
  return null;
}

/**
 * The waits before reading the catalog again while it lists no providers
 * or fails (about 30 s in all): OpenCode lists none until its service is
 * up, which on a fresh stack is after the chat first reads it.
 */
export const CATALOG_RETRY_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 15_000];

export interface UseAgentModelReturn {
  catalog: ModelCatalog | null;
  catalogError: string | null;
  models: PickerModel[];
  providers: PickerProvider[];
  /** The model in use, or null when it is not in the catalog. */
  model: PickerModel | null;
  /** The model's option descriptors with the saved choices applied. */
  descriptors: OptionDescriptor[];
  disabledReason: string | null;
  /** Reads the catalog again (the picker does on opening). */
  refresh: () => void;
  /** Adds or removes a workspace custom model id, then reads the catalog again. */
  addCustomModel: (id: string) => Promise<void>;
  removeCustomModel: (id: string) => Promise<void>;
}

/**
 * The agent's model catalog (its harness's GET /harnesses/{h}/models) and
 * the current model and options, from the agent as Get returns it, so they
 * survive a reload. A catalog with no providers, or a failed read, is read
 * again a few times (CATALOG_RETRY_DELAYS_MS), keeping what it has.
 */
export function useAgentModel(
  workspaceId: string,
  agent: Agent | null,
): UseAgentModelReturn {
  const harness = agent?.harness;
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [reads, setReads] = useState(0);
  const refresh = useCallback(() => setReads((n) => n + 1), []);

  useEffect(() => {
    setCatalog(null);
    setCatalogError(null);
  }, [workspaceId, harness]);

  useEffect(() => {
    if (!harness) return;
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const read = (attempt: number) => {
      const retry = () => {
        const delay = CATALOG_RETRY_DELAYS_MS[attempt];
        if (delay !== undefined)
          timer = setTimeout(() => read(attempt + 1), delay);
      };
      listHarnessModels(workspaceId, harness)
        .then((c) => {
          if (!live) return;
          setCatalog(c);
          setCatalogError(null);
          if (c.providers.length === 0) retry();
        })
        .catch((err) => {
          if (!live) return;
          setCatalogError(err instanceof Error ? err.message : String(err));
          retry();
        });
    };
    read(0);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [workspaceId, harness, reads]);

  const models = useMemo(() => catalogModels(catalog), [catalog]);
  const providers = useMemo(() => pickerProviders(catalog), [catalog]);
  // Read-modify-write of the harness's custom ids; a failure rejects for
  // the picker to show.
  const editCustom = useCallback(
    async (edit: (ids: string[]) => string[]) => {
      if (!harness) return;
      const ids = await getCustomModels(workspaceId, harness);
      await setCustomModels(workspaceId, harness, edit(ids));
      refresh();
    },
    [workspaceId, harness, refresh],
  );
  const addCustomModel = useCallback(
    (id: string) => editCustom((ids) => [...ids, id]),
    [editCustom],
  );
  const removeCustomModel = useCallback(
    (id: string) => editCustom((ids) => ids.filter((x) => x !== id)),
    [editCustom],
  );
  const { model, traitsModel } = useMemo(
    () => resolveAgentModel(models, agent?.model ?? null),
    [models, agent?.model],
  );
  const specJson = agent?.spec_json;
  const descriptors = useMemo(
    () =>
      descriptorsWithSaved(
        traitsModel?.option_descriptors ?? [],
        savedOptions(specJson),
      ),
    [traitsModel, specJson],
  );
  const disabledReason =
    modelControlsDisabledReason(agent) ??
    (catalogError ? `The model list is unavailable: ${catalogError}` : null);

  return {
    catalog,
    catalogError,
    models,
    providers,
    model,
    descriptors,
    disabledReason,
    refresh,
    addCustomModel,
    removeCustomModel,
  };
}
