package leadcontrol

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// Harness lead runtime metadata keys, persisted on the lead's orchestration
// session alongside the shared lead_runtime_* keys from codex_metadata.go.
// The runtime provider value is the loom backend name (e.g. "claude").
const (
	MetadataHarnessName          = "lead_harness_name"
	MetadataHarnessChatSessionID = "lead_harness_chat_session_id"
	MetadataHarnessSessionID     = "lead_harness_session_id"
	MetadataHarnessPID           = "lead_harness_pid"
	// MetadataHarnessStartedAt is the RFC3339Nano wall-clock time this
	// runtime launched its harness. Transcript readers use it to reconcile a
	// launch-pinned session id against reality: when the harness rotated its
	// session id (claude does on a first boot that passes the folder-trust
	// dialog), only transcripts recorded after this instant can belong to
	// this runtime.
	MetadataHarnessStartedAt = "lead_harness_started_at"
)

// HarnessRuntimeMetadata mirrors CodexRuntimeMetadata for leads supervised by
// the harness-wrapper PTY runtime. Unlike codex there is no dialable endpoint:
// delivery requires the in-process conversation registry.
type HarnessRuntimeMetadata struct {
	Provider         string
	HarnessName      string
	ChatSessionID    string
	HarnessSessionID string
	PID              int
	Status           string
	Controlled       bool
	StartedAt        time.Time
}

func HarnessRuntimeMetadataFromSession(session *interaction.SessionRecord) HarnessRuntimeMetadata {
	if session == nil {
		return HarnessRuntimeMetadata{}
	}
	m := session.Metadata
	pid, _ := strconv.Atoi(strings.TrimSpace(m[MetadataHarnessPID]))
	startedAt, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(m[MetadataHarnessStartedAt]))
	return HarnessRuntimeMetadata{
		Provider:         strings.TrimSpace(m[MetadataRuntimeProvider]),
		HarnessName:      strings.TrimSpace(m[MetadataHarnessName]),
		ChatSessionID:    strings.TrimSpace(m[MetadataHarnessChatSessionID]),
		HarnessSessionID: strings.TrimSpace(m[MetadataHarnessSessionID]),
		PID:              pid,
		Status:           strings.TrimSpace(m[MetadataRuntimeStatus]),
		Controlled:       strings.EqualFold(strings.TrimSpace(m[MetadataRuntimeControlled]), "true"),
		StartedAt:        startedAt,
	}
}

func UpdateHarnessRuntimeMetadata(
	ctx context.Context,
	sessionRuntime SessionRuntime,
	workspace, sessionID string,
	runtime HarnessRuntimeMetadata,
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
	if runtime.Provider != "" {
		metadata[MetadataRuntimeProvider] = runtime.Provider
	}
	metadata[MetadataRuntimeControlled] = strconv.FormatBool(runtime.Controlled)
	if runtime.HarnessName != "" {
		metadata[MetadataHarnessName] = runtime.HarnessName
	}
	if runtime.ChatSessionID != "" {
		metadata[MetadataHarnessChatSessionID] = runtime.ChatSessionID
	}
	if runtime.HarnessSessionID != "" {
		metadata[MetadataHarnessSessionID] = runtime.HarnessSessionID
	}
	if runtime.PID > 0 {
		metadata[MetadataHarnessPID] = strconv.Itoa(runtime.PID)
	}
	if !runtime.StartedAt.IsZero() {
		metadata[MetadataHarnessStartedAt] = runtime.StartedAt.UTC().Format(time.RFC3339Nano)
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

func hasHarnessRuntimeMetadata(metadata map[string]string) bool {
	if len(metadata) == 0 {
		return false
	}
	for _, key := range []string{
		MetadataRuntimeProvider,
		MetadataRuntimeControlled,
		MetadataHarnessName,
		MetadataHarnessChatSessionID,
		MetadataHarnessPID,
	} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}

// harnessRuntimeStatus maps a wrapper snapshot onto the shared RuntimeStatus
// vocabulary. An empty wrapper status means the session is producing output
// and has not been classified — treat as active.
func harnessRuntimeStatus(snap wrapper.Snapshot) string {
	switch snap.Status {
	case wrapper.StatusWaitingForInput:
		return RuntimeStatusWaitingUserInput
	case wrapper.StatusIdle, wrapper.StatusStale:
		return RuntimeStatusIdle
	case wrapper.StatusInterrupted:
		return RuntimeStatusWaitingUserInput
	case wrapper.StatusFailed, wrapper.StatusBlockedByCost, wrapper.StatusRetryLater,
		wrapper.StatusAPIError, wrapper.StatusBinaryNotFound:
		return RuntimeStatusFailed
	case "":
		return RuntimeStatusActive
	default:
		return string(snap.Status)
	}
}
