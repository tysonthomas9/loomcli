package editor

import (
	"errors"
	"os"
	"testing"
)

var errNotFound = errors.New("not found")

// mockLookPath returns a function that resolves only the given set of commands.
func mockLookPath(available map[string]string) func(string) (string, error) {
	return func(cmd string) (string, error) {
		if p, ok := available[cmd]; ok {
			return p, nil
		}
		return "", errNotFound
	}
}

// mockStat returns a function that succeeds only for the given set of paths.
func mockStat(available map[string]bool) func(string) (os.FileInfo, error) {
	return func(path string) (os.FileInfo, error) {
		if available[path] {
			return nil, nil
		}
		return nil, errNotFound
	}
}

func TestDetectEditorViaCLI(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"code": "/usr/bin/code"})
	statFn = mockStat(nil)

	d, ok := detectEditor(Registry[0], "darwin") // VS Code
	if !ok {
		t.Fatal("expected VS Code to be detected via CLI")
	}
	if d.ResolvedPath != "/usr/bin/code" {
		t.Errorf("resolved path = %q, want /usr/bin/code", d.ResolvedPath)
	}
	if d.Method != "cli" {
		t.Errorf("method = %q, want cli", d.Method)
	}
	if d.ID != "vscode" {
		t.Errorf("ID = %q, want vscode", d.ID)
	}
}

func TestDetectEditorViaAppPath(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(nil)
	statFn = mockStat(map[string]bool{"/Applications/Visual Studio Code.app": true})

	d, ok := detectEditor(Registry[0], "darwin")
	if !ok {
		t.Fatal("expected VS Code to be detected via app path")
	}
	if d.ResolvedPath != "/Applications/Visual Studio Code.app" {
		t.Errorf("resolved path = %q, want /Applications/Visual Studio Code.app", d.ResolvedPath)
	}
	if d.Method != "app" {
		t.Errorf("method = %q, want app", d.Method)
	}
}

func TestDetectEditorNotFound(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(nil)
	statFn = mockStat(nil)

	_, ok := detectEditor(Registry[0], "darwin")
	if ok {
		t.Fatal("expected VS Code to not be detected")
	}
}

func TestDetectEditorCLIPreferredOverApp(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"code": "/usr/local/bin/code"})
	statFn = mockStat(map[string]bool{"/Applications/Visual Studio Code.app": true})

	d, ok := detectEditor(Registry[0], "darwin")
	if !ok {
		t.Fatal("expected VS Code to be detected")
	}
	if d.Method != "cli" {
		t.Errorf("expected CLI to be preferred, got method = %q", d.Method)
	}
	if d.ResolvedPath != "/usr/local/bin/code" {
		t.Errorf("resolved path = %q, want /usr/local/bin/code", d.ResolvedPath)
	}
}

func TestDetectEditorUnknownGOOS(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"code": "/usr/bin/code"})
	statFn = mockStat(nil)

	_, ok := detectEditor(Registry[0], "freebsd")
	if ok {
		t.Fatal("expected no detection on unsupported GOOS")
	}
}

func TestDetectedEditorsForOS(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	// Only VS Code and Zed CLI available.
	lookPathFn = mockLookPath(map[string]string{
		"code": "/usr/bin/code",
		"zed":  "/usr/bin/zed",
	})
	statFn = mockStat(nil)

	detected := DetectedEditorsForOS("darwin")
	if len(detected) != 2 {
		t.Fatalf("expected 2 detected editors, got %d", len(detected))
	}
	if detected[0].ID != "vscode" {
		t.Errorf("first detected editor = %q, want vscode", detected[0].ID)
	}
	if detected[1].ID != "zed" {
		t.Errorf("second detected editor = %q, want zed", detected[1].ID)
	}
}

func TestDetectedEditorsUsesRuntimeGOOS(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(nil)
	statFn = mockStat(nil)

	if got := DetectedEditors(); len(got) != 0 {
		t.Fatalf("DetectedEditors = %+v, want none with mocked lookups", got)
	}
}

func TestDetectedEditorsForOSEmpty(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(nil)
	statFn = mockStat(nil)

	detected := DetectedEditorsForOS("darwin")
	if len(detected) != 0 {
		t.Fatalf("expected 0 detected editors, got %d", len(detected))
	}
}

func TestDetectedEditorsForOSUnknownPlatform(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"code": "/usr/bin/code"})
	statFn = mockStat(nil)

	detected := DetectedEditorsForOS("freebsd")
	if len(detected) != 0 {
		t.Fatalf("expected 0 detected editors on unknown GOOS, got %d", len(detected))
	}
}

func TestRegistryCompleteness(t *testing.T) {
	if len(Registry) != 13 {
		t.Fatalf("expected 13 editors in registry, got %d", len(Registry))
	}

	ids := make(map[string]bool)
	for _, e := range Registry {
		if e.ID == "" {
			t.Error("editor has empty ID")
		}
		if e.DisplayName == "" {
			t.Errorf("editor %q has empty DisplayName", e.ID)
		}
		if e.IconName == "" {
			t.Errorf("editor %q has empty IconName", e.ID)
		}
		if len(e.Platforms) == 0 {
			t.Errorf("editor %q has no platform configs", e.ID)
		}
		if ids[e.ID] {
			t.Errorf("duplicate editor ID %q", e.ID)
		}
		ids[e.ID] = true

		// Every editor must have at least one platform with at least one detection method.
		hasDetection := false
		for _, pc := range e.Platforms {
			if len(pc.CLICommands) > 0 || len(pc.AppPaths) > 0 {
				hasDetection = true
				break
			}
		}
		if !hasDetection {
			t.Errorf("editor %q has no detection methods on any platform", e.ID)
		}
	}
}

func TestXcodeDetectedViaCLI(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"xed": "/usr/bin/xed"})
	statFn = mockStat(nil)

	d, ok := detectEditor(findEditor("xcode"), "darwin")
	if !ok {
		t.Fatal("expected Xcode to be detected via xed CLI")
	}
	if d.ResolvedPath != "/usr/bin/xed" {
		t.Errorf("resolved path = %q, want /usr/bin/xed", d.ResolvedPath)
	}
	if d.Method != "cli" {
		t.Errorf("method = %q, want cli", d.Method)
	}
}

func TestForkDetectedViaCLI(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{"fork": "/usr/local/bin/fork"})
	statFn = mockStat(nil)

	d, ok := detectEditor(findEditor("fork"), "darwin")
	if !ok {
		t.Fatal("expected Fork to be detected via fork CLI")
	}
	if d.ResolvedPath != "/usr/local/bin/fork" {
		t.Errorf("resolved path = %q, want /usr/local/bin/fork", d.ResolvedPath)
	}
	if d.Method != "cli" {
		t.Errorf("method = %q, want cli", d.Method)
	}
}

func TestForkWindowsLaunchMethodOverride(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(nil)
	// Simulate expanded path (env var already resolved).
	statFn = mockStat(map[string]bool{"/fake/local/Fork/Fork.exe": true})

	forkEditor := findEditor("fork")
	// Override the windows app path for testing (avoid env var dependency).
	testEditor := Editor{
		ID:          forkEditor.ID,
		DisplayName: forkEditor.DisplayName,
		IconName:    forkEditor.IconName,
		Platforms: map[string]PlatformConfig{
			"windows": {
				AppPaths:     []string{"/fake/local/Fork/Fork.exe"},
				LaunchMethod: "app",
			},
		},
	}

	d, ok := detectEditor(testEditor, "windows")
	if !ok {
		t.Fatal("expected Fork to be detected on Windows via app path")
	}
	if d.Method != "app" {
		t.Errorf("method = %q, want app (LaunchMethod override)", d.Method)
	}
}

func TestNullLabelsEdgeCase(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	// Editor with empty platform config (no CLI commands, no app paths).
	e := Editor{
		ID:          "test-empty",
		DisplayName: "Test",
		IconName:    "test",
		Platforms: map[string]PlatformConfig{
			"darwin": {},
		},
	}

	lookPathFn = mockLookPath(nil)
	statFn = mockStat(nil)

	_, ok := detectEditor(e, "darwin")
	if ok {
		t.Fatal("expected editor with empty platform config to not be detected")
	}
}

func TestDetectEditorSecondCLI(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	// Zed on linux has two CLI commands: "zed" and "zeditor".
	// First fails, second succeeds.
	lookPathFn = mockLookPath(map[string]string{"zeditor": "/usr/bin/zeditor"})
	statFn = mockStat(nil)

	d, ok := detectEditor(findEditor("zed"), "linux")
	if !ok {
		t.Fatal("expected Zed to be detected via second CLI command")
	}
	if d.ResolvedPath != "/usr/bin/zeditor" {
		t.Errorf("resolved path = %q, want /usr/bin/zeditor", d.ResolvedPath)
	}
	if d.Method != "cli" {
		t.Errorf("method = %q, want cli", d.Method)
	}
}

func TestDetectEditorSecondApp(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	// IntelliJ on darwin has two app paths; first fails, second found.
	lookPathFn = mockLookPath(nil)
	statFn = mockStat(map[string]bool{"/Applications/IntelliJ IDEA CE.app": true})

	d, ok := detectEditor(findEditor("intellij"), "darwin")
	if !ok {
		t.Fatal("expected IntelliJ to be detected via second app path")
	}
	if d.ResolvedPath != "/Applications/IntelliJ IDEA CE.app" {
		t.Errorf("resolved path = %q, want /Applications/IntelliJ IDEA CE.app", d.ResolvedPath)
	}
	if d.Method != "app" {
		t.Errorf("method = %q, want app", d.Method)
	}
}

func TestAllEditorsDetectableForOS(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	// Collect all CLI commands and app paths across all editors for darwin.
	allCLIs := make(map[string]string)
	allApps := make(map[string]bool)
	var expectedCount int
	for _, e := range Registry {
		pc, ok := e.Platforms["darwin"]
		if !ok {
			continue
		}
		expectedCount++
		if len(pc.CLICommands) > 0 {
			for _, cmd := range pc.CLICommands {
				allCLIs[cmd] = "/usr/bin/" + cmd
			}
		} else if len(pc.AppPaths) > 0 {
			allApps[pc.AppPaths[0]] = true
		}
	}

	lookPathFn = mockLookPath(allCLIs)
	statFn = mockStat(allApps)

	detected := DetectedEditorsForOS("darwin")
	if len(detected) != expectedCount {
		t.Errorf("expected %d detected editors on darwin, got %d", expectedCount, len(detected))
	}
}

func TestEditorIDs(t *testing.T) {
	expected := map[string]bool{
		"vscode":         true,
		"cursor":         true,
		"zed":            true,
		"intellij":       true,
		"pycharm":        true,
		"xcode":          true,
		"sublime":        true,
		"rider":          true,
		"android-studio": true,
		"sourcetree":     true,
		"windsurf":       true,
		"antigravity":    true,
		"fork":           true,
	}

	for _, e := range Registry {
		if !expected[e.ID] {
			t.Errorf("unexpected editor ID in registry: %q", e.ID)
		}
		delete(expected, e.ID)
	}
	for id := range expected {
		t.Errorf("expected editor ID missing from registry: %q", id)
	}
}

func TestDetectedEditorsLinux(t *testing.T) {
	origLookPath := lookPathFn
	origStat := statFn
	t.Cleanup(func() {
		lookPathFn = origLookPath
		statFn = origStat
	})

	lookPathFn = mockLookPath(map[string]string{
		"code":    "/usr/bin/code",
		"idea":    "/usr/bin/idea",
		"pycharm": "/usr/bin/pycharm",
	})
	statFn = mockStat(nil)

	detected := DetectedEditorsForOS("linux")
	if len(detected) != 3 {
		t.Fatalf("expected 3 detected editors on linux, got %d", len(detected))
	}

	ids := make(map[string]bool)
	for _, d := range detected {
		ids[d.ID] = true
	}
	for _, want := range []string{"vscode", "intellij", "pycharm"} {
		if !ids[want] {
			t.Errorf("expected %q to be detected on linux", want)
		}
	}
}

// findEditor looks up an editor by ID in the Registry.
func findEditor(id string) Editor {
	for _, e := range Registry {
		if e.ID == id {
			return e
		}
	}
	panic("editor not found: " + id)
}
