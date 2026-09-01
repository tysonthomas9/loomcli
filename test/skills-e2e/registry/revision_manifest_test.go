package registry

import (
	"strings"
	"testing"
)

func TestValidateCompatibilityRevisionManifest(t *testing.T) {
	want := CompatibilityRevisionManifest{
		LoomRevision:   strings.Repeat("a", 40),
		FleetRevision:  strings.Repeat("b", 40),
		CorpusRevision: strings.Repeat("c", 40),
		Backend:        BackendRedis,
		Provider:       ProviderMinIO,
	}
	manifest := strings.NewReader(strings.Join([]string{
		"loomcli_sha=" + want.LoomRevision,
		"fleetdb_sha=" + want.FleetRevision,
		"vercel_skills_sha=" + want.CorpusRevision,
		"persistence_backend=redis",
		"object_provider=minio",
		"storage_mode=s3",
	}, "\n") + "\n")

	if err := ValidateCompatibilityRevisionManifest(manifest, want); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestValidateCompatibilityRevisionManifestFailsClosed(t *testing.T) {
	valid := CompatibilityRevisionManifest{
		LoomRevision:   strings.Repeat("a", 40),
		FleetRevision:  strings.Repeat("b", 40),
		CorpusRevision: strings.Repeat("c", 40),
		Backend:        BackendRedis,
		Provider:       ProviderGCS,
	}
	base := "loomcli_sha=" + valid.LoomRevision + "\n" +
		"fleetdb_sha=" + valid.FleetRevision + "\n" +
		"vercel_skills_sha=" + valid.CorpusRevision + "\n" +
		"persistence_backend=redis\nobject_provider=gcs\n"

	for _, test := range []struct {
		name string
		body string
		want CompatibilityRevisionManifest
	}{
		{name: "missing corpus", body: strings.ReplaceAll(base, "vercel_skills_sha="+valid.CorpusRevision+"\n", ""), want: valid},
		{name: "duplicate field", body: base + "object_provider=gcs\n", want: valid},
		{name: "checkout mismatch", body: base, want: func() CompatibilityRevisionManifest {
			changed := valid
			changed.FleetRevision = strings.Repeat("d", 40)
			return changed
		}()},
		{name: "abbreviated revision", body: strings.Replace(base, valid.LoomRevision, "abc123", 1), want: valid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateCompatibilityRevisionManifest(strings.NewReader(test.body), test.want); err == nil {
				t.Fatal("invalid revision manifest accepted")
			}
		})
	}
}
