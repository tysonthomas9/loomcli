package daemon

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error is not retryable",
			err:      nil,
			expected: false,
		},
		{
			name:     "ErrDaemonNotRunning is retryable",
			err:      ErrDaemonNotRunning,
			expected: true,
		},
		{
			name:     "ErrConnectionTimeout is retryable",
			err:      ErrConnectionTimeout,
			expected: true,
		},
		{
			name:     "ErrDaemonUnhealthy is retryable",
			err:      ErrDaemonUnhealthy,
			expected: true,
		},
		{
			name:     "ErrInvalidSocketPath is not retryable",
			err:      ErrInvalidSocketPath,
			expected: false,
		},
		{
			name:     "ErrPoolExhausted is not retryable",
			err:      ErrPoolExhausted,
			expected: false,
		},
		{
			name:     "ErrPoolClosed is not retryable",
			err:      ErrPoolClosed,
			expected: false,
		},
		{
			name:     "generic error is not retryable",
			err:      errors.New("some generic error"),
			expected: false,
		},
		{
			name:     "wrapped ErrDaemonNotRunning is retryable",
			err:      fmt.Errorf("connection failed: %w", ErrDaemonNotRunning),
			expected: true,
		},
		{
			name:     "wrapped ErrConnectionTimeout is retryable",
			err:      fmt.Errorf("timed out: %w", ErrConnectionTimeout),
			expected: true,
		},
		{
			name:     "wrapped ErrDaemonUnhealthy is retryable",
			err:      fmt.Errorf("health check failed: %w", ErrDaemonUnhealthy),
			expected: true,
		},
		{
			name:     "double wrapped retryable error is retryable",
			err:      fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrDaemonNotRunning)),
			expected: true,
		},
		{
			name:     "wrapped non-retryable error is not retryable",
			err:      fmt.Errorf("bad path: %w", ErrInvalidSocketPath),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestErrorMessages(t *testing.T) {
	// Verify error messages are meaningful
	tests := []struct {
		err     error
		wantMsg string
	}{
		{
			err:     ErrDaemonNotRunning,
			wantMsg: "daemon not running",
		},
		{
			err:     ErrDaemonUnhealthy,
			wantMsg: "daemon unhealthy",
		},
		{
			err:     ErrConnectionTimeout,
			wantMsg: "connection timeout",
		},
		{
			err:     ErrInvalidSocketPath,
			wantMsg: "invalid socket path",
		},
		{
			err:     ErrPoolExhausted,
			wantMsg: "connection pool exhausted",
		},
		{
			err:     ErrPoolClosed,
			wantMsg: "connection pool closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error message = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	// Verify all error values are distinct
	errs := []error{
		ErrDaemonNotRunning,
		ErrDaemonUnhealthy,
		ErrConnectionTimeout,
		ErrInvalidSocketPath,
		ErrPoolExhausted,
		ErrPoolClosed,
	}

	for i, err1 := range errs {
		for j, err2 := range errs {
			if i != j && errors.Is(err1, err2) {
				t.Errorf("errors should be distinct: %v and %v are considered equal", err1, err2)
			}
		}
	}
}
