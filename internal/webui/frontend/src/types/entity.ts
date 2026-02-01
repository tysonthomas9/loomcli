/**
 * Entity reference types for HOP (Human-Operated Protocol) tracking.
 */

import type { ISODateString } from './common';

/**
 * Structured reference to an entity (human, agent, or org).
 * Foundation for HOP entity tracking and CV chains.
 * Maps to Go types.EntityRef.
 */
export interface EntityRef {
  name?: string;
  platform?: string;
  org?: string;
  id?: string;
}

/**
 * Validation outcome values.
 */
export type ValidationOutcome = 'accepted' | 'rejected' | 'revision_requested';

/**
 * Validation record for work approval.
 * Maps to Go types.Validation.
 */
export interface Validation {
  validator?: EntityRef;
  outcome: ValidationOutcome;
  timestamp: ISODateString;
  score?: number;
}

/**
 * Bond reference for compound molecule lineage.
 * Maps to Go types.BondRef.
 */
export interface BondRef {
  source_id: string;
  bond_type: string;
  bond_point?: string;
}

/**
 * Bond type constants.
 */
export const BondTypeSequential = 'sequential';
export const BondTypeParallel = 'parallel';
export const BondTypeConditional = 'conditional';
export const BondTypeRoot = 'root';
