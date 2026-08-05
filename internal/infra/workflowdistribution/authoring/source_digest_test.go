package authoring

import "testing"

func mustSourceDigest(t testing.TB, files map[string]string) string {
	t.Helper()
	digest, err := SourceDigest(files)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	return digest
}
