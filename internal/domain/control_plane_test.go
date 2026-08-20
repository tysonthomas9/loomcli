package domain

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestResolveRuntimeProvider(t *testing.T) {
	tests := []struct {
		name    string
		agent   *Agent
		profile *DaemonProfile
		want    RuntimeProvider
	}{
		{
			name:    "agent set",
			agent:   &Agent{RuntimeProvider: RuntimeProviderDaytona},
			profile: &DaemonProfile{RuntimeProvider: RuntimeProviderKubernetes},
			want:    RuntimeProviderDaytona,
		},
		{
			name:    "agent unset profile set",
			agent:   &Agent{},
			profile: &DaemonProfile{RuntimeProvider: RuntimeProviderE2B},
			want:    RuntimeProviderE2B,
		},
		{
			name:    "both unset defaults local",
			agent:   &Agent{},
			profile: &DaemonProfile{},
			want:    RuntimeProviderLocal,
		},
		{
			name:    "nil profile defaults local",
			agent:   &Agent{},
			profile: nil,
			want:    RuntimeProviderLocal,
		},
		{
			name:    "nil agent uses profile",
			agent:   nil,
			profile: &DaemonProfile{RuntimeProvider: RuntimeProviderCI},
			want:    RuntimeProviderCI,
		},
		{
			name:    "nil agent and profile defaults local",
			agent:   nil,
			profile: nil,
			want:    RuntimeProviderLocal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRuntimeProvider(tt.agent, tt.profile); got != tt.want {
				t.Fatalf("ResolveRuntimeProvider() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodePlacementLostReleaseJSON(t *testing.T) {
	lostAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	absenceConfirmedAt := lostAt.Add(2 * time.Minute)
	in := NodePlacement{
		Generation:         7,
		State:              PlacementStateReleased,
		LostAt:             &lostAt,
		AbsenceConfirmedAt: &absenceConfirmedAt,
		ReleaseReason:      PlacementReleaseReasonLostConfirmedAbsent,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range [][]byte{
		[]byte(`"lost_at":"2026-01-02T03:04:05Z"`),
		[]byte(`"absence_confirmed_at":"2026-01-02T03:06:05Z"`),
		[]byte(`"release_reason":"lost_confirmed_absent"`),
	} {
		if !bytes.Contains(raw, want) {
			t.Fatalf("JSON %s missing %s", raw, want)
		}
	}
	var got NodePlacement
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.LostAt == nil || !got.LostAt.Equal(lostAt) || got.AbsenceConfirmedAt == nil || !got.AbsenceConfirmedAt.Equal(absenceConfirmedAt) {
		t.Fatalf("round-trip timestamps = lost %v absence %v", got.LostAt, got.AbsenceConfirmedAt)
	}
	if got.ReleaseReason != PlacementReleaseReasonLostConfirmedAbsent {
		t.Fatalf("ReleaseReason = %q, want %q", got.ReleaseReason, PlacementReleaseReasonLostConfirmedAbsent)
	}

	empty, err := json.Marshal(NodePlacement{})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	for _, absent := range [][]byte{[]byte(`"lost_at"`), []byte(`"absence_confirmed_at"`), []byte(`"release_reason"`)} {
		if bytes.Contains(empty, absent) {
			t.Fatalf("empty placement JSON %s contains optional field %s", empty, absent)
		}
	}
}
