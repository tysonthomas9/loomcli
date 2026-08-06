package domain

import "testing"

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
