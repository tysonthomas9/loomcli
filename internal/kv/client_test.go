package kv

import (
	"context"
	"testing"
)

func TestNewClient(t *testing.T) {
	client := NewClient("localhost:6379", "", 0)
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	// Don't actually connect — just verify construction
	_ = client.Close()
}

func TestKeyBuilders(t *testing.T) {
	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"taskOwnerKey", func() string { return taskOwnerKey("task-123") }, "loom:task:task-123:owner"},
		{"workerStateKey", func() string { return workerStateKey("worker-1") }, "loom:worker:worker-1:state"},
		{"activeWorkersKey", func() string { return activeWorkersKey() }, "loom:workers:active"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestToInt64(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    int64
		wantErr bool
	}{
		{"int64", int64(42), 42, false},
		{"string", "42", 42, false},
		{"float", 42.0, 0, true},
		{"nil", nil, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toInt64(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toInt64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{"string", "hello", "hello", false},
		{"int64", int64(42), "42", false},
		{"nil", nil, "", false},
		{"float", 42.0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toString(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toString(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if KeyPrefix != "loom:" {
		t.Errorf("expected KeyPrefix loom:, got %s", KeyPrefix)
	}
	if DefaultTTLSeconds != 300 {
		t.Errorf("expected DefaultTTLSeconds 300, got %d", DefaultTTLSeconds)
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid", "worker-1", false},
		{"valid with dots", "loomcli-kv6.1", false},
		{"empty", "", true},
		{"contains colon", "worker:1", true},
		{"contains newline", "worker\n1", true},
		{"contains tab", "worker\t1", true},
		{"contains space", "worker 1", true},
		{"contains carriage return", "worker\r1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateID(tt.id, "testID")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestClaimTask_InvalidInputs(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	_, err := client.ClaimTask(ctx, "", "task-1", "title", "spark")
	if err == nil {
		t.Error("expected error for empty workerID")
	}

	_, err = client.ClaimTask(ctx, "worker-1", "", "title", "spark")
	if err == nil {
		t.Error("expected error for empty taskID")
	}

	_, err = client.ClaimTask(ctx, "worker:bad", "task-1", "title", "spark")
	if err == nil {
		t.Error("expected error for workerID with colon")
	}
}
