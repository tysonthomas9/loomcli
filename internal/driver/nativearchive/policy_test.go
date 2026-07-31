package nativearchive

import (
	"archive/tar"
	"testing"
)

func TestPolicyRejectsUnsafePathsAndUnsupportedEntryKinds(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", "..", "../escape", "dir/../escape", "/absolute", `dir\file`} {
		name := name
		t.Run("path_"+name, func(t *testing.T) {
			t.Parallel()
			if _, err := CleanEntryName(name); err == nil {
				t.Fatalf("CleanEntryName(%q) succeeded", name)
			}
		})
	}
	if clean, err := CleanEntryName("dist/server.mjs"); err != nil || clean != "dist/server.mjs" {
		t.Fatalf("canonical path = %q err=%v", clean, err)
	}

	for name, typeflag := range map[string]byte{
		"symlink":    tar.TypeSymlink,
		"hardlink":   tar.TypeLink,
		"gnu_sparse": tar.TypeGNUSparse,
		"character":  tar.TypeChar,
		"block":      tar.TypeBlock,
		"fifo":       tar.TypeFifo,
		"pax":        tar.TypeXHeader,
	} {
		name, typeflag := name, typeflag
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ClassifyEntry(typeflag, name); err == nil {
				t.Fatalf("ClassifyEntry(%q) succeeded", typeflag)
			}
		})
	}
}

func TestPolicyBoundsFailClosedWithoutAllocatingLimitSizedPayloads(t *testing.T) {
	t.Parallel()
	if err := ValidateArchiveSize(0); err == nil {
		t.Fatal("empty archive accepted")
	}
	if err := ValidateArchiveSize(MaxArchiveBytes + 1); err == nil {
		t.Fatal("oversized archive accepted")
	}
	if err := ValidateArchiveSize(MaxArchiveBytes); err != nil {
		t.Fatalf("max-sized archive rejected: %v", err)
	}
	if err := ValidateManifestSize(MaxManifestBytes + 1); err == nil {
		t.Fatal("oversized manifest accepted")
	}
	if err := ValidateManifestSize(MaxManifestBytes); err != nil {
		t.Fatalf("max-sized manifest rejected: %v", err)
	}
	if _, _, err := AccountEntry(MaxEntries, 0, 0, false); err == nil {
		t.Fatal("too many directory entries accepted")
	}
	if _, _, err := AccountEntry(0, MaxExtractBytes, 1, true); err == nil {
		t.Fatal("oversized extraction accepted")
	}
	if _, _, err := AccountEntry(0, 0, -1, true); err == nil {
		t.Fatal("negative file size accepted")
	}
	entries, bytes, err := AccountEntry(MaxEntries-1, MaxExtractBytes-1, 1, true)
	if err != nil || entries != MaxEntries || bytes != MaxExtractBytes {
		t.Fatalf("exact extraction limits = entries %d bytes %d err=%v", entries, bytes, err)
	}
}
