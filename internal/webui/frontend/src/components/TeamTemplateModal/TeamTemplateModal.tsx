import { useEffect, useMemo, useRef, useState } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { useTeamTemplates } from "@/hooks/agents";
import type {
  TeamTemplate,
  TeamTemplateApplyReport,
} from "@/types/teamTemplate";
import {
  teamTemplateBreadcrumbFromReport,
  writeTeamTemplateBreadcrumb,
} from "@/utils/teamTemplateState";
import {
  BUILT_IN_TEAM_TEMPLATES,
  builtInTeamTemplateById,
} from "@/utils/teamTemplates";

import { TeamTemplateCard } from "./TeamTemplateCard";
import { TeamTemplateReport } from "./TeamTemplateReport";
import styles from "./TeamTemplateModal.module.css";

export interface TeamTemplateModalProps {
  isOpen: boolean;
  workspaceId: string;
  workspaceName: string;
  detectedTeamTemplateId?: string;
  onClose: () => void;
  onApplyStateChange?: (isApplying: boolean) => void;
  onApplied?: (report: TeamTemplateApplyReport) => void;
}

export function TeamTemplateModal({
  isOpen,
  workspaceId,
  workspaceName,
  detectedTeamTemplateId,
  onClose,
  onApplyStateChange,
  onApplied,
}: TeamTemplateModalProps): JSX.Element | null {
  const { teamTemplates, isLoading, error, retryCatalog, apply } =
    useTeamTemplates(workspaceId, isOpen);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [report, setReport] = useState<TeamTemplateApplyReport | null>(null);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [isApplying, setIsApplying] = useState(false);
  const [runningJobId, setRunningJobId] = useState<string | null>(null);
  const [retryFocusNames, setRetryFocusNames] = useState<ReadonlySet<string>>();
  const wasOpenRef = useRef(false);

  const displayTemplates =
    teamTemplates.length > 0 ? teamTemplates : BUILT_IN_TEAM_TEMPLATES;
  const selectedTeamTemplate = useMemo(
    () =>
      displayTemplates.find((teamTemplate) => teamTemplate.id === selectedId),
    [displayTemplates, selectedId],
  );
  const reportTeamTemplate = report
    ? (displayTemplates.find(
        (teamTemplate) => teamTemplate.id === report.template_id,
      ) ?? builtInTeamTemplateById(report.template_id))
    : undefined;

  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }
    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    setSelectedId(null);
    setReport(null);
    setApplyError(null);
    setIsApplying(false);
    setRunningJobId(null);
    setRetryFocusNames(undefined);
  }, [isOpen]);

  const handleClose = (): void => {
    if (!isApplying) onClose();
  };

  const submit = async (teamTemplate: TeamTemplate, retry: boolean) => {
    if (isApplying) return;
    if (retry && report) {
      setRetryFocusNames(
        new Set(
          report.steps
            .filter((step) => step.action === "failed")
            .map((step) => step.name),
        ),
      );
    } else {
      setRetryFocusNames(undefined);
    }
    setIsApplying(true);
    setApplyError(null);
    setRunningJobId(null);
    onApplyStateChange?.(true);
    try {
      const response = await apply(teamTemplate.id);
      if (response.status === "running") {
        setRunningJobId(response.job_id);
        return;
      }
      setReport(response.report);
      writeTeamTemplateBreadcrumb(
        workspaceId,
        teamTemplateBreadcrumbFromReport(response.report),
      );
      onApplied?.(response.report);
    } catch (reason) {
      setApplyError(
        reason instanceof Error
          ? reason.message
          : "Failed to apply Team Template",
      );
    } finally {
      setIsApplying(false);
      onApplyStateChange?.(false);
    }
  };

  const footer =
    report && reportTeamTemplate ? (
      <div className={styles.footerActions}>
        {report.failed > 0 ? (
          <button
            type="button"
            className={aetherModalStyles.linkButton}
            onClick={() => void submit(reportTeamTemplate, true)}
            disabled={isApplying}
          >
            {isApplying ? "Retrying…" : "Retry failed steps"}
          </button>
        ) : null}
        <button
          type="button"
          className={aetherModalStyles.primaryButton}
          onClick={handleClose}
          disabled={isApplying}
        >
          Done
        </button>
      </div>
    ) : runningJobId ? (
      <button
        type="button"
        className={aetherModalStyles.primaryButton}
        onClick={handleClose}
      >
        Close
      </button>
    ) : (
      <div className={styles.pickerFooter}>
        <button
          type="button"
          className={styles.blankButton}
          onClick={handleClose}
          disabled={isApplying}
        >
          Blank — keep this workspace as-is
        </button>
        <button
          type="button"
          className={aetherModalStyles.primaryButton}
          onClick={() =>
            selectedTeamTemplate && void submit(selectedTeamTemplate, false)
          }
          disabled={
            !selectedTeamTemplate || isApplying || isLoading || Boolean(error)
          }
        >
          {isApplying
            ? "Applying…"
            : `Apply to ${workspaceName || workspaceId}`}
        </button>
      </div>
    );

  return (
    <AetherModal
      isOpen={isOpen}
      title="Set up your team"
      ariaLabel="Set up your team"
      onClose={handleClose}
      disableOverlayDismiss={isApplying}
      showCloseButton={!isApplying}
      dialogClassName={`${aetherModalStyles.dialogWide} ${styles.dialog}`}
      footer={footer}
    >
      {isApplying ? (
        <div className={styles.progressBlock} role="status" aria-live="polite">
          <span className={styles.spinner} aria-hidden="true" />
          <strong>Setting up your team</strong>
          <span>Creating agent roles, then agents…</span>
        </div>
      ) : report && reportTeamTemplate ? (
        <div className={styles.reportResultView}>
          <TeamTemplateReport
            teamTemplate={reportTeamTemplate}
            report={report}
            {...(retryFocusNames ? { retryFocusNames } : {})}
          />
          {applyError ? (
            <p className={styles.applyError} role="alert">
              {applyError}
            </p>
          ) : null}
        </div>
      ) : runningJobId ? (
        <div className={styles.progressBlock} role="status">
          <strong>Team setup is still in progress</strong>
          <span>Background job {runningJobId} was accepted.</span>
        </div>
      ) : (
        <div className={styles.picker}>
          <p className={styles.intro}>
            Choose a built-in team for this workspace. Re-applying is safe and
            only adds what&apos;s missing.
          </p>
          {detectedTeamTemplateId ? (
            <p className={styles.matchCopy}>
              This workspace already has some of these — re-apply is safe and
              only adds what&apos;s missing.
            </p>
          ) : null}
          {error ? (
            <div className={styles.errorBlock} role="alert">
              <span>{error}</span>
              <button type="button" onClick={retryCatalog}>
                Retry catalog
              </button>
            </div>
          ) : null}
          <div className={styles.cardGrid} aria-busy={isLoading}>
            {displayTemplates.map((teamTemplate) => (
              <TeamTemplateCard
                key={teamTemplate.id}
                teamTemplate={teamTemplate}
                selected={selectedId === teamTemplate.id}
                onSelect={() => setSelectedId(teamTemplate.id)}
              />
            ))}
          </div>
          {isLoading ? (
            <p className={styles.catalogStatus} role="status">
              Checking the Team Template catalog…
            </p>
          ) : null}
          {applyError ? (
            <p className={styles.applyError} role="alert">
              {applyError}
            </p>
          ) : null}
        </div>
      )}
    </AetherModal>
  );
}
