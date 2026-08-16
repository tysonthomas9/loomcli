package leadcontrol

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const (
	MetadataRuntimeProvider        = "lead_runtime_provider"
	MetadataRuntimeControlled      = "lead_runtime_controlled"
	MetadataRuntimeStatus          = "lead_runtime_status"
	MetadataRuntimeStatusUpdated   = "lead_runtime_status_updated_at"
	MetadataCodexEndpoint          = "codex_app_server_endpoint"
	MetadataCodexPID               = "codex_app_server_pid"
	MetadataCodexRuntimeHome       = "codex_runtime_home"
	MetadataCodexSQLiteHome        = "codex_sqlite_home"
	MetadataCodexThreadID          = "codex_provider_thread_id"
	MetadataDeliveryVersion        = "lead_assignment_delivered_version"
	MetadataDeliveryEpic           = "lead_assignment_delivered_epic"
	MetadataDeliveryError          = "lead_assignment_delivery_error"
	MetadataDeliveryAttemptedAt    = "lead_assignment_delivery_attempted_at"
	MetadataDeliveryAcknowledged   = "lead_assignment_acknowledged_version"
	MetadataLeadMessageAttemptedAt = "lead_message_delivery_attempted_at"
	MetadataLeadMessageError       = "lead_message_delivery_error"
	RuntimeProviderCodex           = "codex"
	RuntimeStatusStarting          = "starting"
	RuntimeStatusDisconnected      = "disconnected"
	RuntimeStatusIdle              = "idle"
	RuntimeStatusActive            = "active"
	RuntimeStatusWaitingApproval   = "waiting_on_approval"
	RuntimeStatusWaitingUserInput  = "waiting_on_user_input"
	RuntimeStatusFailed            = "failed"
)

type CodexRuntimeMetadata struct {
	Endpoint    string
	ThreadID    string
	RuntimeHome string
	SQLiteHome  string
	PID         int
	Status      string
	Controlled  bool
}

func RuntimeMetadataFromSession(session *interaction.SessionRecord) CodexRuntimeMetadata {
	if session == nil {
		return CodexRuntimeMetadata{}
	}
	m := session.Metadata
	pid, _ := strconv.Atoi(strings.TrimSpace(m[MetadataCodexPID]))
	return CodexRuntimeMetadata{
		Endpoint:    strings.TrimSpace(m[MetadataCodexEndpoint]),
		ThreadID:    strings.TrimSpace(m[MetadataCodexThreadID]),
		RuntimeHome: strings.TrimSpace(m[MetadataCodexRuntimeHome]),
		SQLiteHome:  strings.TrimSpace(m[MetadataCodexSQLiteHome]),
		PID:         pid,
		Status:      strings.TrimSpace(m[MetadataRuntimeStatus]),
		Controlled:  strings.EqualFold(strings.TrimSpace(m[MetadataRuntimeControlled]), "true"),
	}
}

func UpdateCodexRuntimeMetadata(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace, sessionID string,
	runtime CodexRuntimeMetadata,
) error {
	if err := requireSessionRuntime(sessionRuntime, workspace, sessionID); err != nil {
		return err
	}
	if sessionRuntime == nil {
		return nil // explicit standalone mode
	}
	workspace = strings.TrimSpace(workspace)
	sessionID = strings.TrimSpace(sessionID)
	if workspace == "" || sessionID == "" {
		return nil
	}
	metadata := map[string]string{}
	metadata[MetadataRuntimeProvider] = RuntimeProviderCodex
	metadata[MetadataRuntimeControlled] = strconv.FormatBool(runtime.Controlled)
	if runtime.Endpoint != "" {
		metadata[MetadataCodexEndpoint] = runtime.Endpoint
	}
	if runtime.ThreadID != "" {
		metadata[MetadataCodexThreadID] = runtime.ThreadID
	}
	if runtime.RuntimeHome != "" {
		metadata[MetadataCodexRuntimeHome] = runtime.RuntimeHome
	}
	if runtime.SQLiteHome != "" {
		metadata[MetadataCodexSQLiteHome] = runtime.SQLiteHome
	}
	if runtime.PID > 0 {
		metadata[MetadataCodexPID] = strconv.Itoa(runtime.PID)
	}
	if runtime.Status != "" {
		metadata[MetadataRuntimeStatus] = runtime.Status
		metadata[MetadataRuntimeStatusUpdated] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	var phase *string
	if status := strings.TrimSpace(runtime.Status); status != "" {
		phase = &status
	}
	return sessionRuntime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey:    workspace,
		SessionID:       sessionID,
		Phase:           phase,
		MetadataUpserts: metadata,
	})
}

func MarkAssignmentDelivered(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace, sessionID, epicID, version string,
) error {
	if err := requireSessionRuntime(sessionRuntime, workspace, sessionID); err != nil {
		return err
	}
	if sessionRuntime == nil {
		return nil // explicit standalone mode
	}
	sessionID = strings.TrimSpace(sessionID)
	version = strings.TrimSpace(version)
	if strings.TrimSpace(workspace) == "" || sessionID == "" || version == "" {
		return nil
	}
	metadata := map[string]string{}
	metadata[MetadataDeliveryVersion] = version
	if strings.TrimSpace(epicID) != "" {
		metadata[MetadataDeliveryEpic] = strings.TrimSpace(epicID)
	}
	return sessionRuntime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey:     workspace,
		SessionID:        sessionID,
		MetadataUpserts:  metadata,
		MetadataRemovals: []string{MetadataDeliveryError},
	})
}

func MarkAssignmentDeliveryAttempt(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace, sessionID, message string,
) error {
	if err := requireSessionRuntime(sessionRuntime, workspace, sessionID); err != nil {
		return err
	}
	if sessionRuntime == nil {
		return nil // explicit standalone mode
	}
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(workspace) == "" || sessionID == "" {
		return nil
	}
	metadata := map[string]string{}
	metadata[MetadataDeliveryAttemptedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(message) != "" {
		metadata[MetadataDeliveryError] = strings.TrimSpace(message)
	}
	return sessionRuntime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey:    workspace,
		SessionID:       sessionID,
		MetadataUpserts: metadata,
	})
}

func MarkLeadMessageDeliveryAttempt(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace, sessionID, message string,
) error {
	if err := requireSessionRuntime(sessionRuntime, workspace, sessionID); err != nil {
		return err
	}
	if sessionRuntime == nil {
		return nil // explicit standalone mode
	}
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(workspace) == "" || sessionID == "" {
		return nil
	}
	metadata := map[string]string{}
	metadata[MetadataLeadMessageAttemptedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(message) != "" {
		metadata[MetadataLeadMessageError] = strings.TrimSpace(message)
	}
	return sessionRuntime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey:    workspace,
		SessionID:       sessionID,
		MetadataUpserts: metadata,
	})
}

func cloneMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
