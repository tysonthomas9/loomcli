export type TeamTemplateRoleKind = "worker" | "interactive";
export type TeamTemplateDisplayLabel = "Developer" | "QA" | "Architecture";

export interface TeamTemplateRole {
  name: string;
  kind: TeamTemplateRoleKind;
  display_label: TeamTemplateDisplayLabel;
  description: string;
}

export interface TeamTemplateAgent {
  name: string;
  role_name: string;
}

export interface TeamTemplate {
  id: string;
  label: string;
  description: string;
  revision: number;
  schema_version: number;
  roles: readonly TeamTemplateRole[];
  agents: readonly TeamTemplateAgent[];
}

export interface BuiltInTeamTemplate extends TeamTemplate {
  /** Worker agent role that receives the onboarding `architect` issue label. */
  architectRoleName: string;
}

export interface TeamTemplateCatalogResponse {
  templates: readonly TeamTemplate[];
}

export type TeamTemplateStepAction =
  | "created"
  | "skipped_match"
  | "skipped_diverged"
  | "failed";

export interface TeamTemplateStepResult {
  entity: "role" | "agent";
  name: string;
  action: TeamTemplateStepAction;
  fields?: string[];
  error?: string;
}

export interface TeamTemplateApplyReport {
  template_id: string;
  revision: number;
  schema_version: number;
  workspace_key: string;
  dry_run: boolean;
  steps: readonly TeamTemplateStepResult[];
  created: number;
  skipped: number;
  diverged: number;
  failed: number;
  warnings?: readonly string[];
  materialized: number;
}

// The apply endpoint is synchronous: TeamTemplateApplyResponse in
// api/openapi.yaml declares status as const "done" and requires report.
export interface TeamTemplateApplyResponse {
  status: "done";
  report: TeamTemplateApplyReport;
}

export interface TeamTemplateApplyCounts {
  created: number;
  skipped: number;
  diverged: number;
  failed: number;
}

export interface TeamTemplateBreadcrumb {
  templateId: string;
  revision: number;
  ts: number;
  counts: TeamTemplateApplyCounts;
}
