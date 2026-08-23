package authoringkit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const SchemaVersion = "1"

const (
	ExpectedFlueCommit  = "492bf47b9f3d6c379d00471523987b8fe9511f7d"
	ExpectedNodeVersion = "22.20.0"
)

var ExpectedKitDigest string
var (
	ErrMissing = errors.New("authoring_kit_missing")
	ErrInvalid = errors.New("authoring_kit_invalid")
)

type FileEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}
type Manifest struct {
	SchemaVersion string      `json:"schema_version"`
	KitDigest     string      `json:"kit_digest"`
	FlueCommit    string      `json:"flue_commit"`
	NodeVersion   string      `json:"node_version"`
	Files         []FileEntry `json:"files"`
}
type Kit struct {
	Root     string
	Manifest Manifest
	Digest   string
}

var (
	lookupOnce sync.Once
	lookupKit  *Kit
	lookupErr  error
)

func Root() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_AUTHORING_KIT_DIR")); v != "" {
		return v
	}
	if e, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(e), "..", "Resources", "authoring-kit")
	}
	return ""
}
func ResetForTest() { lookupOnce = sync.Once{}; lookupKit = nil; lookupErr = nil }
func Lookup() (*Kit, error) {
	lookupOnce.Do(func() { lookupKit, lookupErr = verify(Root()) })
	return lookupKit, lookupErr
}
func CanonicalBytes(m Manifest) ([]byte, error) {
	m.Files = append([]FileEntry(nil), m.Files...)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return nil, e
	}
	return append(b, '\n'), nil
}
func DigestBytes(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

// IsMachO reports whether data begins with a Mach-O (thin or universal) magic.
// A false positive (e.g. a 0xCAFEBABE Java class, which the kit never ships) is
// harmless: it only routes the file through the stricter macho verification,
// and the classification is deterministic so packager and verifier agree.
func IsMachO(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	switch binary.BigEndian.Uint32(data[:4]) {
	case 0xFEEDFACE, 0xFEEDFACF, // thin, big-endian 32/64
		0xCEFAEDFE, 0xCFFAEDFE, // thin, little-endian 32/64
		0xCAFEBABE, 0xCAFEBABF, // universal (fat), 32/64
		0xBEBAFECA, 0xBFBAFECA: // universal (fat), byte-swapped
		return true
	}
	return false
}

//nolint:cyclop,gocognit,funlen // Verification is intentionally a linear fail-closed audit.
func verify(root string) (*Kit, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: resource root is empty", ErrMissing)
	}
	mp := filepath.Join(root, "kit-manifest.json")
	raw, e := os.ReadFile(mp) //nolint:gosec // root is resolved by the kit locator and the manifest name is fixed.
	if e != nil {
		return nil, fmt.Errorf("%w: %v", ErrMissing, e)
	}
	var m Manifest
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrInvalid, e)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema_version", ErrInvalid)
	}
	if m.FlueCommit != ExpectedFlueCommit || m.NodeVersion != ExpectedNodeVersion {
		return nil, fmt.Errorf("%w: pin", ErrInvalid)
	}
	decl := m.KitDigest
	m.KitDigest = ""
	c, e := CanonicalBytes(m)
	if e != nil {
		return nil, e
	}
	obs := DigestBytes(c)
	if decl != obs {
		return nil, fmt.Errorf("%w: kit_digest", ErrInvalid)
	}
	if ExpectedKitDigest != "" && ExpectedKitDigest != obs {
		return nil, fmt.Errorf("%w: kit_digest", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, x := range m.Files {
		clean := filepath.Clean(filepath.FromSlash(x.Path))
		if x.Kind != "data" && x.Kind != "macho" || clean == "." || filepath.IsAbs(clean) || clean != filepath.FromSlash(x.Path) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: path", ErrInvalid)
		}
		seen[x.Path] = true
		p := filepath.Join(root, clean)
		i, e := os.Lstat(p)
		if e != nil || i.Mode()&os.ModeSymlink != 0 || i.IsDir() {
			return nil, fmt.Errorf("%w: file %s", ErrInvalid, x.Path)
		}
		b, e := os.ReadFile(p) //nolint:gosec // p is joined from a validated manifest-relative path.
		if e != nil {
			return nil, e
		}
		switch x.Kind {
		case "data":
			s := sha256.Sum256(b)
			if strings.TrimPrefix(strings.ToLower(x.SHA256), "sha256:") != hex.EncodeToString(s[:]) {
				return nil, fmt.Errorf("%w: sha256", ErrInvalid)
			}
		case "macho":
			// A declared Team ID means the Mach-O is code-signed: deep-signing
			// the app rewrites its bytes, so a baked content hash would be stale.
			// Bind identity to the signature's Team ID, which survives
			// notarization. An unsigned Mach-O (developer/CI kit) has no Team ID
			// and is bound by content hash like any data file. The choice is
			// itself covered by kit_digest, so it cannot be silently downgraded.
			if x.TeamID != "" {
				team, err := MachOTeamID(p)
				if err != nil {
					return nil, fmt.Errorf("%w: macho signature: %v", ErrInvalid, err)
				}
				if team != x.TeamID {
					return nil, fmt.Errorf("%w: team_id", ErrInvalid)
				}
			} else {
				s := sha256.Sum256(b)
				if strings.TrimPrefix(strings.ToLower(x.SHA256), "sha256:") != hex.EncodeToString(s[:]) {
					return nil, fmt.Errorf("%w: sha256", ErrInvalid)
				}
			}
		}
	}
	if e = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == mp || d.IsDir() {
			return nil
		}
		r, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if !seen[filepath.ToSlash(r)] {
			return fmt.Errorf("%w: extra_file", ErrInvalid)
		}
		return nil
	}); e != nil {
		return nil, e
	}
	return &Kit{Root: root, Manifest: m, Digest: obs}, nil
}
func Describe() map[string]any {
	k, e := Lookup()
	r := map[string]any{"ready": e == nil}
	if k != nil {
		r["root"] = k.Root
		r["digest"] = k.Digest
	}
	if e != nil {
		r["error"] = e.Error()
	}
	return r
}
