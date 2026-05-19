package backends

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestCursorAndGeminiInvokeThroughConfiguredInvokers(t *testing.T) {
	oldCursorInvoker, oldCursorNon := cursorInvoker, cursorNonInteractiveInvoker
	oldGeminiInvoker, oldGeminiNon := geminiInvoker, geminiNonInteractiveInvoker
	t.Cleanup(func() {
		cursorInvoker, cursorNonInteractiveInvoker = oldCursorInvoker, oldCursorNon
		geminiInvoker, geminiNonInteractiveInvoker = oldGeminiInvoker, oldGeminiNon
	})

	cursorCalled := false
	cursorInvoker = func(workDir, prompt, agentName string) error {
		cursorCalled = workDir == "/repo" && prompt == "prompt" && agentName == "nova"
		return errors.New("cursor")
	}
	if err := (&CursorBackend{}).InvokeInteractive("/repo", "prompt", "nova"); err == nil || !cursorCalled {
		t.Fatalf("cursor interactive err=%v called=%v", err, cursorCalled)
	}
	cursorNonCalled := false
	cursorNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
		cursorNonCalled = workDir == "/repo" && prompt == "prompt" && agentName == "nova" && shutdown != nil && collector != nil
		return errors.New("cursor non")
	}
	if err := (&CursorBackend{}).InvokeNonInteractive("/repo", "prompt", "nova", make(chan struct{}), usage.NewCollector("cursor", "nova")); err == nil || !cursorNonCalled {
		t.Fatalf("cursor non-interactive err=%v called=%v", err, cursorNonCalled)
	}

	geminiCalled := false
	geminiInvoker = func(workDir, prompt, agentName string) error {
		geminiCalled = workDir == "/repo" && prompt == "prompt" && agentName == "nova"
		return errors.New("gemini")
	}
	if err := (&GeminiBackend{}).InvokeInteractive("/repo", "prompt", "nova"); err == nil || !geminiCalled {
		t.Fatalf("gemini interactive err=%v called=%v", err, geminiCalled)
	}
	geminiNonCalled := false
	geminiNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
		geminiNonCalled = workDir == "/repo" && prompt == "prompt" && agentName == "nova" && shutdown != nil && collector != nil
		return errors.New("gemini non")
	}
	if err := (&GeminiBackend{}).InvokeNonInteractive("/repo", "prompt", "nova", make(chan struct{}), usage.NewCollector("gemini", "nova")); err == nil || !geminiNonCalled {
		t.Fatalf("gemini non-interactive err=%v called=%v", err, geminiNonCalled)
	}
}

func TestCursorAndGeminiCommandConstruction(t *testing.T) {
	cursorCmd := buildCursorInteractiveCmd("/repo", "do work", "nova")
	if filepath.Base(cursorCmd.Path) != "cursor" || !reflect.DeepEqual(cursorCmd.Args, []string{"cursor", "--force", "do work"}) {
		t.Fatalf("cursor cmd path=%q args=%v", cursorCmd.Path, cursorCmd.Args)
	}
	if cursorCmd.Dir != "/repo" || !hasEnv(cursorCmd.Env, "LOOM_WORKTREE_PATH=/repo") || !hasEnv(cursorCmd.Env, "LOOM_AGENT_NAME=nova") {
		t.Fatalf("cursor cmd env/dir = dir:%q env:%v", cursorCmd.Dir, cursorCmd.Env)
	}

	geminiCmd := buildGeminiInteractiveCmd("/repo", "do work", "")
	if filepath.Base(geminiCmd.Path) != "gemini" || !reflect.DeepEqual(geminiCmd.Args, []string{"gemini", "--approval-mode=yolo", "do work"}) {
		t.Fatalf("gemini cmd path=%q args=%v", geminiCmd.Path, geminiCmd.Args)
	}
	if geminiCmd.Dir != "/repo" || !hasEnv(geminiCmd.Env, "LOOM_WORKTREE_PATH=/repo") || hasEnvPrefix(geminiCmd.Env, "LOOM_AGENT_NAME=") {
		t.Fatalf("gemini cmd env/dir = dir:%q env:%v", geminiCmd.Dir, geminiCmd.Env)
	}
}

func TestCursorAndGeminiHealthAndMeta(t *testing.T) {
	dir := t.TempDir()
	writeVersionBinary(t, dir, "cursor", "cursor 1.2.3")
	writeVersionBinary(t, dir, "gemini", "gemini 2.3.4")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CURSOR_API_KEY", "cursor-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	cursor := &CursorBackend{}
	if cursor.Name() != "cursor" {
		t.Fatalf("cursor Name = %q", cursor.Name())
	}
	cursorHealth := cursor.HealthCheck()
	if !cursorHealth.Healthy || !cursorHealth.Installed || !cursorHealth.APIKeySet || cursorHealth.Version != "cursor 1.2.3" || cursorHealth.Message != "ready" {
		t.Fatalf("cursor health = %#v", cursorHealth)
	}
	if meta := cursor.Meta(); meta.DisplayName != "Cursor" || meta.Version != "cursor 1.2.3" || meta.BinaryName != "cursor" {
		t.Fatalf("cursor meta = %#v", meta)
	}

	gemini := &GeminiBackend{}
	if gemini.Name() != "gemini" {
		t.Fatalf("gemini Name = %q", gemini.Name())
	}
	geminiHealth := gemini.HealthCheck()
	if !geminiHealth.Healthy || !geminiHealth.Installed || !geminiHealth.APIKeySet || geminiHealth.Version != "gemini 2.3.4" || geminiHealth.Message != "ready" {
		t.Fatalf("gemini health = %#v", geminiHealth)
	}
	if meta := gemini.Meta(); meta.DisplayName != "Gemini" || meta.Version != "gemini 2.3.4" || meta.BinaryName != "gemini" {
		t.Fatalf("gemini meta = %#v", meta)
	}
}

func TestCursorAndGeminiHealthReportsMissingConfig(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CURSOR_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	cursorHealth := (&CursorBackend{}).HealthCheck()
	if cursorHealth.Healthy || cursorHealth.Installed || cursorHealth.APIKeySet ||
		!strings.Contains(cursorHealth.Message, "cursor binary not found") ||
		!strings.Contains(cursorHealth.Message, "CURSOR_API_KEY not set") {
		t.Fatalf("cursor missing health = %#v", cursorHealth)
	}
	geminiHealth := (&GeminiBackend{}).HealthCheck()
	if geminiHealth.Healthy || geminiHealth.Installed || geminiHealth.APIKeySet ||
		!strings.Contains(geminiHealth.Message, "gemini binary not found") ||
		!strings.Contains(geminiHealth.Message, "GEMINI_API_KEY or GOOGLE_API_KEY not set") {
		t.Fatalf("gemini missing health = %#v", geminiHealth)
	}
}

func TestCursorAndGeminiUsageCollectors(t *testing.T) {
	cursorCollector := usage.NewCollector("cursor", "nova")
	collectCursorStreamUsage(`{"usage":{"input_tokens":7,"output_tokens":11}}`, cursorCollector)
	collectCursorStreamUsage(`{"type":"message"}`, cursorCollector)
	collectCursorStreamUsage(`{`, cursorCollector)
	if in, out, _, _ := cursorCollector.Totals(); in != 7 || out != 11 {
		t.Fatalf("cursor totals = %d/%d", in, out)
	}

	geminiCollector := usage.NewCollector("gemini", "nova")
	collectGeminiStreamUsage(`{"usage":{"input_tokens":3,"output_tokens":5}}`, geminiCollector)
	collectGeminiStreamUsage(`{"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":17}}`, geminiCollector)
	collectGeminiStreamUsage(`{"type":"message"}`, geminiCollector)
	collectGeminiStreamUsage(`{`, geminiCollector)
	if in, out, _, _ := geminiCollector.Totals(); in != 16 || out != 22 {
		t.Fatalf("gemini totals = %d/%d", in, out)
	}
}

func writeVersionBinary(t *testing.T, dir, name, version string) {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo '" + version + "'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
}

func hasEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func hasEnvPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
