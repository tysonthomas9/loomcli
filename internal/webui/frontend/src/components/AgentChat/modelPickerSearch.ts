// Ported from T3 Code apps/web/src/components/chat/modelPickerSearch.ts and
// packages/shared/src/searchRanking.ts (normalizeSearchQuery,
// scoreSubsequenceMatch, scoreQueryMatch) at commit 2daff8c25. Copyright (c)
// 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md. Changes: the
// provider fields are the catalog's provider id and name in place of T3's
// driver kind and instance display name.

export interface ModelPickerSearchableModel {
  /** Provider id from the catalog, e.g. "openai". */
  providerId: string;
  /** Provider display name from the catalog, e.g. "OpenAI". */
  providerName: string;
  name: string;
  id: string;
  isFavorite?: boolean;
}

const MODEL_PICKER_FAVORITE_SCORE_BOOST = 24;

export function normalizeSearchQuery(input: string): string {
  return input.trim().toLowerCase();
}

function scoreSubsequenceMatch(value: string, query: string): number | null {
  if (!query) return 0;
  let queryIndex = 0;
  let firstMatchIndex = -1;
  let previousMatchIndex = -1;
  let gapPenalty = 0;
  for (let valueIndex = 0; valueIndex < value.length; valueIndex += 1) {
    if (value[valueIndex] !== query[queryIndex]) continue;
    if (firstMatchIndex === -1) firstMatchIndex = valueIndex;
    if (previousMatchIndex !== -1) {
      gapPenalty += valueIndex - previousMatchIndex - 1;
    }
    previousMatchIndex = valueIndex;
    queryIndex += 1;
    if (queryIndex === query.length) {
      const spanPenalty = valueIndex - firstMatchIndex + 1 - query.length;
      const lengthPenalty = Math.min(64, value.length - query.length);
      return firstMatchIndex * 2 + gapPenalty * 3 + spanPenalty + lengthPenalty;
    }
  }
  return null;
}

function lengthPenalty(value: string, query: string): number {
  return Math.min(64, Math.max(0, value.length - query.length));
}

const BOUNDARY_MARKERS = [" ", "-", "_", "/"];

function findBoundaryMatchIndex(value: string, query: string): number | null {
  let bestIndex: number | null = null;
  for (const marker of BOUNDARY_MARKERS) {
    const index = value.indexOf(`${marker}${query}`);
    if (index === -1) continue;
    const matchIndex = index + marker.length;
    if (bestIndex === null || matchIndex < bestIndex) bestIndex = matchIndex;
  }
  return bestIndex;
}

/** Tiered match score (lower is better); both inputs pre-normalized. */
function scoreQueryMatch(
  value: string,
  query: string,
  base: number,
): number | null {
  if (!value || !query) return null;
  if (value === query) return base;
  if (value.startsWith(query)) return base + 2 + lengthPenalty(value, query);
  const boundaryIndex = findBoundaryMatchIndex(value, query);
  if (boundaryIndex !== null) {
    return base + 4 + boundaryIndex * 2 + lengthPenalty(value, query);
  }
  const includesIndex = value.indexOf(query);
  if (includesIndex !== -1) {
    return base + 6 + includesIndex * 2 + lengthPenalty(value, query);
  }
  if (query.length >= 3) {
    const fuzzy = scoreSubsequenceMatch(value, query);
    if (fuzzy !== null) return base + 100 + fuzzy;
  }
  return null;
}

export function buildModelPickerSearchText(
  model: ModelPickerSearchableModel,
): string {
  return normalizeSearchQuery(
    [model.name, model.id, model.providerId, model.providerName]
      .filter((v) => v.length > 0)
      .join(" "),
  );
}

function getModelPickerSearchFields(
  model: ModelPickerSearchableModel,
): string[] {
  return [
    normalizeSearchQuery(model.name),
    normalizeSearchQuery(model.id),
    normalizeSearchQuery(model.providerId),
    normalizeSearchQuery(model.providerName),
    buildModelPickerSearchText(model),
  ];
}

/**
 * Scores model against a tokenized query: every token must match some
 * field. Lower is better; null is no match. Favorites rank a little higher.
 */
export function scoreModelPickerSearch(
  model: ModelPickerSearchableModel,
  query: string,
): number | null {
  const tokens = normalizeSearchQuery(query)
    .split(/\s+/u)
    .filter((token) => token.length > 0);
  if (tokens.length === 0) return 0;
  const fields = getModelPickerSearchFields(model);
  let score = 0;
  for (const token of tokens) {
    let best: number | null = null;
    fields.forEach((field, index) => {
      const s = scoreQueryMatch(field, token, index * 10);
      if (s !== null && (best === null || s < best)) best = s;
    });
    if (best === null) return null;
    score += best;
  }
  return model.isFavorite ? score - MODEL_PICKER_FAVORITE_SCORE_BOOST : score;
}
