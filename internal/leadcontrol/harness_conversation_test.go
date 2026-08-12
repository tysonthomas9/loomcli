package leadcontrol

import (
	"errors"
	"fmt"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"
)

func TestInputPendingFromProbeError(t *testing.T) {
	probeFailure := errors.New("probe failed")
	tests := []struct {
		name        string
		err         error
		wantPending bool
		wantErr     error
	}{
		{name: "no input", err: chat.ErrNoInputPending},
		{name: "no input wrapped", err: fmt.Errorf("answer: %w", chat.ErrNoInputPending)},
		{name: "pending", err: chat.ErrStaleInputRequest, wantPending: true},
		{name: "pending wrapped", err: fmt.Errorf("answer: %w", chat.ErrStaleInputRequest), wantPending: true},
		{name: "reserved id collision fails closed", wantPending: true},
		{name: "probe failure", err: probeFailure, wantErr: probeFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pending, err := inputPendingFromProbeError(test.err)
			if pending != test.wantPending {
				t.Fatalf("pending = %v, want %v", pending, test.wantPending)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
