package interaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

const (
	ActionDeliverChatMessage authority.Action = "interaction.deliver-chat-message"
	ActionDeliverAssignment  authority.Action = "interaction.deliver-assignment"
	ActionReadConversation   authority.Action = "interaction.read-conversation"

	// ChatDeliveryComponentID is the only serve-owned component allowed to
	// derive system authority for provider-neutral chat delivery. Callers
	// receive ChatMessenger, never the issuer or ChatRuntime.
	ChatDeliveryComponentID platformruntime.ComponentID = "serve-interaction-chat-delivery"
)

// ChatOperationRules is Interaction's default-deny registry for chat runtime
// operations. User-authenticated adapters receive the operator surface.
// Registered delivery components receive one exact system action and never
// receive the issuer.
func ChatOperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(
			ActionDeliverChatMessage,
			authority.ClassOperator,
			authority.ClassSystem,
		),
		authority.Allow(
			ActionDeliverAssignment,
			authority.ClassOperator,
			authority.ClassSystem,
		),
		authority.OperatorOnly(ActionReadConversation),
	}
}

type ChatDeliveryState string

const (
	ChatDeliveryNone        ChatDeliveryState = "none"
	ChatDeliveryQueued      ChatDeliveryState = "queued"
	ChatDeliveryPending     ChatDeliveryState = "pending"
	ChatDeliveryDelivered   ChatDeliveryState = "delivered"
	ChatDeliveryUnsupported ChatDeliveryState = "unsupported"
)

func (state ChatDeliveryState) valid() bool {
	switch state {
	case ChatDeliveryNone,
		ChatDeliveryQueued,
		ChatDeliveryPending,
		ChatDeliveryDelivered,
		ChatDeliveryUnsupported:
		return true
	default:
		return false
	}
}

type ConversationState string

const (
	ConversationStarting     ConversationState = "starting"
	ConversationReconnecting ConversationState = "reconnecting"
	ConversationIdle         ConversationState = "idle"
	ConversationRunning      ConversationState = "running"
	ConversationFailed       ConversationState = "failed"
	ConversationUnsupported  ConversationState = "unsupported"
)

func (state ConversationState) valid() bool {
	switch state {
	case ConversationStarting,
		ConversationReconnecting,
		ConversationIdle,
		ConversationRunning,
		ConversationFailed,
		ConversationUnsupported:
		return true
	default:
		return false
	}
}

// DeliverChatMessageCommand contains only durable delivery coordinates and
// message content. Runtime endpoint, thread ID, filesystem transcript paths,
// provider credentials, and Store handles stay behind ChatRuntime.
type DeliverChatMessageCommand struct {
	WorkspaceKey      string
	AgentID           string
	Body              string
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
	DedupeKey         string
}

// DeliverAssignmentCommand asks Interaction to deliver the current durable
// assignment for one lead. The assignment itself is resolved behind the
// runtime port so callers cannot forge its body or version.
type DeliverAssignmentCommand struct {
	WorkspaceKey string
	AgentID      string
}

type ChatDelivery struct {
	State          ChatDeliveryState
	Reason         string
	Provider       string
	RuntimeStatus  string
	Controlled     bool
	SessionID      string
	InboxMessageID string
}

type ConversationQuery struct {
	WorkspaceKey string
	AgentID      string
}

type ConversationMessage struct {
	TurnID string `json:"turn_id"`
	ItemID string `json:"item_id"`
	Role   string `json:"role"`
	Text   string `json:"text"`
	Phase  string `json:"phase,omitempty"`
}

type Conversation struct {
	State    ConversationState
	Detail   string
	Messages []ConversationMessage
}

// ChatAPI is the user-facing Interaction chat surface.
type ChatAPI interface {
	DeliverChatMessage(
		context.Context,
		authority.OperatorAuthority,
		DeliverChatMessageCommand,
	) (*ChatDelivery, error)
	DeliverAssignment(
		context.Context,
		authority.OperatorAuthority,
		DeliverAssignmentCommand,
	) (*ChatDelivery, error)
	ReadConversation(
		context.Context,
		authority.OperatorAuthority,
		ConversationQuery,
	) (*Conversation, error)
}

// RuntimeChatAPI is the system-authorized side used only by serve-owned
// registered delivery components.
type RuntimeChatAPI interface {
	DeliverChatMessageAsSystem(
		context.Context,
		authority.SystemAuthority,
		DeliverChatMessageCommand,
	) (*ChatDelivery, error)
	DeliverAssignmentAsSystem(
		context.Context,
		authority.SystemAuthority,
		DeliverAssignmentCommand,
	) (*ChatDelivery, error)
}

// ChatMessenger is the authority-free application port injected into
// registered delivery callers. Implementations derive one exact system
// authority for each invocation.
type ChatMessenger interface {
	DeliverChatMessage(
		context.Context,
		DeliverChatMessageCommand,
	) (*ChatDelivery, error)
	DeliverAssignment(
		context.Context,
		DeliverAssignmentCommand,
	) (*ChatDelivery, error)
}

// ChatRuntime is Interaction's provider-neutral outbound port. An adapter may
// use a controlled Codex app-server or a supported harness transcript, but
// those provider details never escape this boundary.
type ChatRuntime interface {
	DeliverChatMessage(
		context.Context,
		DeliverChatMessageCommand,
	) (*ChatDelivery, error)
	DeliverAssignment(
		context.Context,
		DeliverAssignmentCommand,
	) (*ChatDelivery, error)
	ReadConversation(
		context.Context,
		ConversationQuery,
	) (*Conversation, error)
}

type ChatService struct {
	runtime   ChatRuntime
	admission *authority.Admission
}

var (
	_ ChatAPI        = (*ChatService)(nil)
	_ RuntimeChatAPI = (*ChatService)(nil)
)

func NewChat(
	runtime ChatRuntime,
	admission *authority.Admission,
) (*ChatService, error) {
	if runtime == nil || admission == nil {
		return nil, fmt.Errorf(
			"compose Interaction chat: runtime and admission are required: %w",
			ErrUnavailable,
		)
	}
	return &ChatService{runtime: runtime, admission: admission}, nil
}

func (service *ChatService) DeliverChatMessage(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command DeliverChatMessageCommand,
) (*ChatDelivery, error) {
	command, err := normalizeChatMessage(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := service.admission.RequireOperator(
		ActionDeliverChatMessage,
		command.WorkspaceKey,
		auth,
	); err != nil {
		return nil, err
	}
	return service.deliverMessage(ctx, command)
}

func (service *ChatService) DeliverChatMessageAsSystem(
	ctx context.Context,
	auth authority.SystemAuthority,
	command DeliverChatMessageCommand,
) (*ChatDelivery, error) {
	command, err := normalizeChatMessage(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := service.admission.RequireSystem(
		ActionDeliverChatMessage,
		command.WorkspaceKey,
		auth,
	); err != nil {
		return nil, err
	}
	return service.deliverMessage(ctx, command)
}

func (service *ChatService) deliverMessage(
	ctx context.Context,
	command DeliverChatMessageCommand,
) (*ChatDelivery, error) {
	if service == nil || service.runtime == nil {
		return nil, ErrUnavailable
	}
	result, err := service.runtime.DeliverChatMessage(ctx, command)
	if err != nil {
		return nil, fmt.Errorf("deliver Interaction chat message: %w", err)
	}
	return validateChatDelivery(result, command.WorkspaceKey)
}

func (service *ChatService) DeliverAssignment(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command DeliverAssignmentCommand,
) (*ChatDelivery, error) {
	command, err := normalizeAssignment(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := service.admission.RequireOperator(
		ActionDeliverAssignment,
		command.WorkspaceKey,
		auth,
	); err != nil {
		return nil, err
	}
	return service.deliverAssignment(ctx, command)
}

func (service *ChatService) DeliverAssignmentAsSystem(
	ctx context.Context,
	auth authority.SystemAuthority,
	command DeliverAssignmentCommand,
) (*ChatDelivery, error) {
	command, err := normalizeAssignment(command)
	if err != nil {
		return nil, err
	}
	if service == nil || service.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := service.admission.RequireSystem(
		ActionDeliverAssignment,
		command.WorkspaceKey,
		auth,
	); err != nil {
		return nil, err
	}
	return service.deliverAssignment(ctx, command)
}

func (service *ChatService) deliverAssignment(
	ctx context.Context,
	command DeliverAssignmentCommand,
) (*ChatDelivery, error) {
	if service == nil || service.runtime == nil {
		return nil, ErrUnavailable
	}
	result, err := service.runtime.DeliverAssignment(ctx, command)
	if err != nil {
		return nil, fmt.Errorf("deliver Interaction assignment: %w", err)
	}
	return validateChatDelivery(result, command.WorkspaceKey)
}

func (service *ChatService) ReadConversation(
	ctx context.Context,
	auth authority.OperatorAuthority,
	query ConversationQuery,
) (*Conversation, error) {
	query, err := normalizeConversationQuery(query)
	if err != nil {
		return nil, err
	}
	if service == nil || service.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := service.admission.RequireOperator(
		ActionReadConversation,
		query.WorkspaceKey,
		auth,
	); err != nil {
		return nil, err
	}
	if service.runtime == nil {
		return nil, ErrUnavailable
	}
	conversation, err := service.runtime.ReadConversation(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read Interaction conversation: %w", err)
	}
	if err := validateConversation(conversation); err != nil {
		return nil, err
	}
	return cloneConversation(conversation), nil
}

const (
	maxChatBodyBytes       = 1 << 20
	maxChatCoordinateBytes = 1024
	maxConversationItems   = 10000
)

func normalizeChatMessage(
	command DeliverChatMessageCommand,
) (DeliverChatMessageCommand, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.AgentID = strings.TrimSpace(command.AgentID)
	command.SourceKind = strings.TrimSpace(command.SourceKind)
	command.SourceRef = strings.TrimSpace(command.SourceRef)
	command.DriverRunID = strings.TrimSpace(command.DriverRunID)
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.TriggerEventID = strings.TrimSpace(command.TriggerEventID)
	command.TriggerDeliveryID = strings.TrimSpace(command.TriggerDeliveryID)
	command.DedupeKey = strings.TrimSpace(command.DedupeKey)
	if command.WorkspaceKey == "" || command.AgentID == "" ||
		strings.TrimSpace(command.Body) == "" ||
		command.SourceKind == "" || command.DedupeKey == "" {
		return DeliverChatMessageCommand{}, fmt.Errorf(
			"workspace, agent, body, source kind, and dedupe key are required: %w",
			ErrInvalid,
		)
	}
	if len(command.Body) > maxChatBodyBytes {
		return DeliverChatMessageCommand{}, fmt.Errorf(
			"chat body exceeds %d bytes: %w",
			maxChatBodyBytes,
			ErrInvalid,
		)
	}
	for label, value := range map[string]string{
		"workspace":        command.WorkspaceKey,
		"agent":            command.AgentID,
		"source kind":      command.SourceKind,
		"source ref":       command.SourceRef,
		"driver run":       command.DriverRunID,
		"task run":         command.TaskRunID,
		"trigger event":    command.TriggerEventID,
		"trigger delivery": command.TriggerDeliveryID,
		"dedupe key":       command.DedupeKey,
	} {
		if value != strings.TrimSpace(value) ||
			len(value) > maxChatCoordinateBytes ||
			strings.ContainsAny(value, "\x00\r\n") {
			return DeliverChatMessageCommand{}, fmt.Errorf(
				"chat %s is not canonical and bounded: %w",
				label,
				ErrInvalid,
			)
		}
	}
	return command, nil
}

func normalizeAssignment(
	command DeliverAssignmentCommand,
) (DeliverAssignmentCommand, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.AgentID = strings.TrimSpace(command.AgentID)
	if !canonicalChatIdentity(command.WorkspaceKey) ||
		!canonicalChatIdentity(command.AgentID) {
		return DeliverAssignmentCommand{}, fmt.Errorf(
			"canonical workspace and agent are required: %w",
			ErrInvalid,
		)
	}
	return command, nil
}

func normalizeConversationQuery(
	query ConversationQuery,
) (ConversationQuery, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	if !canonicalChatIdentity(query.WorkspaceKey) ||
		!canonicalChatIdentity(query.AgentID) {
		return ConversationQuery{}, fmt.Errorf(
			"canonical workspace and agent are required: %w",
			ErrInvalid,
		)
	}
	return query, nil
}

func canonicalChatIdentity(value string) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		len(value) <= maxChatCoordinateBytes &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validateChatDelivery(
	result *ChatDelivery,
	workspace string,
) (*ChatDelivery, error) {
	if result == nil || !result.State.valid() {
		return nil, fmt.Errorf(
			"chat runtime returned an invalid delivery for workspace %q: %w",
			workspace,
			ErrInvalidPersistedState,
		)
	}
	out := *result
	return &out, nil
}

func validateConversation(conversation *Conversation) error {
	if conversation == nil ||
		!conversation.State.valid() ||
		len(conversation.Messages) > maxConversationItems {
		return fmt.Errorf(
			"chat runtime returned an invalid conversation: %w",
			ErrInvalidPersistedState,
		)
	}
	for _, message := range conversation.Messages {
		if message.TurnID == "" || message.ItemID == "" ||
			(message.Role != "user" && message.Role != "assistant") ||
			strings.TrimSpace(message.Text) == "" {
			return fmt.Errorf(
				"chat runtime returned an invalid conversation message: %w",
				ErrInvalidPersistedState,
			)
		}
	}
	return nil
}

func cloneConversation(conversation *Conversation) *Conversation {
	if conversation == nil {
		return nil
	}
	out := *conversation
	out.Messages = append([]ConversationMessage(nil), conversation.Messages...)
	if out.Messages == nil {
		out.Messages = []ConversationMessage{}
	}
	return &out
}
