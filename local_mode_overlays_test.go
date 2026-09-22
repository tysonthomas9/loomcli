package main

import (
	"os"
	"strings"
	"testing"
)

// The topology/pipeline overlays reshape the local-mode stack's agent
// topology, not how it is built or published. The tests below guard that
// boundary, because crossing it is silent: an overlay that declares its own
// ports or image tags comes up looking healthy and takes over the demo stack's
// published ports or overwrites its images.
var localModeTopologyOverlays = []string{
	"test/local-mode/docker-compose.topology.yml",
	"test/local-mode/docker-compose.pipeline.yml",
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(repoRoot(t) + "/" + rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// Compose MERGES `environment` maps but APPENDS `ports:` sequences, so an
// overlay that publishes its own ports keeps the base ones too: the shaped
// stack would publish the demo stack's ports as well, refuse to start whenever
// the demo stack is up, and otherwise answer for it. Parallel stacks come from
// LOCAL_MODE_*_PORT instead, which the base file already reads.
func TestLocalModeOverlaysDeclareNoPorts(t *testing.T) {
	t.Parallel()
	for _, overlay := range localModeTopologyOverlays {
		for _, line := range strings.Split(readRepoFile(t, overlay), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "ports:") {
				t.Errorf("%s declares ports (%q); use LOCAL_MODE_FLEETDB_PORT/_API_PORT/_UI_PORT", overlay, strings.TrimSpace(line))
			}
		}
	}
}

// Image tags default to the Compose project name (LOCAL_MODE_*_IMAGE in the
// Makefile) precisely so a second stack builds into its own tags. An overlay
// that names an image or a build context builds into the shared tag and
// clobbers the demo stack's images.
func TestLocalModeOverlaysDeclareNoImagesOrBuilds(t *testing.T) {
	t.Parallel()
	for _, overlay := range localModeTopologyOverlays {
		for _, line := range strings.Split(readRepoFile(t, overlay), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, key := range []string{"image:", "build:", "dockerfile:"} {
				if strings.HasPrefix(trimmed, key) {
					t.Errorf("%s declares %s; overlays must set environment only so LOCAL_MODE_*_IMAGE keeps stacks apart", overlay, key)
				}
			}
		}
	}
}

// The verify target must go through the shared engine detection in
// verify-lib.sh (podman first, then docker), like every other local-mode
// verify script. Hard-coding `docker compose` bypasses the podman path the
// rest of the repo relies on.
func TestLocalModePipelineVerifyUsesSharedEngineDetection(t *testing.T) {
	t.Parallel()
	script := readRepoFile(t, "test/local-mode/verify-pipeline.sh")
	if !strings.Contains(script, "verify-lib.sh") {
		t.Error("verify-pipeline.sh does not source verify-lib.sh; it would need its own engine detection")
	}
	for _, line := range strings.Split(script, "\n") {
		code, _, _ := strings.Cut(line, "#")
		for _, forbidden := range []string{"docker compose", "docker exec", "docker-compose "} {
			if strings.Contains(code, forbidden) {
				t.Errorf("verify-pipeline.sh hard-codes %q instead of using $ENGINE from verify-lib.sh", forbidden)
			}
		}
	}
}

// The Makefile must reach the pipeline verify the same way it reaches every
// other local-mode verify: a phony target running the script, with no compose
// invocation of its own.
func TestMakefileDeclaresPipelineVerify(t *testing.T) {
	t.Parallel()
	content := readRepoFile(t, "Makefile")
	if !strings.Contains(content, "local-mode-pipeline-verify:") {
		t.Fatal("Makefile has no local-mode-pipeline-verify target")
	}
	phony := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, ".PHONY:") {
			phony = line
			break
		}
	}
	if !strings.Contains(phony, "local-mode-pipeline-verify") {
		t.Error("local-mode-pipeline-verify is not declared .PHONY")
	}
}
