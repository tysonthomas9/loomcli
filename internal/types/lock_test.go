package types

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewExclusiveLock(t *testing.T) {
	t.Parallel()

	lock, err := NewExclusiveLock("test-holder", "1.0.0")
	if err != nil {
		t.Fatalf("NewExclusiveLock() error: %v", err)
	}

	if lock.Holder != "test-holder" {
		t.Errorf("Holder = %q, want %q", lock.Holder, "test-holder")
	}

	if lock.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", lock.Version, "1.0.0")
	}

	if lock.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", lock.PID, os.Getpid())
	}

	hostname, _ := os.Hostname()
	if lock.Hostname != hostname {
		t.Errorf("Hostname = %q, want %q", lock.Hostname, hostname)
	}

	if lock.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}

	// StartedAt should be recent (within last second)
	if time.Since(lock.StartedAt) > time.Second {
		t.Error("StartedAt should be recent")
	}
}

func TestExclusiveLock_MarshalJSON_RoundTrip(t *testing.T) {
	t.Parallel()

	original := ExclusiveLock{
		Holder:    "test-holder",
		PID:       12345,
		Hostname:  "test-host",
		StartedAt: time.Now().UTC().Truncate(time.Millisecond),
		Version:   "2.0.0",
	}

	// Marshal
	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	// Unmarshal
	var restored ExclusiveLock
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	// Compare fields
	if restored.Holder != original.Holder {
		t.Errorf("Holder = %q, want %q", restored.Holder, original.Holder)
	}
	if restored.PID != original.PID {
		t.Errorf("PID = %d, want %d", restored.PID, original.PID)
	}
	if restored.Hostname != original.Hostname {
		t.Errorf("Hostname = %q, want %q", restored.Hostname, original.Hostname)
	}
	if restored.Version != original.Version {
		t.Errorf("Version = %q, want %q", restored.Version, original.Version)
	}
	if !restored.StartedAt.Equal(original.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", restored.StartedAt, original.StartedAt)
	}
}

func TestExclusiveLock_Validate(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name    string
		lock    ExclusiveLock
		wantErr string
	}{
		{
			name: "valid lock",
			lock: ExclusiveLock{
				Holder:    "test-holder",
				PID:       12345,
				Hostname:  "test-host",
				StartedAt: now,
				Version:   "1.0.0",
			},
			wantErr: "",
		},
		{
			name: "empty holder",
			lock: ExclusiveLock{
				Holder:    "",
				PID:       12345,
				Hostname:  "test-host",
				StartedAt: now,
			},
			wantErr: "holder is required",
		},
		{
			name: "zero PID",
			lock: ExclusiveLock{
				Holder:    "test-holder",
				PID:       0,
				Hostname:  "test-host",
				StartedAt: now,
			},
			wantErr: "pid must be positive",
		},
		{
			name: "negative PID",
			lock: ExclusiveLock{
				Holder:    "test-holder",
				PID:       -1,
				Hostname:  "test-host",
				StartedAt: now,
			},
			wantErr: "pid must be positive",
		},
		{
			name: "empty hostname",
			lock: ExclusiveLock{
				Holder:    "test-holder",
				PID:       12345,
				Hostname:  "",
				StartedAt: now,
			},
			wantErr: "hostname is required",
		},
		{
			name: "zero StartedAt",
			lock: ExclusiveLock{
				Holder:   "test-holder",
				PID:      12345,
				Hostname: "test-host",
			},
			wantErr: "started_at is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.lock.Validate()
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

func TestExclusiveLock_JSONFields(t *testing.T) {
	t.Parallel()

	lock := ExclusiveLock{
		Holder:    "test-holder",
		PID:       12345,
		Hostname:  "test-host",
		StartedAt: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Version:   "1.0.0",
	}

	data, err := json.Marshal(&lock)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	// Verify JSON field names
	jsonStr := string(data)
	expectedFields := []string{
		`"holder"`,
		`"pid"`,
		`"hostname"`,
		`"started_at"`,
		`"version"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("JSON missing field %s: %s", field, jsonStr)
		}
	}
}
