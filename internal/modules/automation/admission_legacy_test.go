package automation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// testEventAuthority and testAdmissionCommand keep the long-running admission
// behavior matrix concise while routing every case through the new typed
// origin port. They are test-only and deliberately cannot reintroduce the
// retired production API.
type testEventAuthority struct {
	webhook  *authority.WebhookAuthority
	workflow *authority.ExecutionAuthority
	system   *authority.SystemAuthority
}

func webhookTestAuthority(auth authority.WebhookAuthority) testEventAuthority {
	return testEventAuthority{webhook: &auth}
}

func workflowTestAuthority(auth authority.ExecutionAuthority) testEventAuthority {
	return testEventAuthority{workflow: &auth}
}

func systemTestAuthority(auth authority.SystemAuthority) testEventAuthority {
	return testEventAuthority{system: &auth}
}

type testAdmissionCommand struct {
	WorkspaceKey          string
	SourceKind            string
	SourceRef             string
	RouteKey              string
	SourceEventID         string
	EventType             string
	SubjectRef            string
	ActorRef              string
	ParentEventID         string
	EpicID                string
	ExecutionNodeID       string
	ExecutionLeaseID      string
	ExecutionFencingToken int64
	OccurredAt            time.Time
	RawPayloadRef         string
	RawPayloadDigest      string
	Payload               json.RawMessage
	SubjectAttrs          map[string]string
}

func admitTestEvent(service *Service, ctx context.Context, auth testEventAuthority, command testAdmissionCommand) (*AdmissionResult, error) {
	switch {
	case auth.webhook != nil:
		return service.AdmitWebhookEvent(ctx, *auth.webhook, WebhookEvent{
			WorkspaceKey: command.WorkspaceKey, SourceKind: command.SourceKind, SourceRef: command.SourceRef,
			RouteKey: command.RouteKey, SourceEventID: command.SourceEventID, EventType: command.EventType,
			SubjectRef: command.SubjectRef, ActorRef: command.ActorRef, OccurredAt: command.OccurredAt,
			RawPayloadRef: command.RawPayloadRef, RawPayloadDigest: command.RawPayloadDigest,
			Payload: command.Payload, SubjectAttrs: command.SubjectAttrs,
		})
	case auth.workflow != nil:
		return service.AdmitWorkflowEvent(ctx, *auth.workflow, WorkflowEvent{
			WorkspaceKey: command.WorkspaceKey, SourceEventID: command.SourceEventID, EventType: command.EventType,
			SubjectRef: command.SubjectRef, ExecutionNodeID: command.ExecutionNodeID,
			ExecutionLeaseID: command.ExecutionLeaseID, ExecutionFencingToken: command.ExecutionFencingToken,
			Payload: command.Payload, SubjectAttrs: command.SubjectAttrs,
		})
	case auth.system != nil:
		if command.SourceKind != "" && command.SourceKind != SourceKindInternal {
			return nil, ErrInvalid
		}
		return service.AdmitSystemEvent(ctx, *auth.system, SystemEvent{
			WorkspaceKey: command.WorkspaceKey, SourceEventID: command.SourceEventID, EventType: command.EventType,
			SourceRef: command.SourceRef, SubjectRef: command.SubjectRef, ParentEventID: command.ParentEventID,
			EpicID: command.EpicID, OccurredAt: command.OccurredAt, Payload: command.Payload,
			SubjectAttrs: command.SubjectAttrs,
		})
	default:
		return nil, authority.ErrAdmissionDenied
	}
}
