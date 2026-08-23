package workflows

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// writeStubLoomSDKRuntime writes the @loom/sdk runtime files a successful build
// stages into its dist (packaged.LoomSDKRuntimeFiles) as minimal stubs. Fake-flue
// build tests point LOOM_SDK_ROOT at a directory populated by this helper so
// stageLoomSDKRuntime finds every file it copies, mirroring a real SDK checkout.
func writeStubLoomSDKRuntime(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create stub @loom/sdk dir: %v", err)
	}
	for _, name := range packaged.LoomSDKRuntimeFiles {
		content := "export {};\n"
		if name == "package.json" {
			content = `{"name":"@loom/sdk"}` + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write stub @loom/sdk %s: %v", name, err)
		}
	}
}
