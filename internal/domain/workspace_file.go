package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// WorkspaceFileInput is one file's bytes and materialization metadata on the
// way into an immutable workspace file tree. Content identity is derived by
// the store rather than trusted from the caller.
type WorkspaceFileInput struct {
	Path       string
	Bytes      []byte
	MediaType  string
	Executable bool
}

// WorkspaceFile describes one immutable byte object. BlobRef is opaque and
// must never contain a provider URL, object key, endpoint, or credential.
type WorkspaceFile struct {
	Path        string
	BlobRef     string
	ContentHash string
	SizeBytes   int64
	MediaType   string
	Executable  bool
	Revision    string
}

// WorkspaceFileTree is one complete immutable manifest. Directories are
// represented implicitly by path prefixes.
type WorkspaceFileTree struct {
	WorkspaceKey string
	Revision     string
	Files        []WorkspaceFile
	CreatedBy    string
	CreatedAt    time.Time
}

func (t *WorkspaceFileTree) Clone() *WorkspaceFileTree {
	if t == nil {
		return nil
	}
	out := *t
	out.Files = append([]WorkspaceFile(nil), t.Files...)
	return &out
}

type WorkspaceFileTreeStatus string

const (
	WorkspaceFileTreeExisting          WorkspaceFileTreeStatus = "existing"
	WorkspaceFileTreePublished         WorkspaceFileTreeStatus = "published"
	WorkspaceFileTreeProjectionPending WorkspaceFileTreeStatus = "projection_pending"
)

type WorkspaceFileTreePublishResult struct {
	Tree     *WorkspaceFileTree
	Status   WorkspaceFileTreeStatus
	ETag     string
	Location string
}

var workspaceFileDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// WorkspaceFilePathKey is used only for collision detection. Stored paths
// retain their original valid UTF-8 bytes.
func WorkspaceFilePathKey(value string) string {
	return cases.Fold().String(norm.NFC.String(value))
}

// ValidateWorkspaceFilePath validates a provider-neutral relative path safe
// to materialize beneath a workspace-owned directory.
//
//nolint:funlen // Each unsafe path class keeps a distinct diagnostic.
func ValidateWorkspaceFilePath(value string) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf(format+": %w", append(args, ErrInvalid)...)
	}
	if value == "" {
		return invalid("workspace file path is required")
	}
	if len(value) > MaxWorkspaceFilePathLength {
		return invalid("workspace file path %q must be at most %d bytes", value, MaxWorkspaceFilePathLength)
	}
	if !utf8.ValidString(value) {
		return invalid("workspace file path must be valid UTF-8")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return invalid("workspace file path %q must not contain control characters (U+%04X)", value, r)
		}
	}
	if strings.Contains(value, `\`) {
		return invalid("workspace file path %q must not contain backslashes", value)
	}
	if strings.HasPrefix(value, "/") {
		return invalid("workspace file path %q must be relative, not absolute", value)
	}
	if strings.HasPrefix(value, "~") {
		return invalid("workspace file path %q must not start with ~", value)
	}
	if strings.Contains(value, ":") {
		return invalid("workspace file path %q must not contain a colon", value)
	}
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "":
			return invalid("workspace file path %q must not contain empty segments", value)
		case ".", "..":
			return invalid("workspace file path %q must not contain %q segments", value, segment)
		}
		if len(segment) > MaxWorkspaceFilePathSegmentLength {
			return invalid("workspace file path %q has a component longer than %d bytes", value, MaxWorkspaceFilePathSegmentLength)
		}
		device := WorkspaceFilePathKey(segment)
		if dot := strings.IndexByte(device, '.'); dot >= 0 {
			device = device[:dot]
		}
		if workspaceFileDeviceNames[device] {
			return invalid("workspace file path %q uses the reserved device name %q", value, segment)
		}
	}
	if path.Clean(value) != value {
		return invalid("workspace file path %q must be normalized (got %q)", value, path.Clean(value))
	}
	return nil
}

// CanonicalWorkspaceFileManifest validates and returns a detached manifest in
// path-byte order with every response-only file revision derived afresh.
func CanonicalWorkspaceFileManifest(files []WorkspaceFile) ([]WorkspaceFile, error) {
	if len(files) > MaxWorkspaceFiles {
		return nil, fmt.Errorf("workspace files must be at most %d entries: %w", MaxWorkspaceFiles, ErrInvalid)
	}
	filePaths := make(map[string]string, len(files))
	dirPaths := make(map[string]string, len(files))
	for _, file := range files {
		if err := ValidateWorkspaceFilePath(file.Path); err != nil {
			return nil, err
		}
		key := WorkspaceFilePathKey(file.Path)
		if previous, ok := filePaths[key]; ok {
			return nil, fmt.Errorf("workspace file paths %q and %q collide when materialized: %w", previous, file.Path, ErrInvalid)
		}
		filePaths[key] = file.Path
		segments := strings.Split(file.Path, "/")
		for i := 1; i < len(segments); i++ {
			dirPaths[WorkspaceFilePathKey(strings.Join(segments[:i], "/"))] = file.Path
		}
		if err := validateWorkspaceFileMetadata(file); err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		if nested, ok := dirPaths[WorkspaceFilePathKey(file.Path)]; ok {
			return nil, fmt.Errorf("workspace file path %q cannot be both a file and a directory (%q is inside it): %w", file.Path, nested, ErrInvalid)
		}
	}
	canonical := append([]WorkspaceFile(nil), files...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	for i := range canonical {
		canonical[i].Revision = WorkspaceFileRevision(canonical[i])
	}
	return canonical, nil
}

func validateWorkspaceFileMetadata(file WorkspaceFile) error {
	if file.BlobRef == "" || len(file.BlobRef) > MaxWorkspaceFileBlobRefLength || !utf8.ValidString(file.BlobRef) || containsControl(file.BlobRef) {
		return fmt.Errorf("workspace file %q has invalid blob ref: %w", file.Path, ErrInvalid)
	}
	if len(file.ContentHash) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(file.ContentHash, "sha256:") {
		return fmt.Errorf("workspace file %q content hash must be canonical sha256:<lower hex>: %w", file.Path, ErrInvalid)
	}
	digest := strings.TrimPrefix(file.ContentHash, "sha256:")
	if digest != strings.ToLower(digest) {
		return fmt.Errorf("workspace file %q content hash must be canonical sha256:<lower hex>: %w", file.Path, ErrInvalid)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("workspace file %q content hash must be canonical sha256:<lower hex>: %w", file.Path, ErrInvalid)
	}
	if file.SizeBytes < 0 {
		return fmt.Errorf("workspace file %q size must not be negative: %w", file.Path, ErrInvalid)
	}
	if len(file.MediaType) > MaxWorkspaceFileMediaTypeLength || !utf8.ValidString(file.MediaType) || containsControl(file.MediaType) {
		return fmt.Errorf("workspace file %q has invalid media type: %w", file.Path, ErrInvalid)
	}
	return nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
