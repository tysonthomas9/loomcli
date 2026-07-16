package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
)

// SourceDigest computes the CANONICAL source digest of a workflow source
// tree. It is the single recipe for every path that stamps or compares a
// workflow source_digest: the embedded builtin self-heal
// (EnsureBuiltinWorkflow), API/CLI workflow builds (BuildAndRegister), and
// out-of-band registration (`loom workflow digest` feeding
// `loom driver register --source-digest`, as the e2e stack scripts do).
// Every computation over the same source tree MUST agree — historically the
// slack e2e stack hashed an extra SDK file in unsorted order, so its recorded
// digests could never match the embedded ones and the self-heal fast path
// never hit.
//
// Recipe (deterministic by construction):
//  1. Keys are the workflow-relative file paths, normalized to forward
//     slashes (filepath.ToSlash).
//  2. Entries are hashed in ascending lexicographic order of the normalized
//     key — never map/insertion order.
//  3. Each entry contributes key, NUL, content, NUL; the NUL framing makes
//     the (key, content) boundaries unambiguous.
//  4. The result is "sha256:" + lowercase hex of the SHA-256 sum.
//
// Only workflow source files belong in the map — no SDK/runtime files and no
// build outputs (bundle content is covered by the separate bundle digest).
func SourceDigest(files map[string]string) string {
	normalized := make(map[string]string, len(files))
	keys := make([]string, 0, len(files))
	for key, content := range files {
		k := filepath.ToSlash(key)
		normalized[k] = content
		keys = append(keys, k)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write([]byte(normalized[key]))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
