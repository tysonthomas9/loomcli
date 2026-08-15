/**
 * Styles barrel exports for the loom web UI frontend.
 * Exports TypeScript color constants and helpers.
 */

export {
  StateColors,
  StatusColors,
  PriorityColors,
  SemanticColors,
  TypeColors,
  getStateColor,
  getPriorityColor,
  getStatusColor,
} from "./colors";

export {
  contrastRatio,
  relativeLuminance,
  worstRatioAgainstSurfaces,
  TEXT_TOKENS,
  NON_TEXT_TOKENS,
  WCAG_AA_TEXT,
  WCAG_AA_NON_TEXT,
} from "./contrast";

export type {
  StateColor,
  StatusColor,
  PriorityColor,
  SemanticColor,
  TypeColor,
} from "./colors";
