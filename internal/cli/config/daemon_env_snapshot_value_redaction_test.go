package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// The leak this guards: redaction used to key on the VARIABLE NAME alone, so a
// credential in an innocuously named variable was written verbatim into
// daemon-env.json and logged one line per key. Both cases below are real
// entries from this fleet's daemon environment, not invented ones.
func TestMustRedactEnv_CredentialsInInnocentlyNamedVars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		key   string
		value string
		want  bool
		why   string
	}{
		{
			name:  "redis URL with a password",
			key:   "LOOM_FLEETDB_REDIS_URL",
			value: "redis://:hunter2@127.0.0.1:6379",
			want:  true,
			why:   "the key matches none of KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL, and the password is in the userinfo",
		},
		{
			name:  "OTLP headers carrying a bearer token",
			key:   "OTEL_EXPORTER_OTLP_HEADERS",
			value: "authorization=Bearer abc123",
			want:  true,
			why:   "an auth header blob, and OTEL_ is a captured prefix so it really is written out",
		},
		{
			name:  "OTLP headers carrying an api-key pair",
			key:   "OTEL_EXPORTER_OTLP_HEADERS",
			value: "api-key=secretvalue,x-tenant=acme",
			want:  true,
			why:   "a secret assignment nested inside a compound value",
		},
		{
			name:  "postgres DSN with a password",
			key:   "LOOM_FLEETDB_POSTGRES_DSN",
			value: "postgres://user:pw@db:5432/fleet",
			want:  true,
		},
		{
			name:  "key name alone still redacts",
			key:   "LOOM_FLEET_DB_API_KEY",
			value: "anything",
			want:  true,
		},
		// The other half: over-redacting makes the snapshot useless as a
		// diagnostic, so plain values must stay readable.
		{
			name:  "plain URL with no credential",
			key:   "LOOM_FLEET_DB_URL",
			value: "http://127.0.0.1:3011",
			want:  false,
		},
		{
			name:  "URL with a user but no password",
			key:   "LOOM_GIT_REMOTE",
			value: "ssh://git@github.com/owner/repo.git",
			want:  false,
			why:   "git@host is a username, not a credential; redacting it would hide ordinary config",
		},
		{
			name:  "a port is not userinfo",
			key:   "LOOM_SERVER_URL",
			value: "http://localhost:3012",
			want:  false,
		},
		{
			name:  "ordinary word containing 'basic'",
			key:   "LOOM_MODE",
			value: "basic",
			want:  false,
			why:   "the bearer/basic pattern needs a following token, or every mention of the word redacts",
		},
		{
			name:  "plain workspace name",
			key:   "LOOM_WORKSPACE",
			value: "PUPPET",
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MustRedactEnv(tc.key, tc.value); got != tc.want {
				t.Errorf("MustRedactEnv(%q, %q) = %v, want %v\n  %s", tc.key, tc.value, got, tc.want, tc.why)
			}
		})
	}
}

// End to end through the capture, because the predicate being right does not
// prove the writer calls it.
func TestCaptureDaemonEnvSnapshot_RedactsCredentialBearingValues(t *testing.T) {
	t.Parallel()
	environ := []string{
		"LOOM_FLEETDB_REDIS_URL=redis://:hunter2@127.0.0.1:6379",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer abc123",
		"LOOM_WORKSPACE=PUPPET",
	}
	snap := CaptureDaemonEnvSnapshot(environ, "PUPPET")

	for _, key := range []string{"LOOM_FLEETDB_REDIS_URL", "OTEL_EXPORTER_OTLP_HEADERS"} {
		v, ok := snap.Env[key]
		if !ok {
			t.Fatalf("%s missing from the snapshot", key)
		}
		if !v.Redacted {
			t.Errorf("%s was not redacted", key)
		}
		if v.Value != "" {
			t.Errorf("%s recorded a plain value %q; the credential is in it", key, v.Value)
		}
		if v.Fingerprint == "" {
			t.Errorf("%s has no fingerprint, so drift is undetectable", key)
		}
	}

	// The secrets must not appear anywhere in the snapshot, under any field.
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	rendered := string(data)
	for _, secret := range []string{"hunter2", "abc123"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("secret %q appears in the serialised snapshot", secret)
		}
	}

	if plain := snap.Env["LOOM_WORKSPACE"]; plain.Redacted || plain.Value != "PUPPET" {
		t.Errorf("LOOM_WORKSPACE = %+v, want a plain readable value", plain)
	}
}
