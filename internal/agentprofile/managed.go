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
//
// The scheme was born JSON-only, which quietly excluded the harness that needed
// it most: codex writes its own [hooks.state] and [tui.model_availability_nux]
// tables into config.toml on essentially every run, so a byte-hashed
// config.toml bricks the next lead launch — and because enforceLeadProfile
// iterates every harness and exits on the first failure, a drifted CODEX
// profile also blocks `loom lead --backend claude`. Decoding is therefore keyed
// on the managed file's EXTENSION (see decodeManaged), so config.toml can move
// out of `files` and into `managed` and be verified semantically like
// settings.json always was.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
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

		// Both sides go through decodeManaged with the SAME rel. That is the
		// whole invariant subsetOf rests on — see its doc comment.
		want, err := decodeManaged(rel, baseRaw)
		if err != nil {
			if errors.Is(err, errUnsupportedManagedExt) {
				return fmt.Errorf("%w: %s: %s: %v (fix the provisioner, then re-provision the profile)",
					ErrManifestUnreadable, dir, rel, err)
			}
			return fmt.Errorf("%w: %s: %s: provisioned baseline %v (re-provision the profile)",
				ErrManagedContentDrift, dir, rel, err)
		}
		got, err := decodeManaged(rel, liveRaw)
		if err != nil {
			// The extension case cannot reach here: decodeManaged already
			// rejected it on the baseline above, before the live file was
			// considered. Keep the branch anyway so a future caller reordering
			// the two cannot silently reclassify a manifest fault as drift.
			if errors.Is(err, errUnsupportedManagedExt) {
				return fmt.Errorf("%w: %s: %s: %v (fix the provisioner, then re-provision the profile)",
					ErrManifestUnreadable, dir, rel, err)
			}
			return fmt.Errorf("%w: %s: %s: on-disk file %v (re-provision the profile)",
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

// errUnsupportedManagedExt marks the one decodeManaged failure that is NOT
// content drift: the manifest names a managed file in a format the verifier
// cannot read at all. Nothing the agent did causes that, and re-provisioning
// alone does not repair it — the provisioner has to learn the format first.
// verifyManaged translates it to ErrManifestUnreadable for exactly that reason.
var errUnsupportedManagedExt = errors.New("unsupported managed file extension")

// decodeManaged parses a managed file according to its extension.
//
// Both sides of a comparison MUST be decoded by this function with the SAME
// rel; see the decoder-mixing note on subsetOf for why that is load-bearing and
// not merely tidy.
//
// The extension set here and the one in the provisioner's managed-file
// validation (scripts/provision-profile.sh) must stay in step: the provisioner
// decides which files land in `managed`, and a file it accepts that this
// rejects is a profile that cannot be verified. They are traceable to one
// another the way ProvisionedDirName already is.
func decodeManaged(rel string, raw []byte) (any, error) {
	var v any
	switch ext := strings.ToLower(filepath.Ext(rel)); ext {
	case ".json":
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("is not valid JSON: %w", err)
		}
	case ".toml":
		if err := toml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("is not valid TOML: %w", err)
		}
	default:
		return nil, fmt.Errorf("%w %q", errUnsupportedManagedExt, ext)
	}
	return v, nil
}

// subsetOf reports whether want is contained in got, and on failure the dotted
// JSON path of the FIRST divergence — the path is the operator-facing value, so
// it is part of the contract, not a debugging aid.
//
// Containment is asymmetric only for objects: every key want declares must
// exist in got and recurse. Arrays are exact, order significant — appending to
// permissions.allow is a real change to what the agent may do, not an
// extension. Everything else is reflect.DeepEqual.
//
// DeepEqual is stable here only because both sides are produced by the same
// decodeManaged call for the same rel, so their number types agree — NOT
// because there is one number type. encoding/json decodes 1 into float64;
// go-toml decodes it into int64; reflect.DeepEqual(int64(1), float64(1)) is
// false. Never compare values produced by two different decoders, and do not
// "fix" that by normalizing one side: the shared decoder is the guarantee, and
// normalizing would weaken a comparison the trust_level entries depend on.
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

// renderValue quotes a decoded value compactly for the operator-facing error.
// Absent values read as "(absent)" rather than "null", which is a value a
// settings file can legitimately hold.
//
// It marshals as JSON even for TOML documents: every value shape codex writes
// (string, int64, nested table) renders readably. TOML's local date/time
// literals decode into go-toml types that carry MarshalText but not
// MarshalJSON, so they would render structurally; no managed key uses them, and
// this is error-path cosmetics only.
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
