import { createContext, useContext, type ReactNode } from "react";

import type { UseIssueSessionMapReturn } from "@/hooks/useIssueSessionMap";

type HasActiveSessionFn = (issueId: string) => boolean;

const IssueSessionContext = createContext<HasActiveSessionFn>(() => false);

export interface IssueSessionProviderProps {
  value: UseIssueSessionMapReturn;
  children: ReactNode;
}

export function IssueSessionProvider({
  value,
  children,
}: IssueSessionProviderProps): JSX.Element {
  return (
    <IssueSessionContext.Provider value={value.hasActiveSession}>
      {children}
    </IssueSessionContext.Provider>
  );
}

export function useHasActiveSession(): HasActiveSessionFn {
  return useContext(IssueSessionContext);
}
