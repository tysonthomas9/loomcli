package beads

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		op       string
		err      error
		resp     *rpc.Response
		wantKind backend.ErrorKind
		wantNil  bool
	}{
		{
			name:     "context.Canceled",
			op:       "Get",
			err:      context.Canceled,
			resp:     nil,
			wantKind: backend.KindCanceled,
		},
		{
			name:     "context.DeadlineExceeded",
			op:       "List",
			err:      context.DeadlineExceeded,
			resp:     nil,
			wantKind: backend.KindTimeout,
		},
		{
			name:     "wrapped context.Canceled",
			op:       "Ready",
			err:      fmt.Errorf("call failed: %w", context.Canceled),
			resp:     nil,
			wantKind: backend.KindCanceled,
		},
		{
			name:     "wrapped context.DeadlineExceeded",
			op:       "Stats",
			err:      fmt.Errorf("timeout: %w", context.DeadlineExceeded),
			resp:     nil,
			wantKind: backend.KindTimeout,
		},
		{
			name:     "generic transport error",
			op:       "Update",
			err:      errors.New("connection refused"),
			resp:     nil,
			wantKind: backend.KindUnavailable,
		},
		{
			name:     "nil response no error",
			op:       "Delete",
			err:      nil,
			resp:     nil,
			wantKind: backend.KindUnavailable,
		},
		{
			name: "resp.Error not found",
			op:   "Get",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "issue xyz not found",
			},
			wantKind: backend.KindNotFound,
		},
		{
			name: "resp.Error not found uppercase",
			op:   "Get",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "Issue Not Found in database",
			},
			wantKind: backend.KindNotFound,
		},
		{
			name: "resp.Error already claimed",
			op:   "Update",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "already claimed by agent-1",
			},
			wantKind: backend.KindConflict,
		},
		{
			name: "resp.Error validation error",
			op:   "Create",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "validation error: empty title",
			},
			wantKind: backend.KindValidation,
		},
		{
			name: "resp.Error invalid priority",
			op:   "Update",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "invalid priority",
			},
			wantKind: backend.KindValidation,
		},
		{
			name: "resp.Error generic message",
			op:   "Batch",
			err:  nil,
			resp: &rpc.Response{
				Success: false,
				Error:   "something went wrong",
			},
			wantKind: backend.KindInternal,
		},
		{
			name: "nil error successful response",
			op:   "Get",
			err:  nil,
			resp: &rpc.Response{
				Success: true,
				Data:    []byte(`{}`),
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.op, tt.err, tt.resp)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("classifyError() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("classifyError() = nil, want error with kind %q", tt.wantKind)
			}
			var be *backend.BackendError
			if !errors.As(got, &be) {
				t.Fatalf("classifyError() returned %T, want *backend.BackendError", got)
			}
			if be.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", be.Kind, tt.wantKind)
			}
			if be.Op != tt.op {
				t.Errorf("Op = %q, want %q", be.Op, tt.op)
			}
		})
	}
}

func TestClassifyError_TransportErrorPreservesWrappedCause(t *testing.T) {
	cause := errors.New("dial unix: no such file")
	got := classifyError("Get", cause, nil)

	var be *backend.BackendError
	if !errors.As(got, &be) {
		t.Fatal("expected *backend.BackendError")
	}
	if !errors.Is(got, cause) {
		t.Error("expected original cause to be preserved in error chain")
	}
}

func TestClassifyError_CanceledPreservesCause(t *testing.T) {
	got := classifyError("Get", context.Canceled, nil)

	if !errors.Is(got, context.Canceled) {
		t.Error("expected context.Canceled to be preserved in error chain")
	}
}

func TestClassifyError_TimeoutPreservesCause(t *testing.T) {
	got := classifyError("Get", context.DeadlineExceeded, nil)

	if !errors.Is(got, context.DeadlineExceeded) {
		t.Error("expected context.DeadlineExceeded to be preserved in error chain")
	}
}
