package workflowcatalog

import "testing"

func TestVersionApprovedPreservesLegacySemantics(t *testing.T) {
	version := &DriverVersion{VersionID: "v1", SourceDigest: "sha256:abc"}
	for _, test := range []struct {
		name     string
		driver   *Driver
		version  *DriverVersion
		approved bool
	}{
		{name: "nil driver", version: version},
		{name: "nil version", driver: &Driver{}},
		{name: "missing key", driver: &Driver{Metadata: map[string]string{}}, version: version},
		{name: "empty legacy marker", driver: &Driver{Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "  "}}, version: version, approved: true},
		{name: "source digest", driver: &Driver{Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "sha256:abc"}}, version: version, approved: true},
		{name: "trusted legacy marker", driver: &Driver{Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "trusted"}}, version: version, approved: true},
		{name: "digest mismatch", driver: &Driver{Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "sha256:old"}}, version: version},
		{name: "another version", driver: &Driver{Metadata: map[string]string{ApprovedVersionMetadataKey("v2"): "sha256:abc"}}, version: version},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := VersionApproved(test.driver, test.version); got != test.approved {
				t.Fatalf("VersionApproved() = %t, want %t", got, test.approved)
			}
		})
	}
}

func TestEffectiveTrustPrecedenceAndFailClosedDefault(t *testing.T) {
	version := func(manifestTrust string) *DriverVersion {
		manifest := map[string]string{}
		if manifestTrust != "" {
			manifest[ManifestTrustLevelKey] = manifestTrust
		}
		return &DriverVersion{VersionID: "v1", SourceDigest: "digest", Manifest: manifest}
	}
	for _, test := range []struct {
		name    string
		driver  *Driver
		version *DriverVersion
		want    DriverTrustLevel
	}{
		{name: "approval wins", driver: &Driver{TrustLevel: DriverTrustUntrusted, Metadata: map[string]string{ApprovedVersionMetadataKey("v1"): "digest"}}, version: version("untrusted"), want: DriverTrustTrusted},
		{name: "manifest trusted", driver: &Driver{TrustLevel: DriverTrustUntrusted}, version: version(" trusted "), want: DriverTrustTrusted},
		{name: "manifest untrusted overrides driver", driver: &Driver{TrustLevel: DriverTrustTrusted}, version: version("untrusted"), want: DriverTrustUntrusted},
		{name: "driver fallback", driver: &Driver{TrustLevel: DriverTrustTrusted}, version: version(""), want: DriverTrustTrusted},
		{name: "unknown manifest falls back", driver: &Driver{TrustLevel: DriverTrustTrusted}, version: version("future"), want: DriverTrustTrusted},
		{name: "missing values fail closed", driver: &Driver{}, version: version(""), want: DriverTrustUntrusted},
		{name: "nil values fail closed", want: DriverTrustUntrusted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := EffectiveTrust(test.driver, test.version); got != test.want {
				t.Fatalf("EffectiveTrust() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestApprovalMetadataKeyPreservesExactVersionID(t *testing.T) {
	if got := ApprovedVersionMetadataKey(" v1 "); got != "approved_version: v1 " {
		t.Fatalf("ApprovedVersionMetadataKey() = %q", got)
	}
}
