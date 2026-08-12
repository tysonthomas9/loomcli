package runcapture

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const maxCaptureArtifacts = 256

type Service struct {
	executions   execution.TaskRunQueries
	interactions interaction.SessionQueries
	artifacts    artifacts.QueryAPI
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
	values, err := service.artifacts.ListArtifacts(ctx, artifacts.SearchQuery{
		WorkspaceKey: query.WorkspaceKey,
		Filter:       filter,
	})
	if err != nil {
		return nil, mapArtifactError(err)
	}
	capture.Evidence = make([]Evidence, 0, len(values))
	for _, artifact := range values {
		if err := validateOwnedArtifact(artifact, query, transcriptID); err != nil {
			return nil, err
		}
		kind, err := artifacts.EvidenceKindForArtifactType(artifact.Type)
		if err != nil {
			return nil, errors.Join(ErrInvalidPersistedState, err)
		}
		capture.Evidence = append(capture.Evidence, evidenceFromArtifact(kind, artifact))
	}
	return cloneCapture(capture), nil
}

func (service *Service) List(ctx context.Context, query ArchiveQuery) ([]*RunCapture, error) {
	if query.Limit < 0 || query.Limit > 1 || strings.TrimSpace(query.OwnerID) == "" {
		return nil, ErrInvalid
	}
	capture, err := service.Get(ctx, Query{
		WorkspaceKey: query.WorkspaceKey, OwnerKind: query.OwnerKind, OwnerID: query.OwnerID,
		AgentID: query.AgentID, WorkItemID: query.WorkItemID,
	})
	if err != nil {
		return nil, err
	}
	return []*RunCapture{capture}, nil
}

func (service *Service) Transcript(ctx context.Context, query Query) (*TranscriptEvidence, error) {
	capture, err := service.Get(ctx, query)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range capture.Evidence {
		if capture.Evidence[i].Kind != artifacts.EvidenceTranscript {
			continue
		}
		if index >= 0 {
			return nil, fmt.Errorf("capture %q has multiple transcript artifacts: %w", capture.OwnerID, ErrInvalidPersistedState)
		}
		index = i
	}
	if index < 0 {
		return &TranscriptEvidence{Capture: capture, Evidence: Evidence{Kind: artifacts.EvidenceTranscript, State: EvidenceMissing}}, nil
	}
	result := &TranscriptEvidence{Capture: capture, Evidence: capture.Evidence[index]}
	if result.Evidence.State != EvidenceFinalized && result.Evidence.State != EvidenceTruncated {
		return result, nil
	}
	content, err := service.artifacts.ReadArtifactContent(ctx, artifacts.Query{
		WorkspaceKey: capture.WorkspaceKey, ArtifactID: result.Evidence.ArtifactID,
	})
	if err != nil {
		if errors.Is(err, artifacts.ErrContentUnavailable) || errors.Is(err, artifacts.ErrNotFound) {
			result.Evidence.State = EvidenceContentUnavailable
			return result, nil
		}
		return nil, mapArtifactError(err)
	}
	events, _, err := transcript.DecodeCanonicalJSONL(content, artifacts.MaxEvidenceCaptureBytes, transcript.MaxCanonicalEvents)
	if err != nil {
		result.Evidence.State = EvidenceCorrupt
		result.Evidence.FailureClass = "canonical_transcript_invalid"
		return result, nil
	}
	result.Events = append([]transcript.Event(nil), events...)
	return result, nil
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
		if run == nil || run.WorkspaceKey != query.WorkspaceKey || run.TaskRunID != query.OwnerID ||
			(query.WorkItemID != "" && run.WorkItemID != query.WorkItemID) {
			return nil, artifacts.SearchFilter{}, "", ErrNotFound
		}
		capture := &RunCapture{
			WorkspaceKey: run.WorkspaceKey, OwnerKind: OwnerExecution, OwnerID: run.TaskRunID,
			WorkItemID: run.WorkItemID, Status: string(run.Status), StartedAt: cloneTime(run.StartedAt),
			FinishedAt: cloneTime(run.FinishedAt), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		}
		return capture, artifacts.SearchFilter{OwnerType: artifacts.OwnerTaskRun, OwnerID: run.TaskRunID, Limit: maxCaptureArtifacts}, "", nil
	case OwnerInteraction:
		session, err := service.interactions.GetSession(ctx, query.WorkspaceKey, query.OwnerID)
		if err != nil {
			return nil, artifacts.SearchFilter{}, "", mapOwnerError(err)
		}
		if session == nil || session.WorkspaceKey != query.WorkspaceKey || session.SessionID != query.OwnerID ||
			(query.AgentID != "" && session.AgentID != query.AgentID) ||
			(query.WorkItemID != "" && session.TaskID != query.WorkItemID) {
			return nil, artifacts.SearchFilter{}, "", ErrNotFound
		}
		started := session.StartedAt
		capture := &RunCapture{
			WorkspaceKey: session.WorkspaceKey, OwnerKind: OwnerInteraction, OwnerID: session.SessionID,
			AgentID: session.AgentID, WorkItemID: session.TaskID, Status: string(session.Status),
			StartedAt: &started, FinishedAt: cloneTime(session.FinishedAt),
			CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		}
		return capture, artifacts.SearchFilter{OwnerType: artifacts.OwnerSession, OwnerID: session.SessionID, Limit: maxCaptureArtifacts}, strings.TrimSpace(session.TranscriptArtifactID), nil
	default:
		return nil, artifacts.SearchFilter{}, "", ErrInvalid
	}
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
		return ErrInvalidPersistedState
	}
	return nil
}

func evidenceFromArtifact(kind artifacts.EvidenceKind, artifact *artifacts.Artifact) Evidence {
	state := EvidencePending
	switch artifact.DurableStatus {
	case artifacts.StatusFinalized:
		state = EvidenceFinalized
		if value, _ := strconv.ParseBool(artifact.Metadata[artifacts.MetadataEvidenceTruncated]); value {
			state = EvidenceTruncated
		}
	case artifacts.StatusFailed:
		state = EvidenceCaptureFailed
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
	return &result
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
