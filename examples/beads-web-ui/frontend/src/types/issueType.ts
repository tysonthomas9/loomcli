/**
 * Issue type definitions.
 */

/**
 * Built-in issue types (core work types).
 */
export type KnownIssueType = 'bug' | 'feature' | 'task' | 'epic' | 'chore';

/**
 * Issue type that allows custom types.
 * Built-in types are type-checked, custom types are allowed via string.
 */
export type IssueType = KnownIssueType | (string & {});

/**
 * Issue type constants for type-safe usage.
 */
export const TypeBug: IssueType = 'bug';
export const TypeFeature: IssueType = 'feature';
export const TypeTask: IssueType = 'task';
export const TypeEpic: IssueType = 'epic';
export const TypeChore: IssueType = 'chore';

/**
 * All known issue type values for validation.
 */
export const KNOWN_ISSUE_TYPES: readonly KnownIssueType[] = [
  'bug',
  'feature',
  'task',
  'epic',
  'chore',
] as const;

/**
 * Type guard to check if an issue type is a known built-in type.
 */
export function isKnownIssueType(type: string): type is KnownIssueType {
  return KNOWN_ISSUE_TYPES.includes(type as KnownIssueType);
}

/**
 * Type guard to check if a value is a valid issue type (non-empty string).
 */
export function isValidIssueType(type: unknown): type is IssueType {
  return typeof type === 'string' && type.length > 0;
}
