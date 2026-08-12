package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type interactionHTTPExecution struct {
	execution.DriverRunAPI
	run *execution.DriverRun
}

func (stub interactionHTTPExecution) HeartbeatDriverRun(
	context.Context,
	authority.ExecutionAuthority,
	execution.DriverRunHeartbeatCommand,
) (*execution.DriverRun, error) {
	return stub.run, nil
}

type interactionHTTPAuthorities struct {
	execution.DriverRunAuthorityResolver
}

func (interactionHTTPAuthorities) ResolveDriverRunAuthority(
	context.Context,
	string,
	authority.Action,
	execution.Owner,
) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

type interactionHTTPMessenger struct {
	commands []interaction.DeliverChatMessageCommand
	delivery *interaction.ChatDelivery
	err      error
}

func (stub *interactionHTTPMessenger) DeliverChatMessage(
	_ context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	stub.commands = append(stub.commands, command)
	return stub.delivery, stub.err
}

func (*interactionHTTPMessenger) DeliverAssignment(
	context.Context,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return nil, interaction.ErrUnavailable
}

func TestInteractionDomainErrorsUseStableDriverEnvelope(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
	}{
		{
			name: "invalid", err: interaction.ErrInvalid,
			status: http.StatusBadRequest, code: "invalid",
		},
		{
			name: "not found", err: interaction.ErrNotFound,
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "not owner", err: interaction.ErrNotOwner,
			status: http.StatusForbidden, code: "not_owner",
		},
		{
			name: "conflict", err: interaction.ErrConflict,
			status: http.StatusConflict, code: "conflict",
		},
		{
			name: "invalid transition", err: interaction.ErrInvalidTransition,
			status: http.StatusConflict, code: "invalid_transition",
		},
		{
			name: "unavailable", err: interaction.ErrUnavailable,
			status: http.StatusServiceUnavailable, code: "unavailable",
			retryable: true,
		},
		{
			name:   "invalid persisted state",
			err:    interaction.ErrInvalidPersistedState,
			status: http.StatusInternalServerError, code: "internal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeDomainOpError(
				recorder,
				fmt.Errorf("deliver agent message: %w", test.err),
			)
			if recorder.Code != test.status {
				t.Fatalf(
					"status = %d, want %d",
					recorder.Code,
					test.status,
				)
			}
			var envelope struct {
				Error opError `json:"error"`
			}
			if err := json.Unmarshal(
				recorder.Body.Bytes(),
				&envelope,
			); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != test.code ||
				envelope.Error.Retryable != test.retryable {
				t.Fatalf("error envelope = %+v", envelope.Error)
			}
		})
	}
}

func TestDriverAPIDeliverAgentMessageUsesComposedInteractionChat(
	t *testing.T,
) {
	messenger := &interactionHTTPMessenger{
		delivery: &interaction.ChatDelivery{
			State:          interaction.ChatDeliveryQueued,
			Reason:         "queued",
			InboxMessageID: "inbox-1",
		},
	}
	module := newInteractionHTTPModule(messenger)
	recorder := serveInteractionHTTPRequest(t, module)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"status = %d body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var result struct {
		AgentName      string `json:"agentName"`
		State          string `json:"state"`
		InboxMessageID string `json:"inboxMessageId"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AgentName != "worker-1" ||
		result.State != string(interaction.ChatDeliveryQueued) ||
		result.InboxMessageID != "inbox-1" {
		t.Fatalf("result = %+v", result)
	}
	if len(messenger.commands) != 1 {
		t.Fatalf("commands = %+v, want one", messenger.commands)
	}
	command := messenger.commands[0]
	if command.WorkspaceKey != "WS" ||
		command.AgentID != "worker-1" ||
		command.Body != "hello" ||
		command.SourceKind != "workflow" ||
		command.SourceRef != "driver-run://run-1" ||
		command.DriverRunID != "run-1" ||
		command.DedupeKey == "" {
		t.Fatalf("command = %+v", command)
	}
}

func TestDriverAPIDeliverAgentMessageMapsInteractionUnavailable(
	t *testing.T,
) {
	module := newInteractionHTTPModule(&interactionHTTPMessenger{
		err: interaction.ErrUnavailable,
	})
	recorder := serveInteractionHTTPRequest(t, module)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"status = %d body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var envelope struct {
		Error opError `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "unavailable" ||
		!envelope.Error.Retryable {
		t.Fatalf("error envelope = %+v", envelope.Error)
	}
}

func newInteractionHTTPModule(
	messenger interaction.ChatMessenger,
) *Module {
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun,
		ResourceID:   "run-1",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		LeaseToken:   "lease-token",
		FencingToken: 1,
	}
	return NewModule(Config{
		RunTokenKey: bytes.Repeat([]byte{0x42}, 32),
		Execution: interactionHTTPExecution{
			run: &execution.DriverRun{
				WorkspaceKey: "WS",
				RunID:        "run-1",
				Status:       execution.DriverRunRunning,
				Owner:        owner,
			},
		},
		ExecutionAuthorities: interactionHTTPAuthorities{},
		InteractionChat:      messenger,
	})
}

func serveInteractionHTTPRequest(
	t *testing.T,
	module *Module,
) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	module.Register(mux)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/driver/deliver-agent-message",
		bytes.NewBufferString(
			`{"agent":"worker-1","message":"hello"}`,
		),
	)
	runToken, err := driverpkg.MintRunToken(driverpkg.RunTokenClaims{
		WorkspaceKey: "WS", RunID: "run-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
	}, bytes.Repeat([]byte{0x42}, 32), time.Hour)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+runToken)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}
