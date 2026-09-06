package agentprofile

// READING the provisioned baseline, as opposed to verifying against it.
//
// managed.go owns the semantic verification scheme this file borrows: the
// provisioner keeps an immutable copy of every `managed` file under
// .provisioned/, and the live file is checked against it as a subset. Read
// that file first — everything below assumes its vocabulary (`managed` vs.
// `files`, ProvisionedDirName, decodeManaged).
//
// The baseline is now also a LAUNCH INPUT, not only a verification target.
// A human typing /model in a lead session gets "saved as your default for new
// sessions", which rewrites `model` in the LIVE settings.json; until an
// operator reconciles it, the next lead session boots on whatever was last
// saved. Passing the provisioned value on the command line — where a CLI
// argument outranks settings.json — makes the START STATE immune, and demotes
// a drifted settings.json to a `loom doctor` cleanup item.
//
// The single-source-of-truth requirement is why this decodes with the SAME
// decodeManaged the verifier uses: the pin reads the exact file the verifier
// enforces, so changing the source template and re-provisioning moves the pin
// with no code change here.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProvisionedString returns the string value of a top-level key in the
// provisioned baseline of a managed file, e.g. ("model") in settings.json or
// config.toml.
//
// found=false with a nil error for every "there is nothing to pin" case, which
// callers treat as "no pin": no manifest, rel not listed in the manifest's
// `managed` list, no baseline file, key absent, key not a string, or an
// empty/whitespace value. An error is returned only when the baseline exists
// but cannot be decoded at all.
//
// rel MUST appear in `managed`. A file that is byte-hashed in `files` has no
// .provisioned/ copy, and falling back to the LIVE file instead would defeat
// the entire point — the live file is the drifted one.
//
// The key is flat on purpose: both real callers pin a top-level scalar, and a
// path API with no second caller is speculative surface.
func ProvisionedString(dir, rel, key string) (string, bool, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		// A profile that predates the managed scheme, or has no manifest at
		// all, simply has no pin. Refusing the launch is PUPPET-523's job and
		// deliberately not this function's: a resolver that can brick a boot
		// from inside an argv builder is a new way to brick the fleet.
		if errors.Is(err, ErrManifestMissing) || errors.Is(err, ErrManifestUnreadable) {
			return "", false, nil
		}
		return "", false, err
	}
	if !containsString(m.Managed, rel) {
		return "", false, nil
	}

	raw, err := os.ReadFile(filepath.Join(dir, ProvisionedDirName, rel)) //nolint:gosec // G304: rel comes from the profile's own manifest
	if err != nil {
		// A manifest naming a managed file that was never provisioned is a
		// provisioning fault, and the verifier already reports it as one. Here
		// it is only an absent pin.
		return "", false, nil
	}
	decoded, err := decodeManaged(rel, raw)
	if err != nil {
		return "", false, fmt.Errorf("%s: %s: %w", dir, filepath.Join(ProvisionedDirName, rel), err)
	}
	doc, ok := decoded.(map[string]any)
	if !ok {
		return "", false, nil
	}
	value, ok := doc[key].(string)
	if !ok {
		return "", false, nil
	}
	if value = strings.TrimSpace(value); value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
