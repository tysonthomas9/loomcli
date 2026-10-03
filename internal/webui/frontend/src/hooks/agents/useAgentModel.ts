import { useEffect, useMemo, useState } from "react";
import { listHarnessModels } from "@/api/agentsv1";
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
}

/**
 * The agent's model catalog (its harness's GET /harnesses/{h}/models) and
 * the current model and options, from the agent as Get returns it, so they
 * survive a reload.
 */
export function useAgentModel(
  workspaceId: string,
  agent: Agent | null,
): UseAgentModelReturn {
  const harness = agent?.harness;
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [catalogError, setCatalogError] = useState<string | null>(null);

  useEffect(() => {
    if (!harness) return;
    let live = true;
    setCatalog(null);
    setCatalogError(null);
    listHarnessModels(workspaceId, harness)
      .then((c) => live && setCatalog(c))
      .catch((err) => {
        if (live)
          setCatalogError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      live = false;
    };
  }, [workspaceId, harness]);

  const models = useMemo(() => catalogModels(catalog), [catalog]);
  const providers = useMemo(
    () => (catalog?.providers ?? []).map((p) => ({ id: p.id, name: p.name })),
    [catalog],
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
  };
}
