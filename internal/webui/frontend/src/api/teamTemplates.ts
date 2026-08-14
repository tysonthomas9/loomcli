import { get, post, wsUrl } from "@/api/common";
import type {
  TeamTemplateApplyResponse,
  TeamTemplateCatalogResponse,
} from "@/types/teamTemplate";

/** Fetch the code-shipped Team Template catalog. */
export function listTeamTemplates(): Promise<TeamTemplateCatalogResponse> {
  return get<TeamTemplateCatalogResponse>("/api/team-templates");
}

/** Apply one Team Template to an existing workspace. */
export function applyTeamTemplate(
  workspaceId: string,
  teamTemplateId: string,
): Promise<TeamTemplateApplyResponse> {
  return post<TeamTemplateApplyResponse>(
    wsUrl(
      workspaceId,
      `/team-templates/${encodeURIComponent(teamTemplateId)}/apply`,
    ),
    undefined,
    { timeout: 120_000 },
  );
}
