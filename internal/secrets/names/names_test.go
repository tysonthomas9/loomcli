package names

import (
	"strings"
	"testing"
)

func TestProviderCredentialNamesCanonical(t *testing.T) {
	if len(ProviderCredentialNames) == 0 {
		t.Fatal("ProviderCredentialNames is empty")
	}
	seen := make(map[string]struct{}, len(ProviderCredentialNames))
	for _, n := range ProviderCredentialNames {
		if n != strings.ToUpper(strings.TrimSpace(n)) {
			t.Fatalf("credential name %q is not canonical uppercase without whitespace", n)
		}
		if _, ok := seen[n]; ok {
			t.Fatalf("duplicate credential name %q", n)
		}
		seen[n] = struct{}{}
	}
}

func TestProviderCredentialSetMatchesNames(t *testing.T) {
	set := ProviderCredentialSet()
	if len(set) != len(ProviderCredentialNames) {
		t.Fatalf("ProviderCredentialSet size = %d, want %d", len(set), len(ProviderCredentialNames))
	}
	for _, n := range ProviderCredentialNames {
		if _, ok := set[n]; !ok {
			t.Fatalf("ProviderCredentialSet missing %q", n)
		}
	}
}

func TestProviderCredentialSetReturnsFreshMap(t *testing.T) {
	first := ProviderCredentialSet()
	second := ProviderCredentialSet()
	if len(ProviderCredentialNames) == 0 {
		t.Fatal("ProviderCredentialNames is empty")
	}
	name := ProviderCredentialNames[0]
	delete(first, name)
	first["NOT_A_REAL_PROVIDER_CREDENTIAL"] = struct{}{}
	if _, ok := second[name]; !ok {
		t.Fatalf("mutating first set changed second set: missing %q", name)
	}
	if _, ok := second["NOT_A_REAL_PROVIDER_CREDENTIAL"]; ok {
		t.Fatal("mutating first set changed second set: unexpected synthetic key")
	}
	for _, n := range ProviderCredentialNames {
		if n == "NOT_A_REAL_PROVIDER_CREDENTIAL" {
			t.Fatal("mutation changed ProviderCredentialNames")
		}
	}
}
