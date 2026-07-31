// Package nativearchive owns the wire-format and resource policy for native
// Driver bundle uploads. The CLI uses it while constructing an archive and
// loom serve applies the same policy authoritatively while extracting it.
package nativearchive

import (
	"archive/tar"
	"fmt"
	"path"
	"strings"
)

const (
	MaxRequestBytes  = 48 << 20
	MaxArchiveBytes  = 32 << 20
	MaxExtractBytes  = 64 << 20
	MaxManifestBytes = 1 << 20
	MaxEntries       = 4096
)

// EntryKind is the closed set of archive entries accepted by loom serve.
type EntryKind uint8

const (
	EntryDirectory EntryKind = iota + 1
	EntryRegularFile
)

// CleanEntryName validates the canonical slash-separated archive coordinate.
func CleanEntryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	clean := path.Clean(name)
	if name == "" || clean == "." || clean != name || path.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, `\`) {
		return "", fmt.Errorf("native driver archive contains unsafe path %q", name)
	}
	return clean, nil
}

// ClassifyEntry admits only directories and regular files. Links, devices,
// FIFOs, GNU sparse entries, and extension records fail closed.
func ClassifyEntry(typeflag byte, name string) (EntryKind, error) {
	switch typeflag {
	case tar.TypeDir:
		return EntryDirectory, nil
	case tar.TypeReg:
		return EntryRegularFile, nil
	default:
		return 0, fmt.Errorf("native driver archive contains unsupported entry %q", name)
	}
}

// AccountEntry advances the entry and extraction budgets without allocating
// the advertised file size. Directories count toward the entry limit so a
// compressed directory-only inode bomb cannot bypass the policy.
func AccountEntry(entryCount int, extractedBytes, size int64, regular bool) (int, int64, error) {
	entryCount++
	if entryCount > MaxEntries {
		return entryCount, extractedBytes, fmt.Errorf("native driver archive exceeds extraction limits")
	}
	if !regular {
		return entryCount, extractedBytes, nil
	}
	if size < 0 {
		return entryCount, extractedBytes, fmt.Errorf("native driver archive file has invalid size")
	}
	if size > MaxExtractBytes || extractedBytes > MaxExtractBytes-size {
		return entryCount, extractedBytes, fmt.Errorf("native driver archive exceeds extraction limits")
	}
	extractedBytes += size
	return entryCount, extractedBytes, nil
}

func ValidateArchiveSize(size int) error {
	if size <= 0 || size > MaxArchiveBytes {
		return fmt.Errorf("native driver archive is missing or too large")
	}
	return nil
}

func ValidateManifestSize(size int) error {
	if size > MaxManifestBytes {
		return fmt.Errorf("native driver manifest is too large")
	}
	return nil
}
