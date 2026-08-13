package runcapture

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const (
	maxCaptureArtifacts = 256
	defaultArchiveLimit = 50
	maxArchiveLimit     = 100
)

type Service struct {
	executions   execution.TaskRunQueries
	interactions interaction.SessionQueries
	artifacts    artifacts.QueryAPI
}

type selectedEvidence struct {
	value   Evidence
	attempt int
}

var _ API = (*Service)(nil)

func New(
	executions execution.TaskRunQueries,
	interactions interaction.SessionQueries,
	artifactQueries artifacts.QueryAPI,
) (*Service, error) {
	if executions == nil || interactions == nil || artifactQueries == nil {
		return nil, fmt.Errorf("compose Run Capture: lifecycle-owner and Artifacts queries are required: %w", ErrUnavailable)
	}
	return &Service{executions: executions, interactions: interactions, artifacts: artifactQueries}, nil
}

func (service *Service) Get(ctx context.Context, query Query) (*RunCapture, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.executions == nil || service.interactions == nil || service.artifacts == nil {
		return nil, ErrUnavailable
	}
	capture, filter, transcriptID, err := service.ownerCapture(ctx, query)
	if err != nil {
		return nil, err
	}
	return service.loadEvidence(ctx, capture, filter, transcriptID, query)
}

func (service *Service) loadEvidence(
	ctx context.Context,
	capture *RunCapture,
	filter artifacts.SearchFilter,
	transcriptID string,
	query Query,
) (*RunCapture, error) {
	values, err := service.artifacts.ListArtifacts(ctx, artifacts.SearchQuery{
		WorkspaceKey: query.WorkspaceKey,
		Filter:       filter,
	})
	if err != nil {
		return nil, mapArtifactError(err)
	}
	selected, err := selectArtifacts(values, query, transcriptID, capture.OwnerID)
	if err != nil {
		return nil, err
	}
	if err := appendSelectedEvidence(capture, selected); err != nil {
		return nil, err
	}
	return cloneCapture(capture), nil
}

func selectArtifacts(
	values []*artifacts.Artifact,
	query Query,
	transcriptID string,
	ownerID string,
) (map[artifacts.EvidenceKind]selectedEvidence, error) {
	selected := make(map[artifacts.EvidenceKind]selectedEvidence, len(values))
	for _, artifact := range values {
		if err := validateOwnedArtifact(artifact, query, transcriptID); err != nil {
			return nil, err
		}
		kind, err := artifacts.EvidenceKindForArtifactType(artifact.Type)
		if err != nil {
			if errors.Is(err, artifacts.ErrInvalid) {
				// A TaskRun can own delivery or runner artifacts that are not one
				// of the durable evidence facets. Run Capture is a projection over
				// evidence, not a second view of the complete Artifact aggregate.
				continue
			}
			return nil, errors.Join(ErrInvalidPersistedState, err)
		}
		attempt, err := evidenceAttempt(query.OwnerKind, artifact)
		if err != nil {
			return nil, err
		}
		if current, ok := selected[kind]; ok {
			if query.OwnerKind != OwnerExecution || current.attempt == attempt {
				return nil, fmt.Errorf("capture %q has multiple %s artifacts for attempt %d: %w",
					ownerID, kind, attempt, ErrInvalidPersistedState)
			}
			if current.attempt > attempt {
				continue
			}
		}
		selected[kind] = selectedEvidence{value: evidenceFromArtifact(kind, artifact), attempt: attempt}
	}
	return selected, nil
}

func appendSelectedEvidence(
	capture *RunCapture,
	selected map[artifacts.EvidenceKind]selectedEvidence,
) error {
	capture.Evidence = make([]Evidence, 0, len(selected))
	for _, kind := range []artifacts.EvidenceKind{
		artifacts.EvidencePrompt,
		artifacts.EvidenceTranscript,
		artifacts.EvidenceDiff,
		artifacts.EvidenceLog,
		artifacts.EvidenceReport,
		artifacts.EvidenceScrollback,
	} {
		ownerEvidence, ownerAttempt, hasOwnerFailure, ownerErr := evidenceFailureFromOwnerMetadata(
			kind,
			capture.ownerEvidenceMetadata,
		)
		if ownerErr != nil {
			return ownerErr
		}
		current, hasCurrent := selected[kind]
		if hasOwnerFailure && (!hasCurrent || ownerAttempt > current.attempt) {
			current = selectedEvidence{value: ownerEvidence, attempt: ownerAttempt}
			hasCurrent = true
		}
		if hasCurrent {
			evidence := current
			capture.Evidence = append(capture.Evidence, evidence.value)
		}
	}
	return nil
}

func evidenceFailureFromOwnerMetadata(
	kind artifacts.EvidenceKind,
	metadata map[string]string,
) (Evidence, int, bool, error) {
	status := strings.TrimSpace(metadata[artifacts.OwnerEvidenceCaptureStatusKey(kind)])
	if status == "" || status == "finalized" {
		return Evidence{}, 0, false, nil
	}
	if status != "capture_failed" {
		return Evidence{}, 0, false, fmt.Errorf("owner evidence %s has invalid status %q: %w",
			kind, status, ErrInvalidPersistedState)
	}
	attempt := 1
	if raw := strings.TrimSpace(metadata[artifacts.OwnerEvidenceAttemptKey(kind)]); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return Evidence{}, 0, false, fmt.Errorf("owner evidence %s has invalid attempt %q: %w",
				kind, raw, ErrInvalidPersistedState)
		}
		attempt = value
	}
	failureClass := strings.TrimSpace(metadata[artifacts.OwnerEvidenceFailureClassKey(kind)])
	if failureClass == "" {
		return Evidence{}, 0, false, fmt.Errorf("owner evidence %s capture failure has no class: %w",
			kind, ErrInvalidPersistedState)
	}
	return Evidence{Kind: kind, State: EvidenceCaptureFailed, FailureClass: failureClass}, attempt, true, nil
}

func evidenceAttempt(ownerKind OwnerKind, artifact *artifacts.Artifact) (int, error) {
	if ownerKind != OwnerExecution {
		return 1, nil
	}
	raw := strings.TrimSpace(artifact.Metadata["task_run_attempt"])
	if raw == "" {
		return 1, nil
	}
	attempt, err := strconv.Atoi(raw)
	if err != nil || attempt < 1 {
		return 0, fmt.Errorf("artifact %q has invalid task_run_attempt %q: %w",
			artifact.ArtifactID, raw, ErrInvalidPersistedState)
	}
	return attempt, nil
}

//nolint:gocognit,cyclop,funlen // The bounded merge keeps both owner queries and global ordering visible.
func (service *Service) List(ctx context.Context, query ArchiveQuery) ([]*RunCapture, error) {
	query, err := normalizeArchiveQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.executions == nil || service.interactions == nil || service.artifacts == nil {
		return nil, ErrUnavailable
	}
	if query.OwnerID != "" {
		capture, getErr := service.Get(ctx, Query{
			WorkspaceKey: query.WorkspaceKey, OwnerKind: query.OwnerKind, OwnerID: query.OwnerID,
			AgentID: query.AgentID, WorkItemID: query.WorkItemID,
		})
		if errors.Is(getErr, ErrNotFound) {
			return []*RunCapture{}, nil
		}
		if getErr != nil {
			return nil, getErr
		}
		return []*RunCapture{capture}, nil
	}

	results := make([]*RunCapture, 0, query.Limit)
	if (query.OwnerKind == "" && query.AgentID == "") || query.OwnerKind == OwnerExecution {
		runs, listErr := service.executions.ListTaskRuns(ctx, execution.TaskRunArchiveQuery{
			WorkspaceKey: query.WorkspaceKey, WorkItemID: query.WorkItemID, Limit: query.Limit,
		})
		if listErr != nil {
			return nil, mapOwnerError(listErr)
		}
		for _, run := range runs {
			capture, filter, snapshotErr := executionCapture(run, query.WorkspaceKey, query.WorkItemID)
			if snapshotErr != nil {
				return nil, snapshotErr
			}
			loaded, loadErr := service.loadEvidence(ctx, capture, filter, "", Query{
				WorkspaceKey: capture.WorkspaceKey, OwnerKind: OwnerExecution,
				OwnerID: capture.OwnerID, WorkItemID: capture.WorkItemID,
			})
			if loadErr != nil {
				return nil, loadErr
			}
			results = append(results, loaded)
		}
	}
	if query.OwnerKind == "" || query.OwnerKind == OwnerInteraction {
		sessions, listErr := service.interactions.ListSessions(ctx, interaction.SessionArchiveQuery{
			WorkspaceKey: query.WorkspaceKey, AgentID: query.AgentID,
			WorkItemID: query.WorkItemID, Limit: query.Limit,
		})
		if listErr != nil {
			return nil, mapOwnerError(listErr)
		}
		for _, session := range sessions {
			capture, filter, transcriptID, snapshotErr := interactionCapture(
				session, query.WorkspaceKey, query.AgentID, query.WorkItemID,
			)
			if snapshotErr != nil {
				return nil, snapshotErr
			}
			loaded, loadErr := service.loadEvidence(ctx, capture, filter, transcriptID, Query{
				WorkspaceKey: capture.WorkspaceKey, OwnerKind: OwnerInteraction,
				OwnerID: capture.OwnerID, AgentID: capture.AgentID, WorkItemID: capture.WorkItemID,
			})
			if loadErr != nil {
				return nil, loadErr
			}
			results = append(results, loaded)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := archiveTimestamp(results[i]), archiveTimestamp(results[j])
		if left.Equal(right) {
			if results[i].OwnerKind == results[j].OwnerKind {
				return results[i].OwnerID < results[j].OwnerID
			}
			return results[i].OwnerKind < results[j].OwnerKind
		}
		return left.After(right)
	})
	if len(results) > query.Limit {
		results = results[:query.Limit]
	}
	return results, nil
}

func (service *Service) Transcript(ctx context.Context, query Query) (*TranscriptEvidence, error) {
	content, err := service.ReadEvidence(ctx, query, artifacts.EvidenceTranscript)
	if err != nil {
		return nil, err
	}
	result := &TranscriptEvidence{Capture: content.Capture, Evidence: content.Evidence}
	if result.Evidence.State != EvidenceFinalized && result.Evidence.State != EvidenceTruncated {
		return result, nil
	}
	events, _, err := artifacts.DecodeCanonicalTranscript(content.Content, artifacts.MaxEvidenceCaptureBytes, artifacts.MaxTranscriptEvents)
	if err != nil {
		result.Evidence.State = EvidenceCorrupt
		result.Evidence.FailureClass = "canonical_transcript_invalid"
		return result, nil
	}
	result.Events = append([]artifacts.TranscriptEvent(nil), events...)
	return result, nil
}

func (service *Service) ReadEvidence(
	ctx context.Context,
	query Query,
	kind artifacts.EvidenceKind,
) (*EvidenceContent, error) {
	if !validEvidenceKind(kind) {
		return nil, ErrInvalid
	}
	capture, err := service.Get(ctx, query)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range capture.Evidence {
		if capture.Evidence[i].Kind != kind {
			continue
		}
		if index >= 0 {
			return nil, fmt.Errorf("capture %q has multiple %s artifacts: %w", capture.OwnerID, kind, ErrInvalidPersistedState)
		}
		index = i
	}
	if index < 0 {
		return &EvidenceContent{Capture: capture, Evidence: Evidence{Kind: kind, State: EvidenceMissing}}, nil
	}
	result := &EvidenceContent{Capture: capture, Evidence: capture.Evidence[index]}
	if result.Evidence.State != EvidenceFinalized && result.Evidence.State != EvidenceTruncated {
		return result, nil
	}
	value, err := service.artifacts.ReadArtifactContent(ctx, artifacts.Query{
		WorkspaceKey: capture.WorkspaceKey, ArtifactID: result.Evidence.ArtifactID,
	})
	if err != nil {
		if errors.Is(err, artifacts.ErrContentUnavailable) || errors.Is(err, artifacts.ErrNotFound) {
			result.Evidence.State = EvidenceContentUnavailable
			return result, nil
		}
		if errors.Is(err, artifacts.ErrEvidenceCorrupt) {
			result.Evidence.State = EvidenceCorrupt
			result.Evidence.FailureClass = "durable_content_integrity"
			return result, nil
		}
		return nil, mapArtifactError(err)
	}
	result.Content = append([]byte(nil), value...)
	return result, nil
}

func validEvidenceKind(kind artifacts.EvidenceKind) bool {
	switch kind {
	case artifacts.EvidencePrompt, artifacts.EvidenceTranscript, artifacts.EvidenceDiff,
		artifacts.EvidenceLog, artifacts.EvidenceReport, artifacts.EvidenceScrollback:
		return true
	default:
		return false
	}
}

func (service *Service) ownerCapture(
	ctx context.Context,
	query Query,
) (*RunCapture, artifacts.SearchFilter, string, error) {
	switch query.OwnerKind {
	case OwnerExecution:
		run, err := service.executions.GetTaskRun(ctx, query.WorkspaceKey, query.OwnerID)
		if err != nil {
			return nil, artifacts.SearchFilter{}, "", mapOwnerError(err)
		}
		capture, filter, err := executionCapture(run, query.WorkspaceKey, query.WorkItemID)
		return capture, filter, "", err
	case OwnerInteraction:
		session, err := service.interactions.GetSession(ctx, query.WorkspaceKey, query.OwnerID)
		if err != nil {
			return nil, artifacts.SearchFilter{}, "", mapOwnerError(err)
		}
		return interactionCapture(session, query.WorkspaceKey, query.AgentID, query.WorkItemID)
	default:
		return nil, artifacts.SearchFilter{}, "", ErrInvalid
	}
}

func executionCapture(run *execution.TaskRun, workspace, workItemID string) (*RunCapture, artifacts.SearchFilter, error) {
	if run == nil || run.WorkspaceKey != workspace || strings.TrimSpace(run.TaskRunID) == "" ||
		(workItemID != "" && run.WorkItemID != workItemID) {
		return nil, artifacts.SearchFilter{}, ErrNotFound
	}
	capture := &RunCapture{
		WorkspaceKey: run.WorkspaceKey, OwnerKind: OwnerExecution, OwnerID: run.TaskRunID,
		WorkItemID: run.WorkItemID, Status: string(run.Status), StartedAt: cloneTime(run.StartedAt),
		FinishedAt: cloneTime(run.FinishedAt), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		ownerEvidenceMetadata: cloneMetadata(run.RuntimeMetadata),
	}
	return capture, artifacts.SearchFilter{OwnerType: artifacts.OwnerTaskRun, OwnerID: run.TaskRunID, Limit: maxCaptureArtifacts}, nil
}

func interactionCapture(
	session *interaction.AgentSession,
	workspace, agentID, workItemID string,
) (*RunCapture, artifacts.SearchFilter, string, error) {
	if session == nil || session.WorkspaceKey != workspace || strings.TrimSpace(session.SessionID) == "" ||
		(agentID != "" && session.AgentID != agentID) ||
		(workItemID != "" && session.TaskID != workItemID) {
		return nil, artifacts.SearchFilter{}, "", ErrNotFound
	}
	started := session.StartedAt
	capture := &RunCapture{
		WorkspaceKey: session.WorkspaceKey, OwnerKind: OwnerInteraction, OwnerID: session.SessionID,
		AgentID: session.AgentID, WorkItemID: session.TaskID, Status: string(session.Status),
		StartedAt: &started, FinishedAt: cloneTime(session.FinishedAt),
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		ownerEvidenceMetadata: cloneMetadata(session.Metadata),
	}
	return capture, artifacts.SearchFilter{OwnerType: artifacts.OwnerSession, OwnerID: session.SessionID, Limit: maxCaptureArtifacts}, strings.TrimSpace(session.TranscriptArtifactID), nil
}

func normalizeArchiveQuery(query ArchiveQuery) (ArchiveQuery, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.OwnerKind = OwnerKind(strings.TrimSpace(string(query.OwnerKind)))
	query.OwnerID = strings.TrimSpace(query.OwnerID)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.WorkItemID = strings.TrimSpace(query.WorkItemID)
	if query.Limit == 0 {
		query.Limit = defaultArchiveLimit
	}
	if query.WorkspaceKey == "" || query.Limit < 1 || query.Limit > maxArchiveLimit ||
		(query.OwnerKind != "" && query.OwnerKind != OwnerExecution && query.OwnerKind != OwnerInteraction) ||
		(query.OwnerID != "" && query.OwnerKind == "") ||
		(query.AgentID != "" && query.OwnerKind == OwnerExecution) {
		return ArchiveQuery{}, ErrInvalid
	}
	return query, nil
}

func archiveTimestamp(capture *RunCapture) time.Time {
	if capture == nil {
		return time.Time{}
	}
	if capture.StartedAt != nil && !capture.StartedAt.IsZero() {
		return *capture.StartedAt
	}
	return capture.CreatedAt
}

func normalizeQuery(query Query) (Query, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.OwnerKind = OwnerKind(strings.TrimSpace(string(query.OwnerKind)))
	query.OwnerID = strings.TrimSpace(query.OwnerID)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.WorkItemID = strings.TrimSpace(query.WorkItemID)
	if query.WorkspaceKey == "" || query.OwnerID == "" ||
		(query.OwnerKind != OwnerExecution && query.OwnerKind != OwnerInteraction) {
		return Query{}, ErrInvalid
	}
	return query, nil
}

func validateOwnedArtifact(artifact *artifacts.Artifact, query Query, transcriptID string) error {
	wantType := artifacts.OwnerTaskRun
	if query.OwnerKind == OwnerInteraction {
		wantType = artifacts.OwnerSession
	}
	if artifact == nil || artifact.WorkspaceKey != query.WorkspaceKey || artifact.OwnerType != wantType ||
		artifact.OwnerID != query.OwnerID || strings.TrimSpace(artifact.ArtifactID) == "" {
		return ErrInvalidPersistedState
	}
	if query.OwnerKind == OwnerInteraction && strings.TrimSpace(artifact.Type) == "transcript" &&
		transcriptID != "" && artifact.ArtifactID != transcriptID {
		// Treat a mismatched selected transcript as absent at the public read
		// boundary. Returning persisted-state detail would reveal whether a
		// forged cross-session Artifact identifier exists.
		return ErrNotFound
	}
	return nil
}

func evidenceFromArtifact(kind artifacts.EvidenceKind, artifact *artifacts.Artifact) Evidence {
	var state EvidenceState
	switch artifact.DurableStatus {
	case artifacts.StatusFinalized:
		captureStatus := strings.TrimSpace(artifact.Metadata[artifacts.MetadataEvidenceCaptureStatus])
		truncated, truncatedErr := strconv.ParseBool(strings.TrimSpace(artifact.Metadata[artifacts.MetadataEvidenceTruncated]))
		if captureStatus != "finalized" || truncatedErr != nil ||
			(truncated && strings.TrimSpace(artifact.Metadata[artifacts.MetadataEvidenceTruncateReason]) == "") {
			state = EvidenceCorrupt
		} else if truncated {
			state = EvidenceTruncated
		} else {
			state = EvidenceFinalized
		}
	case artifacts.StatusFailed:
		if strings.TrimSpace(artifact.Metadata[artifacts.MetadataEvidenceCaptureStatus]) != "capture_failed" ||
			strings.TrimSpace(artifact.Metadata["loom.evidence.failure_class"]) == "" {
			state = EvidenceCorrupt
		} else {
			state = EvidenceCaptureFailed
		}
	case artifacts.StatusDeclared, artifacts.StatusUploading:
		state = EvidencePending
	default:
		state = EvidenceCorrupt
	}
	return Evidence{
		Kind: kind, ArtifactID: artifact.ArtifactID, State: state, MIMEType: artifact.MIMEType,
		SizeBytes: artifact.SizeBytes, ContentHash: artifact.ContentHash,
		RedactionStatus: artifact.RedactionStatus, Truncated: state == EvidenceTruncated,
		FailureClass: artifact.Metadata["loom.evidence.failure_class"], Artifact: cloneArtifact(artifact),
	}
}

func mapOwnerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, execution.ErrNotFound) || errors.Is(err, interaction.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, execution.ErrInvalid) || errors.Is(err, interaction.ErrInvalid) {
		return ErrInvalid
	}
	if errors.Is(err, execution.ErrUnavailable) || errors.Is(err, interaction.ErrUnavailable) {
		return ErrUnavailable
	}
	return err
}

func mapArtifactError(err error) error {
	switch {
	case errors.Is(err, artifacts.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, artifacts.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, artifacts.ErrUnavailable):
		return ErrUnavailable
	case errors.Is(err, artifacts.ErrInvalidPersistedState):
		return ErrInvalidPersistedState
	default:
		return err
	}
}

func cloneCapture(value *RunCapture) *RunCapture {
	if value == nil {
		return nil
	}
	result := *value
	result.StartedAt = cloneTime(value.StartedAt)
	result.FinishedAt = cloneTime(value.FinishedAt)
	result.Evidence = make([]Evidence, len(value.Evidence))
	for index := range value.Evidence {
		result.Evidence[index] = value.Evidence[index]
		result.Evidence[index].Artifact = cloneArtifact(value.Evidence[index].Artifact)
	}
	result.ownerEvidenceMetadata = cloneMetadata(value.ownerEvidenceMetadata)
	return &result
}

func cloneMetadata(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneArtifact(value *artifacts.Artifact) *artifacts.Artifact {
	if value == nil {
		return nil
	}
	result := *value
	result.Metadata = make(map[string]string, len(value.Metadata))
	for key, item := range value.Metadata {
		result.Metadata[key] = item
	}
	result.FinalizedAt = cloneTime(value.FinalizedAt)
	return &result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
