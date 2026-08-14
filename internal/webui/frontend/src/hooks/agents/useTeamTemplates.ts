import { useCallback, useEffect, useRef, useState } from "react";

import { applyTeamTemplate, listTeamTemplates } from "@/api/teamTemplates";
import type {
  TeamTemplate,
  TeamTemplateApplyResponse,
} from "@/types/teamTemplate";

export interface UseTeamTemplatesResult {
  teamTemplates: readonly TeamTemplate[];
  isLoading: boolean;
  error: string | null;
  retryCatalog: () => void;
  apply: (teamTemplateId: string) => Promise<TeamTemplateApplyResponse>;
}

export function useTeamTemplates(
  workspaceId: string,
  enabled: boolean,
): UseTeamTemplatesResult {
  const [teamTemplates, setTeamTemplates] = useState<readonly TeamTemplate[]>(
    [],
  );
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [catalogRequest, setCatalogRequest] = useState(0);
  const requestGenerationRef = useRef(0);

  useEffect(() => {
    if (!enabled) return;
    const generation = ++requestGenerationRef.current;
    setIsLoading(true);
    setError(null);
    void listTeamTemplates().then(
      (response) => {
        if (requestGenerationRef.current !== generation) return;
        setTeamTemplates(response.templates);
        setIsLoading(false);
      },
      (reason: unknown) => {
        if (requestGenerationRef.current !== generation) return;
        setError(
          reason instanceof Error
            ? reason.message
            : "Failed to load Team Templates",
        );
        setIsLoading(false);
      },
    );
    return () => {
      requestGenerationRef.current += 1;
    };
  }, [enabled, catalogRequest]);

  const retryCatalog = useCallback(() => {
    setCatalogRequest((request) => request + 1);
  }, []);

  const apply = useCallback(
    (teamTemplateId: string) => applyTeamTemplate(workspaceId, teamTemplateId),
    [workspaceId],
  );

  return { teamTemplates, isLoading, error, retryCatalog, apply };
}
