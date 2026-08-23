package authoringkit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyRejectsPinDrift is the DoD "pin drift" case: a kit whose manifest
// declares a Flue commit or Node version other than the baked pins is rejected
// as authoring_kit_invalid — even when its kit_digest is internally consistent
// (the pin check precedes the digest check). Guards A2 (node_version is the exact
// shipped pin 22.20.0, not the 22.18 engine floor).
func TestVerifyRejectsPinDrift(t *testing.T) {
	build := func(flueCommit, nodeVersion string) error {
		root := t.TempDir()
		data := []byte("hello")
		if err := os.WriteFile(filepath.Join(root, "tool.js"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		m := Manifest{SchemaVersion: SchemaVersion, FlueCommit: flueCommit, NodeVersion: nodeVersion, Files: []FileEntry{{Path: "tool.js", Kind: "data", SHA256: DigestBytes(data)}}}
		canonical, err := CanonicalBytes(m)
		if err != nil {
			t.Fatal(err)
		}
		m.KitDigest = DigestBytes(canonical)
		manifest, err := CanonicalBytes(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "kit-manifest.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = verify(root)
		return err
	}
	if err := build(ExpectedFlueCommit, ExpectedNodeVersion); err != nil {
		t.Fatalf("kit with correct pins rejected: %v", err)
	}
	if err := build("0000000000000000000000000000000000000000", ExpectedNodeVersion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("flue pin drift not rejected as authoring_kit_invalid: %v", err)
	}
	// The 22.18 engine floor mistakenly baked as the manifest pin must be rejected.
	if err := build(ExpectedFlueCommit, "22.18.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("node pin drift not rejected as authoring_kit_invalid: %v", err)
	}
}

// machoLE64 is a minimal little-endian 64-bit Mach-O magic prefix. IsMachO and
// the packager only inspect the first four bytes, so the trailing bytes are
// arbitrary filler standing in for a real binary.
var machoLE64 = append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("\x07\x00\x00\x01macho-body")...)

type kitFile struct {
	entry   FileEntry
	content []byte
}

// writeKitFixture materializes files plus a self-consistent manifest (correct
// kit_digest) under root, exactly as the packager would, so verify accepts it
// until a test tampers with it.
func writeKitFixture(t *testing.T, root string, files []kitFile) {
	t.Helper()
	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.entry.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, f.content, 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, f.entry)
	}
	writeManifest(t, root, entries)
}

// writeManifest writes a manifest with a correct self-consistent kit_digest over
// the given entries (which may reference files that do not exist, for path
// checks that run before any file read).
func writeManifest(t *testing.T, root string, entries []FileEntry) {
	t.Helper()
	m := Manifest{SchemaVersion: SchemaVersion, FlueCommit: ExpectedFlueCommit, NodeVersion: ExpectedNodeVersion, Files: entries}
	canonical, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	m.KitDigest = DigestBytes(canonical)
	manifest, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kit-manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsMachO(t *testing.T) {
	if !IsMachO(machoLE64) {
		t.Fatal("little-endian 64-bit Mach-O magic not detected")
	}
	for _, magic := range [][]byte{
		{0xFE, 0xED, 0xFA, 0xCE}, // thin BE 32
		{0xFE, 0xED, 0xFA, 0xCF}, // thin BE 64
		{0xCE, 0xFA, 0xED, 0xFE}, // thin LE 32
		{0xCA, 0xFE, 0xBA, 0xBE}, // fat
		{0xBF, 0xBA, 0xFE, 0xCA}, // fat 64 swapped
	} {
		if !IsMachO(append(append([]byte{}, magic...), 0, 0, 0, 0)) {
			t.Fatalf("Mach-O magic %x not detected", magic)
		}
	}
	if IsMachO([]byte("plain text file")) {
		t.Fatal("plain data misdetected as Mach-O")
	}
	if IsMachO([]byte{0xCF}) {
		t.Fatal("short buffer misdetected as Mach-O")
	}
}

// TestVerifyMachOUnsignedBoundBySHA256 closes the H3 gap: a Mach-O entry with no
// Team ID must still be byte-bound by sha256 (previously macho entries were not
// verified at all).
func TestVerifyMachOUnsignedBoundBySHA256(t *testing.T) {
	root := t.TempDir()
	data := []byte("plain-data")
	writeKitFixture(t, root, []kitFile{
		{FileEntry{Path: "tool.js", Kind: "data", SHA256: DigestBytes(data)}, data},
		{FileEntry{Path: "bin/addon.node", Kind: "macho", SHA256: DigestBytes(machoLE64)}, machoLE64},
	})
	if _, err := verify(root); err != nil {
		t.Fatalf("valid kit with unsigned macho rejected: %v", err)
	}
	// Swap the macho bytes without touching the manifest: the sha256 branch must reject.
	tampered := append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("tampered-body")...)
	if err := os.WriteFile(filepath.Join(root, "bin", "addon.node"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err == nil {
		t.Fatal("tampered unsigned macho accepted")
	}
}

// TestVerifyMachOWithTeamIDFailsClosedWhenUnverifiable proves a Team-ID-bound
// macho whose signature cannot be confirmed (an unsigned blob here, and always
// off macOS) is rejected rather than trusted.
func TestVerifyMachOWithTeamIDFailsClosedWhenUnverifiable(t *testing.T) {
	root := t.TempDir()
	writeKitFixture(t, root, []kitFile{
		{FileEntry{Path: "bin/addon.node", Kind: "macho", TeamID: "ABCDE12345"}, machoLE64},
	})
	if _, err := verify(root); err == nil {
		t.Fatal("macho with an unverifiable declared Team ID was accepted")
	}
}

func TestVerifyRejectsDataTamper(t *testing.T) {
	root := t.TempDir()
	data := []byte("original")
	writeKitFixture(t, root, []kitFile{{FileEntry{Path: "a.js", Kind: "data", SHA256: DigestBytes(data)}, data}})
	if err := os.WriteFile(filepath.Join(root, "a.js"), []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err == nil {
		t.Fatal("tampered data file accepted")
	}
}

func TestVerifyRejectsBadKitDigest(t *testing.T) {
	root := t.TempDir()
	data := []byte("x")
	if err := os.WriteFile(filepath.Join(root, "a.js"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Manifest with a deliberately wrong kit_digest.
	m := Manifest{SchemaVersion: SchemaVersion, FlueCommit: ExpectedFlueCommit, NodeVersion: ExpectedNodeVersion, KitDigest: DigestBytes([]byte("not-the-real-digest")), Files: []FileEntry{{Path: "a.js", Kind: "data", SHA256: DigestBytes(data)}}}
	manifest, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kit-manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err == nil {
		t.Fatal("manifest with a forged kit_digest accepted")
	}
}

func TestVerifyRejectsSymlinkEntry(t *testing.T) {
	root := t.TempDir()
	data := []byte("real")
	writeKitFixture(t, root, []kitFile{{FileEntry{Path: "a.js", Kind: "data", SHA256: DigestBytes(data)}, data}})
	// Replace the declared file with a symlink to an outside target.
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "a.js")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(root); err == nil {
		t.Fatal("symlink kit entry accepted")
	}
}

func TestVerifyRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	// The path check runs before any file read, so the target need not exist.
	writeManifest(t, root, []FileEntry{{Path: "../evil", Kind: "data", SHA256: DigestBytes([]byte("x"))}})
	if _, err := verify(root); err == nil {
		t.Fatal("path-traversal manifest entry accepted")
	}
}
