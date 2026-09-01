package agentprofile

// Semantic verification of MANAGED profile content: provisioned files the
// harness legitimately rewrites at runtime.
//
// This is deliberately a separate file from manifest.go: manifest.go owns the
// BYTE scheme (Fingerprint over the `files` allowlist) and this owns the
// SEMANTIC one. They answer different questions and must not be read as one
// mechanism.
//
// Why it exists. settings.json is provisioned content and runtime-mutable
// content at the same time. On 2026-08-30 Claude Code re-serialized `worker`'s
// settings.json with a different key order and a new "enabledPlugins" key; the
// byte hash tripped, every spawn refused, and Bless — which refuses on a
// fingerprint mismatch by design — could not repair it. Enabling a plugin
// bricked an agent.
//
// The fix is not an ignore-list of harness-owned keys: that is the same trap
// one level up, since the next runtime key the harness invents re-bricks the
// fleet. Instead the provisioner keeps an immutable copy of what it wrote under
// .provisioned/, byte-hashes THAT, and the live file is checked against it
// semantically: every key the baseline declares must still be present and
// deep-equal. Extra keys are the harness's business.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ProvisionedDirName is the subdirectory of a profile root holding the pristine
// copy of every `managed` file, exactly as the provisioner wrote it. Nothing
// but the provisioner ever writes under it, which is what makes byte-hashing
// the copy safe when byte-hashing the live file is not.
//
// The provisioner (scripts/provision-profile.sh) names the same directory; the
// two are traceable to one another through this constant's name.
const ProvisionedDirName = ".provisioned"

// verifyManaged asserts, for every managed relative path, that the provisioned
// baseline under ProvisionedDirName is still a subset of the live file.
//
// A nil/empty list is a no-op, which is what preserves the behavior of every
// manifest currently on disk: they have no "managed" key at all, so their
// settings.json stays in `files` and stays byte-hashed.
//
// The two unreadable cases are deliberately DIFFERENT sentinels, because they
// have different repairs. A missing baseline is a provisioning fault (the
// manifest names a managed file that was never provisioned) — ErrManifestUnreadable.
// A missing or unparseable LIVE file is content drift: the agent deleted or
// truncated its own settings, and a profile with no settings is not the
// provisioned profile.
func verifyManaged(dir string, managed []string) error {
	for _, rel := range managed {
		basePath := filepath.Join(dir, ProvisionedDirName, rel)
		baseRaw, err := os.ReadFile(basePath) //nolint:gosec // G304: rel comes from the profile's own manifest
		if err != nil {
			return fmt.Errorf("%w: %s: managed baseline %s: %v (re-provision the profile)",
				ErrManifestUnreadable, dir, filepath.Join(ProvisionedDirName, rel), err)
		}
		liveRaw, err := os.ReadFile(filepath.Join(dir, rel)) //nolint:gosec // G304: rel comes from the profile's own manifest
		if err != nil {
			return fmt.Errorf("%w: %s: %s: %v (re-provision the profile)",
				ErrManagedContentDrift, dir, rel, err)
		}

		var want, got any
		if err := json.Unmarshal(baseRaw, &want); err != nil {
			return fmt.Errorf("%w: %s: %s: provisioned baseline is not valid JSON: %v (re-provision the profile)",
				ErrManagedContentDrift, dir, rel, err)
		}
		if err := json.Unmarshal(liveRaw, &got); err != nil {
			return fmt.Errorf("%w: %s: %s: on-disk file is not valid JSON: %v (re-provision the profile)",
				ErrManagedContentDrift, dir, rel, err)
		}

		if path, ok := subsetOf(want, got); !ok {
			// An empty path is a divergence at the document ROOT: the baseline
			// is an object and the live file is an array, or vice versa. Name
			// it rather than emitting an empty path segment.
			label := path
			if label == "" {
				label = "(document root)"
			}
			return fmt.Errorf("%w: %s: %s: %s: provisioned %s, on disk %s (re-provision the profile)",
				ErrManagedContentDrift, dir, rel, label,
				renderValue(valueAt(want, path)), renderValue(valueAt(got, path)))
		}
	}
	return nil
}

// subsetOf reports whether want is contained in got, and on failure the dotted
// JSON path of the FIRST divergence — the path is the operator-facing value, so
// it is part of the contract, not a debugging aid.
//
// Containment is asymmetric only for objects: every key want declares must
// exist in got and recurse. Arrays are exact, order significant — appending to
// permissions.allow is a real change to what the agent may do, not an
// extension. Everything else is reflect.DeepEqual; both sides come out of
// encoding/json so numbers are float64 on both and compare stably.
func subsetOf(want, got any) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return "", false
		}
		// Sorted, so an operator staring at two divergences twice gets the
		// same path named both times; Go's map order would not.
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			wv := w[k]
			gv, present := g[k]
			if !present {
				return k, false
			}
			if sub, ok := subsetOf(wv, gv); !ok {
				return joinPath(k, sub), false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return "", false
		}
		for i, wv := range w {
			if sub, ok := subsetOf(wv, g[i]); !ok {
				return joinPath("["+strconv.Itoa(i)+"]", sub), false
			}
		}
		return "", true
	default:
		return "", reflect.DeepEqual(want, got)
	}
}

// joinPath appends a child path segment to a parent key, dotting object steps
// and abutting array indices: permissions.allow[2].tool.
func joinPath(head, tail string) string {
	switch {
	case tail == "":
		return head
	case strings.HasPrefix(tail, "["):
		return head + tail
	default:
		return head + "." + tail
	}
}

// valueAt walks a dotted/indexed path produced by subsetOf back into a decoded
// document, so the error can quote both sides at the point they diverge. A path
// that does not resolve (the key is missing on the live side — the common case)
// yields nil, which renderValue prints as "(absent)".
func valueAt(v any, path string) any {
	if path == "" {
		return v
	}
	for _, step := range splitPath(path) {
		switch cur := v.(type) {
		case map[string]any:
			child, ok := cur[step]
			if !ok {
				return nil
			}
			v = child
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i < 0 || i >= len(cur) {
				return nil
			}
			v = cur[i]
		default:
			return nil
		}
	}
	return v
}

// splitPath is joinPath's inverse: "permissions.allow[2].tool" -> the four
// steps, with the bracket stripped off index steps.
func splitPath(path string) []string {
	var steps []string
	for _, dotted := range strings.Split(path, ".") {
		for i, part := range strings.Split(dotted, "[") {
			if i > 0 {
				part = strings.TrimSuffix(part, "]")
			}
			if part != "" {
				steps = append(steps, part)
			}
		}
	}
	return steps
}

// renderValue quotes a decoded JSON value compactly for the operator-facing
// error. Absent values read as "(absent)" rather than "null", which is a value
// a settings file can legitimately hold.
func renderValue(v any) string {
	if v == nil {
		return "(absent)"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
