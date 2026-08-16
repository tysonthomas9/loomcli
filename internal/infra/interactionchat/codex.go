package interactionchat

import (
	"context"
	"strings"

	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

func (runtime *Runtime) readCodexConversation(
	ctx context.Context,
	session *interaction.SessionRecord,
) *interaction.Conversation {
	metadata := leadcontrol.RuntimeMetadataFromSession(session)
	if session == nil || metadata.Endpoint == "" || metadata.ThreadID == "" {
		return &interaction.Conversation{
			State: interaction.ConversationStarting,
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, providerCallTimeout)
	defer cancel()
	client, err := runtime.dialCodex(callCtx, metadata.Endpoint)
	if err != nil {
		return &interaction.Conversation{
			State: interaction.ConversationReconnecting,
		}
	}
	defer func() {
		_ = client.Close("Interaction conversation snapshot")
	}()
	thread, err := client.ReadThreadWithTurns(callCtx, metadata.ThreadID)
	if err != nil {
		return &interaction.Conversation{
			State: interaction.ConversationReconnecting,
		}
	}
	return &interaction.Conversation{
		State:    codexConversationState(thread),
		Messages: flattenCodexMessages(thread),
	}
}

func codexConversationState(
	thread *leadcontrol.CodexThread,
) interaction.ConversationState {
	if thread != nil && thread.Status.CanStartTurn() {
		return interaction.ConversationIdle
	}
	return interaction.ConversationRunning
}

func flattenCodexMessages(
	thread *leadcontrol.CodexThread,
) []interaction.ConversationMessage {
	if thread == nil {
		return nil
	}
	var out []interaction.ConversationMessage
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			message := codexConversationMessage(turn.ID, item)
			if message != nil {
				out = append(out, *message)
			}
		}
	}
	return trimLaunchPreamble(out)
}

func codexConversationMessage(
	turnID string,
	item leadcontrol.CodexTurnItem,
) *interaction.ConversationMessage {
	message := interaction.ConversationMessage{
		TurnID: turnID,
		ItemID: item.ID,
		Text:   item.PlainText(),
		Phase:  item.Phase,
	}
	switch item.Type {
	case "userMessage":
		message.Role = "user"
	case "agentMessage":
		message.Role = "assistant"
	default:
		return nil
	}
	if strings.TrimSpace(message.Text) == "" {
		return nil
	}
	return &message
}

const reviewerPromptMarker = "## READ-ONLY PR REVIEWER"

func trimLaunchPreamble(
	messages []interaction.ConversationMessage,
) []interaction.ConversationMessage {
	index := 0
	for index < len(messages) {
		message := messages[index]
		if message.Role == "user" &&
			strings.HasPrefix(
				strings.TrimSpace(message.Text),
				reviewerPromptMarker,
			) {
			index++
			continue
		}
		break
	}
	return messages[index:]
}
