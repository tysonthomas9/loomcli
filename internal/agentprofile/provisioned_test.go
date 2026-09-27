package agentprofile

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProvisionedProfile lays out a profile root: a manifest with the given files/managed
// lists, and every named baseline under .provisioned/.
func writeProvisionedProfile(t *testing.T, manifest string, baselines map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	for rel, body := range baselines {
		path := filepath.Join(dir, ProvisionedDirName, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir baseline: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
	}
	return dir
}

func TestProvisionedString(t *testing.T) {
	const managedSettings = `{"files":[".provisioned/settings.json"],"managed":["settings.json"],"fingerprint":"x","harness_version":"v"}`
	const managedConfig = `{"files":[".provisioned/config.toml"],"managed":["config.toml"],"fingerprint":"x","harness_version":"v"}`

	cases := []struct {
		name      string
		manifest  string
		baselines map[string]string
		rel       string
		key       string
		want      string
		wantFound bool
		wantErr   bool
	}{
		{
			name:      "json baseline",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `{"model":"opus[1m]","cleanupPeriodDays":30}`},
			rel:       "settings.json", key: "model",
			want: "opus[1m]", wantFound: true,
		},
		{
			// The suffix form is the value actually provisioned for lead/claude
			// and must survive the round trip untouched.
			name:      "toml baseline",
			manifest:  managedConfig,
			baselines: map[string]string{"config.toml": "model = \"gpt-5.6-sol\"\nmodel_reasoning_effort = \"medium\"\n"},
			rel:       "config.toml", key: "model",
			want: "gpt-5.6-sol", wantFound: true,
		},
		{
			// Byte-hashed in `files` means there is no .provisioned/ copy;
			// reading the LIVE file instead would defeat the whole point.
			name:      "rel listed only in files",
			manifest:  `{"files":["settings.json"],"fingerprint":"x","harness_version":"v"}`,
			baselines: map[string]string{"settings.json": `{"model":"opus"}`},
			rel:       "settings.json", key: "model",
		},
		{
			name:      "no manifest",
			baselines: map[string]string{"settings.json": `{"model":"opus"}`},
			rel:       "settings.json", key: "model",
		},
		{
			name:     "unparseable manifest",
			manifest: `{"files":`,
			rel:      "settings.json", key: "model",
		},
		{
			name:     "baseline file absent",
			manifest: managedSettings,
			rel:      "settings.json", key: "model",
		},
		{
			name:      "key absent",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `{"cleanupPeriodDays":30}`},
			rel:       "settings.json", key: "model",
		},
		{
			name:      "key not a string",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `{"model":7}`},
			rel:       "settings.json", key: "model",
		},
		{
			name:      "key empty after trim",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `{"model":"   "}`},
			rel:       "settings.json", key: "model",
		},
		{
			name:      "baseline is not an object",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `["model"]`},
			rel:       "settings.json", key: "model",
		},
		{
			name:      "undecodable baseline",
			manifest:  managedSettings,
			baselines: map[string]string{"settings.json": `{"model":`},
			rel:       "settings.json", key: "model",
			wantErr: true,
		},
		{
			name:      "unsupported extension",
			manifest:  `{"files":[".provisioned/settings.yaml"],"managed":["settings.yaml"],"fingerprint":"x","harness_version":"v"}`,
			baselines: map[string]string{"settings.yaml": "model: opus\n"},
			rel:       "settings.yaml", key: "model",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProvisionedProfile(t, tc.manifest, tc.baselines)
			got, found, err := ProvisionedString(dir, tc.rel, tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ProvisionedString() error = nil, want an error")
				}
				if found || got != "" {
					t.Fatalf("ProvisionedString() = %q/%v on error, want \"\"/false", got, found)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProvisionedString() error = %v", err)
			}
			if got != tc.want || found != tc.wantFound {
				t.Fatalf("ProvisionedString() = %q/%v, want %q/%v", got, found, tc.want, tc.wantFound)
			}
		})
	}
}

// A profile directory that does not exist at all is the "nothing to pin" case,
// not an error: the pin must never fail a launch.
func TestProvisionedStringMissingDir(t *testing.T) {
	got, found, err := ProvisionedString(filepath.Join(t.TempDir(), "nope"), "settings.json", "model")
	if err != nil || found || got != "" {
		t.Fatalf("ProvisionedString() = %q/%v/%v, want \"\"/false/nil", got, found, err)
	}
}
