package leadcontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Deprecated: use DeliverCurrentAssignment. Kept while callers migrate.
func DeliverCurrentAssignmentToCodex(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
	return DeliverCurrentAssignment(ctx, st, workspace, leadName)
}

// Deprecated: use DeliverLeadMessage. Kept while callers migrate.
func DeliverLeadMessageToCodex(ctx context.Context, st store.Store, workspace, leadName, message string) (*DeliveryResult, error) {
	return DeliverLeadMessage(ctx, st, workspace, leadName, message)
}

// Deprecated: use DeliverLeadMessageWithOptions. Kept while callers migrate.
func DeliverLeadMessageToCodexWithOptions(ctx context.Context, st store.Store, workspace, leadName, message string, opts LeadMessageDeliveryOptions) (*DeliveryResult, error) {
	return DeliverLeadMessageWithOptions(ctx, st, workspace, leadName, message, opts)
}

// Deprecated: use DeliverPendingLeadMessages. Kept while callers migrate.
func DeliverPendingLeadMessagesToCodex(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
	return DeliverPendingLeadMessages(ctx, st, workspace, leadName)
}

// codexTurnDeliverer injects turns through the codex app-server endpoint
// persisted in session metadata, so it works from any process.
type codexTurnDeliverer struct {
	runtime CodexRuntimeMetadata
}

func newCodexTurnDeliverer(session *domain.AgentSession) *codexTurnDeliverer {
	return &codexTurnDeliverer{runtime: RuntimeMetadataFromSession(session)}
}

func (d *codexTurnDeliverer) provider() string { return RuntimeProviderCodex }

func (d *codexTurnDeliverer) hasRuntimeMetadata(metadata map[string]string) bool {
	return hasCodexRuntimeMetadata(metadata)
}

func (d *codexTurnDeliverer) notReadyReason() string {
	return "controlled Codex runtime is not ready"
}

func (d *codexTurnDeliverer) unsupportedReason(metadata map[string]string) string {
	if codexRuntimeUnsupported(metadata, d.runtime) {
		return "lead session is not a controlled Codex runtime"
	}
	return ""
}

func (d *codexTurnDeliverer) pendingReason() string {
	return codexRuntimePendingReason(d.runtime)
}

func (d *codexTurnDeliverer) claimedBy(sessionID string) string {
	claimedBy := "codex:" + sessionID
	if d.runtime.ThreadID != "" {
		claimedBy += ":" + d.runtime.ThreadID
	}
	return claimedBy
}

func (d *codexTurnDeliverer) populate(result *DeliveryResult, session *domain.AgentSession) {
	d.runtime = RuntimeMetadataFromSession(session)
	result.Provider = RuntimeProviderCodex
	result.Runtime = d.runtime
}

func (d *codexTurnDeliverer) deliveredThreadID() string { return d.runtime.ThreadID }

func (d *codexTurnDeliverer) deliverTurn(
	ctx context.Context,
	st store.Store,
	sessionRuntime SessionRuntime,
	workspace string,
	sessionID string,
	result *DeliveryResult,
	message string,
	closeReason string,
) (*DeliveryResult, error) {
	return deliverCodexLeadTurn(
		ctx, sessionRuntime, workspace, sessionID, d.runtime, result, message, closeReason,
	)
}

func deliverCodexLeadTurn(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace string,
	sessionID string,
	runtime CodexRuntimeMetadata,
	result *DeliveryResult,
	message string,
	closeReason string,
) (*DeliveryResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := dialCodexAppServerClient(callCtx, runtime.Endpoint)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	defer func() { _ = client.Close(closeReason) }()

	thread, err := client.ReadThread(callCtx, runtime.ThreadID)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Thread = thread
	runtime.Status = thread.Status.RuntimeStatus()
	result.Runtime = runtime
	_ = UpdateCodexRuntimeMetadata(ctx, sessionRuntime, workspace, sessionID, runtime)
	if !thread.Status.CanStartTurn() {
		result.Reason = fmt.Sprintf("codex thread is %s", runtime.Status)
		return result, nil
	}

	if err := client.StartTurn(callCtx, runtime.ThreadID, message); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.State = DeliveryStateDelivered
	result.Reason = ""
	return result, nil
}

func codexRuntimePendingReason(runtime CodexRuntimeMetadata) string {
	if runtime.Endpoint == "" || runtime.ThreadID == "" {
		return "controlled Codex runtime is not ready"
	}
	return ""
}

func codexRuntimeUnsupported(metadata map[string]string, runtime CodexRuntimeMetadata) bool {
	return strings.TrimSpace(metadata[MetadataRuntimeProvider]) != RuntimeProviderCodex || !runtime.Controlled
}

func hasCodexRuntimeMetadata(metadata map[string]string) bool {
	if len(metadata) == 0 {
		return false
	}
	for _, key := range []string{
		MetadataRuntimeProvider,
		MetadataRuntimeControlled,
		MetadataCodexEndpoint,
		MetadataCodexPID,
		MetadataCodexRuntimeHome,
		MetadataCodexSQLiteHome,
		MetadataCodexThreadID,
	} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}
