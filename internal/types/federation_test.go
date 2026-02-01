package types

import (
	"strings"
	"testing"
	"time"
)

func TestFederatedMessageType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msgType FederatedMessageType
		valid   bool
	}{
		{MsgWorkHandoff, true},
		{MsgQuery, true},
		{MsgReply, true},
		{MsgBroadcast, true},
		{MsgAck, true},
		{MsgReject, true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		name := string(tt.msgType)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.msgType.IsValid(); got != tt.valid {
				t.Errorf("FederatedMessageType(%q).IsValid() = %v, want %v", tt.msgType, got, tt.valid)
			}
		})
	}
}

func TestFederatedMessageType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msgType  FederatedMessageType
		expected string
	}{
		{MsgWorkHandoff, "work-handoff"},
		{MsgQuery, "query"},
		{MsgReply, "reply"},
		{MsgBroadcast, "broadcast"},
		{MsgAck, "ack"},
		{MsgReject, "reject"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.msgType.String(); got != tt.expected {
				t.Errorf("FederatedMessageType.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFederatedMessageType_IsResponse(t *testing.T) {
	t.Parallel()

	responseTypes := []FederatedMessageType{MsgReply, MsgAck, MsgReject}
	nonResponseTypes := []FederatedMessageType{MsgWorkHandoff, MsgQuery, MsgBroadcast}

	for _, mt := range responseTypes {
		t.Run(string(mt)+"_is_response", func(t *testing.T) {
			if !mt.IsResponse() {
				t.Errorf("FederatedMessageType(%q).IsResponse() = false, want true", mt)
			}
		})
	}

	for _, mt := range nonResponseTypes {
		t.Run(string(mt)+"_not_response", func(t *testing.T) {
			if mt.IsResponse() {
				t.Errorf("FederatedMessageType(%q).IsResponse() = true, want false", mt)
			}
		})
	}
}

func TestFederatedMessageType_RequiresRecipient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msgType  FederatedMessageType
		requires bool
	}{
		{MsgWorkHandoff, true},
		{MsgQuery, true},
		{MsgReply, true},
		{MsgAck, true},
		{MsgReject, true},
		{MsgBroadcast, false}, // Broadcasts don't require recipient
	}

	for _, tt := range tests {
		t.Run(string(tt.msgType), func(t *testing.T) {
			if got := tt.msgType.RequiresRecipient(); got != tt.requires {
				t.Errorf("FederatedMessageType(%q).RequiresRecipient() = %v, want %v", tt.msgType, got, tt.requires)
			}
		})
	}
}

func TestRejectCode_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code  RejectCode
		valid bool
	}{
		{RejectInvalid, true},
		{RejectUnauthorized, true},
		{RejectCapacity, true},
		{RejectNotFound, true},
		{RejectTimeout, true},
		{RejectDuplicate, true},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		name := string(tt.code)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.code.IsValid(); got != tt.valid {
				t.Errorf("RejectCode(%q).IsValid() = %v, want %v", tt.code, got, tt.valid)
			}
		})
	}
}

func TestRejectCode_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code     RejectCode
		expected string
	}{
		{RejectInvalid, "invalid"},
		{RejectUnauthorized, "unauthorized"},
		{RejectCapacity, "capacity"},
		{RejectNotFound, "not_found"},
		{RejectTimeout, "timeout"},
		{RejectDuplicate, "duplicate"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.code.String(); got != tt.expected {
				t.Errorf("RejectCode.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFederatedMessage_Validate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	validSender := &EntityRef{
		Name:     "test-sender",
		Platform: "test",
		Org:      "org",
		ID:       "sender-1",
	}

	tests := []struct {
		name    string
		msg     FederatedMessage
		wantErr string
	}{
		{
			name: "valid complete message",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgWorkHandoff,
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "",
		},
		{
			name: "missing ID",
			msg: FederatedMessage{
				Type:      MsgWorkHandoff,
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "message ID is required",
		},
		{
			name: "invalid type",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      "invalid",
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "invalid message type",
		},
		{
			name: "zero timestamp",
			msg: FederatedMessage{
				ID:     "msg-123",
				Type:   MsgWorkHandoff,
				Sender: validSender,
			},
			wantErr: "timestamp is required",
		},
		{
			name: "missing sender",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgWorkHandoff,
				Timestamp: now,
			},
			wantErr: "sender is required",
		},
		{
			name: "empty sender",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgWorkHandoff,
				Timestamp: now,
				Sender:    &EntityRef{},
			},
			wantErr: "sender is required",
		},
		{
			name: "reply without ReplyTo",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgReply,
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "reply message requires reply_to field",
		},
		{
			name: "ack without ReplyTo",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgAck,
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "ack message requires reply_to field",
		},
		{
			name: "reject without ReplyTo",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgReject,
				Timestamp: now,
				Sender:    validSender,
			},
			wantErr: "reject message requires reply_to field",
		},
		{
			name: "valid reply with ReplyTo",
			msg: FederatedMessage{
				ID:        "msg-123",
				Type:      MsgReply,
				Timestamp: now,
				Sender:    validSender,
				ReplyTo:   "msg-original",
			},
			wantErr: "",
		},
		{
			name: "reject with invalid code",
			msg: FederatedMessage{
				ID:         "msg-123",
				Type:       MsgReject,
				Timestamp:  now,
				Sender:     validSender,
				ReplyTo:    "msg-original",
				RejectCode: "bad_code",
			},
			wantErr: "invalid reject code",
		},
		{
			name: "reject with valid code",
			msg: FederatedMessage{
				ID:         "msg-123",
				Type:       MsgReject,
				Timestamp:  now,
				Sender:     validSender,
				ReplyTo:    "msg-original",
				RejectCode: RejectInvalid,
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("Validate() expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Validate() error = %q, want error containing %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestFederatedMessage_ValidateBroadcastNoRecipient(t *testing.T) {
	t.Parallel()

	// Note: This documents that RequiresRecipient is not enforced in Validate()
	// The validation currently passes for broadcasts without recipients,
	// which is correct behavior since broadcasts are meant for all peers.
	now := time.Now()
	msg := FederatedMessage{
		ID:        "msg-123",
		Type:      MsgBroadcast,
		Timestamp: now,
		Sender: &EntityRef{
			Name:     "sender",
			Platform: "test",
			Org:      "org",
			ID:       "id",
		},
		// No Recipient - this is valid for broadcasts
	}

	if err := msg.Validate(); err != nil {
		t.Errorf("Broadcast without recipient should be valid: %v", err)
	}
}

