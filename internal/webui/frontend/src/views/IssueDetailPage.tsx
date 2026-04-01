import { ErrorBoundary, IssueDetailView } from "@/components";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { Issue, IssueDetails } from "@/types";

export interface IssueDetailPageProps {
  issueDetails: Issue | IssueDetails | null;
  isLoading: boolean;
  error: string | null;
  previousView: ViewMode;
  selectedIssueId: string | null;
  onBack: () => void;
  onApprove: (issue: Issue) => Promise<void>;
  onReject: (issue: Issue, comment: string) => Promise<void>;
  onOpenInTerminal: (issue: Issue | IssueDetails) => void;
  onCopyLink: () => void;
  onNavigateToIssue: (issue: Issue) => void;
  onIssueUpdate: (issue: Issue) => void;
}

export function IssueDetailPage({
  issueDetails,
  isLoading,
  error,
  previousView,
  selectedIssueId,
  onBack,
  onApprove,
  onReject,
  onOpenInTerminal,
  onCopyLink,
  onNavigateToIssue,
  onIssueUpdate,
}: IssueDetailPageProps) {
  return (
    <ErrorBoundary resetOnChange={[selectedIssueId]}>
      <IssueDetailView
        issue={issueDetails}
        isLoading={isLoading}
        error={error}
        previousView={previousView}
        onBack={onBack}
        onApprove={onApprove}
        onReject={onReject}
        onOpenInTerminal={onOpenInTerminal}
        onCopyLink={onCopyLink}
        onNavigateToIssue={onNavigateToIssue}
        onIssueUpdate={onIssueUpdate}
      />
    </ErrorBoundary>
  );
}
