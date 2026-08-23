package workflows

// This file contains the small, host-side part of the authoring contract. The
// kit is deliberately resolved here (rather than in the CLI) so HTTP callers
// and the CLI have identical fail-closed behavior.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/noderuntime"
	"github.com/tysonthomas9/loomcli/internal/workflows/authoringkit"
	"github.com/tysonthomas9/loomcli/internal/workflows/buildsandbox"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

type AuthoringToolchain struct {
	Node, Flue                                 string
	KitRoot, SDKRoot, RuntimeRoot, DaytonaRoot string
	Source, BuildID                            string
}

// ResolveAuthoringToolchain applies the documented precedence. An explicit
// override is atomic: a partially configured override never falls through to
// a different checkout or PATH tool.
func ResolveAuthoringToolchain() (AuthoringToolchain, error) {
	if hasAuthoringOverride() {
		return resolveOverrideToolchain()
	}
	if kit, err := authoringkit.Lookup(); err == nil {
		return resolveKitToolchain(kit)
	} else if packaged.FailClosed() {
		// Packaged builds (desktop runtime or a baked index digest) never fall
		// through to a developer PATH toolchain: if the verified kit is missing
		// or invalid, authoring fails closed with the kit's own error.
		return AuthoringToolchain{}, err
	}
	return resolveDeveloperToolchain()
}

func hasAuthoringOverride() bool {
	for _, k := range []string{"LOOM_REAL_FLUE_CMD_JSON", "LOOM_REAL_FLUE_CMD", "LOOM_SDK_ROOT", "LOOM_FLUE_RUNTIME_ROOT", "FLUE_RUNTIME_ROOT", "FLUE_REPO", "DAYTONA_SDK_ROOT"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}
func resolveOverrideToolchain() (AuthoringToolchain, error) {
	node, err := noderuntime.Resolve()
	if err != nil {
		return AuthoringToolchain{}, fmt.Errorf("node_runtime_missing: %w", err)
	}
	cmd, err := flueCommand()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	sdk, err := loomSDKRoot()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	runtimeRoot, err := flueRuntimeRoot()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	daytona, _ := daytonaSDKRoot()
	return AuthoringToolchain{Node: node.Path, Flue: strings.Join(cmd, " "), SDKRoot: sdk, RuntimeRoot: runtimeRoot, DaytonaRoot: daytona, Source: "override", BuildID: "override"}, nil
}
func resolveDeveloperToolchain() (AuthoringToolchain, error) {
	node, err := noderuntime.Resolve()
	if err != nil {
		return AuthoringToolchain{}, fmt.Errorf("node_runtime_missing: %w", err)
	}
	cmd, err := flueCommand()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	sdk, err := loomSDKRoot()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	runtimeRoot, err := flueRuntimeRoot()
	if err != nil {
		return AuthoringToolchain{}, err
	}
	daytona, _ := daytonaSDKRoot()
	return AuthoringToolchain{Node: node.Path, Flue: strings.Join(cmd, " "), SDKRoot: sdk, RuntimeRoot: runtimeRoot, DaytonaRoot: daytona, Source: "developer", BuildID: "developer"}, nil
}
func resolveKitToolchain(kit *authoringkit.Kit) (AuthoringToolchain, error) {
	node := filepath.Join(kit.Root, "node", "bin", "node")
	if _, err := os.Stat(node); err != nil {
		resolved, err := noderuntime.Resolve()
		if err != nil {
			return AuthoringToolchain{}, fmt.Errorf("node_runtime_missing: %w", err)
		}
		node = resolved.Path
	}
	flue := filepath.Join(kit.Root, "flue-cli", "bin", "flue.mjs")
	if _, err := os.Stat(flue); err != nil {
		return AuthoringToolchain{}, fmt.Errorf("authoring_kit_invalid: flue cli: %w", err)
	}
	return AuthoringToolchain{Node: node, Flue: flue, KitRoot: kit.Root, SDKRoot: filepath.Join(kit.Root, "loom-sdk"), RuntimeRoot: filepath.Join(kit.Root, "flue-runtime"), DaytonaRoot: filepath.Join(kit.Root, "daytona-sdk"), Source: "kit", BuildID: kit.Digest}, nil
}

func (t AuthoringToolchain) Command() ([]string, error) {
	if t.Source == "kit" {
		return []string{t.Node, t.Flue}, nil
	}
	parts := strings.Fields(t.Flue)
	if len(parts) == 0 {
		return nil, fmt.Errorf("flue command is empty")
	}
	return parts, nil
}

func (t AuthoringToolchain) Description() map[string]any {
	return map[string]any{"source": t.Source, "build_id": t.BuildID, "node": t.Node, "flue": t.Flue, "kit": t.KitRoot, "loom_sdk": t.SDKRoot, "flue_runtime": t.RuntimeRoot, "daytona_sdk": t.DaytonaRoot}
}

func authoringReadyDescription() map[string]any {
	kit, err := authoringkit.Lookup()
	sandbox := "unavailable"
	if mode, e := buildsandbox.Mode(packaged.FailClosed()); e == nil {
		sandbox = mode
	}
	result := map[string]any{"source": "missing", "sandbox": sandbox, "ready": false}
	if kit != nil {
		result["source"] = "kit"
		result["digest"] = kit.Digest
	}
	if err != nil {
		result["error"] = err.Error()
	}
	if tc, e := ResolveAuthoringToolchain(); e == nil {
		result["toolchain"] = tc.Description()
		result["ready"] = true
	}
	return result
}

// AuthoringReadiness is intentionally a data-only API so readyz can expose
// actionable kit state without importing the CLI or leaking raw environment.
func AuthoringReadiness() map[string]any { return authoringReadyDescription() }

func marshalAuthoringDescription() string {
	b, _ := json.Marshal(authoringReadyDescription())
	return string(b)
}
