// Package editor holds the registry of external editors and git clients (VS Code,
// Cursor, Zed, JetBrains, Sourcetree, ...), detects which are installed on the
// machine running loom, and launches one against a workspace path via a per-GOOS
// implementation. Consumed by internal/webui/handlers/misc for the /api/editors routes.
package editor

import (
	"os"
	"os/exec"
	"runtime"
)

// PlatformConfig holds detection and launch info for one OS.
type PlatformConfig struct {
	CLICommands  []string // e.g., ["code", "code-insiders"] — tried with exec.LookPath
	AppPaths     []string // e.g., ["/Applications/Visual Studio Code.app"] — tried with os.Stat
	LaunchMethod string   // "cli" or "app" — hints for launch logic
}

// Editor represents an external editor/tool that can open workspaces.
type Editor struct {
	ID          string                    // unique slug, e.g., "vscode"
	DisplayName string                    // human-readable, e.g., "VS Code"
	IconName    string                    // frontend icon key, e.g., "vscode"
	Platforms   map[string]PlatformConfig // keyed by GOOS: "darwin", "linux", "windows"
}

// DetectedEditor is an Editor confirmed to be installed on this machine.
type DetectedEditor struct {
	Editor
	ResolvedPath string // the CLI path or app path that was found
	Method       string // "cli" or "app" — how it was detected
}

// newCommandFn is the function used to create exec commands.
// It is a variable so tests can override it.
var newCommandFn = exec.Command

// lookPathFn is the function used to locate CLI commands.
// It is a variable so tests can override it.
var lookPathFn = exec.LookPath

// statFn is the function used to check app paths.
// It is a variable so tests can override it.
var statFn = os.Stat

// Registry contains all supported editors with their platform configs.
var Registry = []Editor{
	{
		ID:          "vscode",
		DisplayName: "VS Code",
		IconName:    "vscode",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"code"},
				AppPaths:    []string{"/Applications/Visual Studio Code.app"},
			},
			"linux": {
				CLICommands: []string{"code"},
			},
			"windows": {
				CLICommands: []string{"code"},
				AppPaths:    []string{"${LOCALAPPDATA}/Programs/Microsoft VS Code/Code.exe"},
			},
		},
	},
	{
		ID:          "cursor",
		DisplayName: "Cursor",
		IconName:    "cursor",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"cursor"},
				AppPaths:    []string{"/Applications/Cursor.app"},
			},
			"linux": {
				CLICommands: []string{"cursor"},
			},
			"windows": {
				CLICommands: []string{"cursor"},
				AppPaths:    []string{"${LOCALAPPDATA}/Programs/Cursor/Cursor.exe"},
			},
		},
	},
	{
		ID:          "zed",
		DisplayName: "Zed",
		IconName:    "zed",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"zed"},
				AppPaths:    []string{"/Applications/Zed.app"},
			},
			"linux": {
				CLICommands: []string{"zed", "zeditor"},
			},
		},
	},
	{
		ID:          "intellij",
		DisplayName: "IntelliJ IDEA",
		IconName:    "intellij",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"idea"},
				AppPaths:    []string{"/Applications/IntelliJ IDEA.app", "/Applications/IntelliJ IDEA CE.app"},
			},
			"linux": {
				CLICommands: []string{"idea"},
				AppPaths:    []string{"/snap/intellij-idea-ultimate/current", "/snap/intellij-idea-community/current"},
			},
			"windows": {
				CLICommands: []string{"idea"},
				AppPaths:    []string{"${ProgramFiles}/JetBrains/IntelliJ IDEA/bin/idea64.exe"},
			},
		},
	},
	{
		ID:          "pycharm",
		DisplayName: "PyCharm",
		IconName:    "pycharm",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"pycharm"},
				AppPaths:    []string{"/Applications/PyCharm.app", "/Applications/PyCharm CE.app"},
			},
			"linux": {
				CLICommands: []string{"pycharm"},
				AppPaths:    []string{"/snap/pycharm-professional/current", "/snap/pycharm-community/current"},
			},
			"windows": {
				CLICommands: []string{"pycharm"},
				AppPaths:    []string{"${ProgramFiles}/JetBrains/PyCharm/bin/pycharm64.exe"},
			},
		},
	},
	{
		ID:          "xcode",
		DisplayName: "Xcode",
		IconName:    "xcode",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"xed"},
				AppPaths:    []string{"/Applications/Xcode.app"},
			},
		},
	},
	{
		ID:          "sublime",
		DisplayName: "Sublime Text",
		IconName:    "sublime",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"subl"},
				AppPaths:    []string{"/Applications/Sublime Text.app"},
			},
			"linux": {
				CLICommands: []string{"subl"},
			},
			"windows": {
				CLICommands: []string{"subl"},
				AppPaths:    []string{"${ProgramFiles}/Sublime Text/sublime_text.exe"},
			},
		},
	},
	{
		ID:          "rider",
		DisplayName: "Rider",
		IconName:    "rider",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"rider"},
				AppPaths:    []string{"/Applications/Rider.app"},
			},
			"linux": {
				CLICommands: []string{"rider"},
				AppPaths:    []string{"/snap/rider/current"},
			},
			"windows": {
				CLICommands: []string{"rider"},
				AppPaths:    []string{"${ProgramFiles}/JetBrains/Rider/bin/rider64.exe"},
			},
		},
	},
	{
		ID:          "android-studio",
		DisplayName: "Android Studio",
		IconName:    "android-studio",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"studio"},
				AppPaths:    []string{"/Applications/Android Studio.app"},
			},
			"linux": {
				CLICommands: []string{"studio"},
				AppPaths:    []string{"/opt/android-studio/bin/studio.sh"},
			},
			"windows": {
				AppPaths: []string{"${ProgramFiles}/Android/Android Studio/bin/studio64.exe"},
			},
		},
	},
	{
		ID:          "sourcetree",
		DisplayName: "Sourcetree",
		IconName:    "sourcetree",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"stree"},
				AppPaths:    []string{"/Applications/Sourcetree.app"},
			},
			"windows": {
				CLICommands: []string{"stree"},
				AppPaths:    []string{"${LOCALAPPDATA}/SourceTree/SourceTree.exe"},
			},
		},
	},
	{
		ID:          "windsurf",
		DisplayName: "Windsurf",
		IconName:    "windsurf",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"windsurf"},
				AppPaths:    []string{"/Applications/Windsurf.app"},
			},
			"linux": {
				CLICommands: []string{"windsurf"},
			},
			"windows": {
				CLICommands: []string{"windsurf"},
			},
		},
	},
	{
		ID:          "antigravity",
		DisplayName: "Antigravity",
		IconName:    "antigravity",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"antigravity"},
				AppPaths:    []string{"/Applications/Antigravity.app"},
			},
			"linux": {
				CLICommands: []string{"antigravity"},
			},
			"windows": {
				CLICommands: []string{"antigravity"},
			},
		},
	},
	{
		ID:          "fork",
		DisplayName: "Fork",
		IconName:    "fork",
		Platforms: map[string]PlatformConfig{
			"darwin": {
				CLICommands: []string{"fork"},
				AppPaths:    []string{"/Applications/Fork.app"},
			},
			"windows": {
				AppPaths:     []string{"${LOCALAPPDATA}/Fork/Fork.exe"},
				LaunchMethod: "app",
			},
		},
	},
}

// DetectedEditors probes the current OS and returns editors that are installed.
func DetectedEditors() []DetectedEditor {
	return DetectedEditorsForOS(runtime.GOOS)
}

// DetectedEditorsForOS probes the given OS and returns editors that are installed.
// Exposed for cross-platform testing.
func DetectedEditorsForOS(goos string) []DetectedEditor {
	var result []DetectedEditor
	for _, e := range Registry {
		if d, ok := detectEditor(e, goos); ok {
			result = append(result, d)
		}
	}
	return result
}

// detectEditor checks if a single editor is installed on the given OS.
func detectEditor(e Editor, goos string) (DetectedEditor, bool) {
	pc, ok := e.Platforms[goos]
	if !ok {
		return DetectedEditor{}, false
	}

	// Try CLI commands first (preferred).
	for _, cmd := range pc.CLICommands {
		path, err := lookPathFn(cmd)
		if err == nil {
			method := "cli"
			if pc.LaunchMethod != "" {
				method = pc.LaunchMethod
			}
			return DetectedEditor{
				Editor:       e,
				ResolvedPath: path,
				Method:       method,
			}, true
		}
	}

	// Fall back to app paths.
	for _, appPath := range pc.AppPaths {
		expanded := os.ExpandEnv(appPath)
		_, err := statFn(expanded)
		if err == nil {
			method := "app"
			if pc.LaunchMethod != "" {
				method = pc.LaunchMethod
			}
			return DetectedEditor{
				Editor:       e,
				ResolvedPath: expanded,
				Method:       method,
			}, true
		}
	}

	return DetectedEditor{}, false
}
