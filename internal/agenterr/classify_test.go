package agenterr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

func TestClassifyClaude(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       string
		exitCode  int
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "rate limit with message",
			log:       "Error: 429 Too Many Requests: rate_limit_error",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "rate limit with retry-after",
			log:       "Error: rate limit exceeded\nretry-after: 30",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 30 * time.Second,
		},
		{
			name:      "auth failure invalid key",
			log:       "Error: 401 Unauthorized: invalid api key",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "auth failure env var",
			log:       "Error: ANTHROPIC_API_KEY is not set",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "billing error",
			log:       "Error: 402 Payment Required: billing error",
			exitCode:  1,
			wantClass: BillingError,
		},
		{
			name:      "model not found",
			log:       "Error: 404 model claude-99 does not exist",
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "model not found selected model wording",
			log:       `There's an issue with the selected model (definitely-not-a-real-model). It may not exist or you may not have access to it. Run --model to pick a different model.`,
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "generic access wording is not model not found",
			log:       `You may not have access to it in your current region.`,
			exitCode:  1,
			wantClass: Unknown,
		},
		{
			name:      "context overflow",
			log:       "Error: context_length_exceeded: max tokens 200000",
			exitCode:  1,
			wantClass: ContextOverflow,
		},
		{
			name:      "connection timeout",
			log:       "Error: ETIMEDOUT connecting to api.anthropic.com",
			exitCode:  1,
			wantClass: Timeout,
		},
		{
			name:      "overloaded error",
			log:       "Error: 529 overloaded_error: API is temporarily overloaded",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "overloaded error with retry-after",
			log:       "Error: 529 overloaded_error: API is temporarily overloaded\nretry-after: 45",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 45 * time.Second,
		},
		{
			name:      "server error 500",
			log:       "Error: 500 internal server error",
			exitCode:  1,
			wantClass: Transient,
		},
		{
			name:      "unknown error",
			log:       "Segmentation fault",
			exitCode:  1,
			wantClass: Unknown,
		},
		{
			name:      "empty log",
			log:       "",
			exitCode:  1,
			wantClass: Unknown,
		},
		// User-evidence strings from the 2026-05-21 dead-agents incident.
		// These previously fell through to Unknown and burned the entire
		// retry budget; the shared rate-limit patterns now catch them.
		{
			name:      "session limit prose with reset time",
			log:       "You've hit your session limit · resets 6:40pm (Europe/Warsaw)",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "usage limit prose with try again at",
			log:       "You've hit your usage limit. Upgrade to Pro or try again at May 21st, 2026 1:32 AM.",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "usage limit JSON envelope",
			log:       `{"type":"error","message":"You've hit your usage limit."}`,
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "generic try-again-at with time",
			log:       "Error: try again at 6:40pm",
			exitCode:  1,
			wantClass: RateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.log, tt.exitCode, "claude")
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifyCodex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       string
		exitCode  int
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 rate_limit: too many requests",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "rate limit with retry-after",
			log:       "Error: 429 rate_limit: too many requests\nretry-after: 45",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 45 * time.Second,
		},
		{
			name:      "rate limit with Retry-After header casing",
			log:       "Error: too many requests\nRetry-After: 120",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 120 * time.Second,
		},
		{
			name:      "tokens per minute",
			log:       "Error: tokens per min limit exceeded",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "tokens per minute with retry-after",
			log:       "Error: tokens per min limit exceeded\nretry-after: 60",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 60 * time.Second,
		},
		{
			name:      "auth failure",
			log:       "Error: 401 invalid api key",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "auth env var",
			log:       "Error: OPENAI_API_KEY is not set",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "billing error",
			log:       "Error: insufficient_quota: exceeded quota",
			exitCode:  1,
			wantClass: BillingError,
		},
		{
			name:      "model not found",
			log:       "Error: model gpt-99 not found",
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "context length",
			log:       "Error: context_length_exceeded: maximum context length is 128000",
			exitCode:  1,
			wantClass: ContextOverflow,
		},
		{
			name:      "timeout",
			log:       "Error: ETIMEDOUT connecting to api.openai.com",
			exitCode:  1,
			wantClass: Timeout,
		},
		{
			name:      "server error",
			log:       "Error: server_error: internal error",
			exitCode:  1,
			wantClass: Transient,
		},
		{
			name:      "unknown",
			log:       "something unexpected happened",
			exitCode:  1,
			wantClass: Unknown,
		},
		// User-evidence strings: codex emits the same prose envelopes
		// when a usage limit is hit, so the shared rate-limit patterns
		// must apply to codex too.
		{
			name:      "session limit prose with reset time",
			log:       "You've hit your session limit · resets 6:40pm (Europe/Warsaw)",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "usage limit JSON envelope",
			log:       `{"type":"error","message":"You've hit your usage limit."}`,
			exitCode:  1,
			wantClass: RateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.log, tt.exitCode, "codex")
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifyOpenCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       string
		exitCode  int
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 too many requests",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "rate limit with retry-after",
			log:       "Error: 429 too many requests\nretry-after: 30",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 30 * time.Second,
		},
		{
			name:      "rate limit with Retry-After header casing",
			log:       "Error: rate_limit exceeded\nRetry-After: 90",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 90 * time.Second,
		},
		{
			name:      "auth failure",
			log:       "Error: 401 unauthorized",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "billing",
			log:       "Error: 402 payment required, insufficient credits",
			exitCode:  1,
			wantClass: BillingError,
		},
		{
			name:      "model not found",
			log:       "Error: model_not_found: model does not exist",
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "context length",
			log:       "Error: context_length exceeded, token_limit reached",
			exitCode:  1,
			wantClass: ContextOverflow,
		},
		{
			name:      "timeout",
			log:       "Error: ETIMEDOUT",
			exitCode:  1,
			wantClass: Timeout,
		},
		{
			name:      "server error",
			log:       "Error: 500 internal_error",
			exitCode:  1,
			wantClass: Transient,
		},
		{
			name:      "unknown",
			log:       "bus error",
			exitCode:  1,
			wantClass: Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.log, tt.exitCode, "opencode")
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifyGemini(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       string
		exitCode  int
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 RESOURCE_EXHAUSTED: rate limit exceeded",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "rate limit with retry-after",
			log:       "Error: too many requests\nretry-after: 25",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 25 * time.Second,
		},
		{
			name:      "auth failure env var",
			log:       "Error: GEMINI_API_KEY is not set",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "billing error",
			log:       "Error: quota exceeded for billing project",
			exitCode:  1,
			wantClass: BillingError,
		},
		{
			name:      "model not found",
			log:       "Error: unsupported model gemini-99",
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "context length",
			log:       "Error: prompt too long, max tokens exceeded",
			exitCode:  1,
			wantClass: ContextOverflow,
		},
		{
			name:      "timeout",
			log:       "Error: deadline exceeded",
			exitCode:  1,
			wantClass: Timeout,
		},
		{
			name:      "server error",
			log:       "Error: backend error",
			exitCode:  1,
			wantClass: Transient,
		},
		{
			name:      "unknown",
			log:       "bus error",
			exitCode:  1,
			wantClass: Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.log, tt.exitCode, "gemini")
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifyCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       string
		exitCode  int
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 too many requests",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "rate limit with retry-after",
			log:       "Error: rate limit exceeded\nretry-after: 15",
			exitCode:  1,
			wantClass: RateLimited,
			wantRetry: 15 * time.Second,
		},
		{
			name:      "auth failure env var",
			log:       "Error: CURSOR_API_KEY is invalid",
			exitCode:  1,
			wantClass: AuthFailure,
		},
		{
			name:      "billing error",
			log:       "Error: insufficient credits",
			exitCode:  1,
			wantClass: BillingError,
		},
		{
			name:      "model not found",
			log:       "Error: invalid model selected",
			exitCode:  1,
			wantClass: ModelNotFound,
		},
		{
			name:      "context length",
			log:       "Error: token limit reached",
			exitCode:  1,
			wantClass: ContextOverflow,
		},
		{
			name:      "timeout",
			log:       "Error: connection timed out",
			exitCode:  1,
			wantClass: Timeout,
		},
		{
			name:      "server error",
			log:       "Error: service unavailable",
			exitCode:  1,
			wantClass: Transient,
		},
		{
			name:      "unknown",
			log:       "bus error",
			exitCode:  1,
			wantClass: Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.log, tt.exitCode, "cursor")
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
		})
	}
}

func TestClassifyByExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exitCode  int
		wantClass ErrorClass
	}{
		{"SIGKILL", 137, Timeout},
		{"SIGTERM", 143, Transient},
		{"generic failure", 1, Unknown},
		{"usage error", 2, Unknown},
		{"other", 42, Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := OutcomeFromHarness(classifyByExitCode(tt.exitCode))
			if got != tt.wantClass {
				t.Errorf("classifyByExitCode(%d) = %s, want %s", tt.exitCode, got, tt.wantClass)
			}
		})
	}
}

func TestClassifyByExitCodeMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exitCode int
		contains []string
	}{
		{"SIGKILL", 137, []string{"SIGKILL", "137"}},
		{"SIGTERM", 143, []string{"SIGTERM", "143"}},
		{"generic failure", 1, []string{"exit code 1"}},
		{"arbitrary code", 42, []string{"exit code 42"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyByExitCodeMessage(tt.exitCode)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("classifyByExitCodeMessage(%d) = %q, want to contain %q", tt.exitCode, got, s)
				}
			}
		})
	}
}

func TestClassifyFromLog(t *testing.T) {
	t.Parallel()

	t.Run("claude rate limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte("Starting agent...\nError: 429 Too Many Requests\nretry-after: 60\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 1, "claude")
		if aerr.Class != RateLimited {
			t.Errorf("class = %s, want RateLimited", aerr.Class)
		}
		if aerr.RetryAfter != 60*time.Second {
			t.Errorf("retryAfter = %v, want 60s", aerr.RetryAfter)
		}
		if aerr.Backend != "claude" {
			t.Errorf("backend = %q, want %q", aerr.Backend, "claude")
		}
		if aerr.ExitCode != 1 {
			t.Errorf("exitCode = %d, want 1", aerr.ExitCode)
		}
	})

	t.Run("codex auth failure", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte("Error: 401 incorrect api key\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 1, "codex")
		if aerr.Class != AuthFailure {
			t.Errorf("class = %s, want AuthFailure", aerr.Class)
		}
		// Fatality of AuthFailure is policy, asserted by the agentpolicy
		// golden table (Decide -> StopFatal).
	})

	t.Run("missing log file", func(t *testing.T) {
		t.Parallel()
		aerr := ClassifyFromLog("/nonexistent/log.txt", 1, "claude")
		if aerr.Class != Unknown {
			t.Errorf("class = %s, want Unknown", aerr.Class)
		}
	})

	t.Run("backend binary not on PATH marker classifies as BackendUnavailable", func(t *testing.T) {
		t.Parallel()
		// The marker is emitted by the loom-side wrapper translator
		// when the configured CLI is missing. It outranks per-backend
		// patterns because it's a cross-cutting wrapper signal —
		// without this branch, the supervisor would burn restart
		// budget on a binary that won't appear on its own (LOOM-4).
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		body := "Starting agent...\n" + BackendUnavailableMarker + `: wrapper: binary not found: exec: "codex": executable file not found in $PATH` + "\n"
		if err := os.WriteFile(logPath, []byte(body), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 1, "codex")
		if aerr.Class != BackendUnavailable {
			t.Errorf("class = %s, want BackendUnavailable", aerr.Class)
		}
		// Backend-specific rate-limit / billing patterns must NOT win
		// against the wrapper-level marker.
		body2 := BackendUnavailableMarker + ": rate limit and 429 sprinkled in for distraction"
		if err := os.WriteFile(logPath, []byte(body2), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}
		aerr = ClassifyFromLog(logPath, 1, "codex")
		if aerr.Class != BackendUnavailable {
			t.Errorf("class = %s, want BackendUnavailable (marker outranks backend patterns)", aerr.Class)
		}
	})

	t.Run("empty log file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte(""), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 137, "claude")
		if aerr.Class != Timeout {
			t.Errorf("class = %s, want Timeout (exit code 137 fallback)", aerr.Class)
		}
	})

	t.Run("unknown backend falls back to exit code", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte("some log output\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 143, "unknown-backend")
		if aerr.Class != Transient {
			t.Errorf("class = %s, want Transient (exit code 143 fallback)", aerr.Class)
		}
	})

	t.Run("codex rate limit with retry-after", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte("Starting codex agent...\nError: 429 rate_limit: too many requests\nretry-after: 45\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 1, "codex")
		if aerr.Class != RateLimited {
			t.Errorf("class = %s, want RateLimited", aerr.Class)
		}
		if aerr.RetryAfter != 45*time.Second {
			t.Errorf("retryAfter = %v, want 45s", aerr.RetryAfter)
		}
		if aerr.Backend != "codex" {
			t.Errorf("backend = %q, want %q", aerr.Backend, "codex")
		}
		if aerr.ExitCode != 1 {
			t.Errorf("exitCode = %d, want 1", aerr.ExitCode)
		}
	})

	t.Run("opencode rate limit with retry-after", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logPath := filepath.Join(dir, "agent.log")
		if err := os.WriteFile(logPath, []byte("Starting opencode agent...\nError: 429 too many requests\nretry-after: 30\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		aerr := ClassifyFromLog(logPath, 1, "opencode")
		if aerr.Class != RateLimited {
			t.Errorf("class = %s, want RateLimited", aerr.Class)
		}
		if aerr.RetryAfter != 30*time.Second {
			t.Errorf("retryAfter = %v, want 30s", aerr.RetryAfter)
		}
		if aerr.Backend != "opencode" {
			t.Errorf("backend = %q, want %q", aerr.Backend, "opencode")
		}
		if aerr.ExitCode != 1 {
			t.Errorf("exitCode = %d, want 1", aerr.ExitCode)
		}
	})

	t.Run("never returns nil", func(t *testing.T) {
		t.Parallel()
		aerr := ClassifyFromLog("", 0, "")
		if aerr == nil {
			t.Fatal("ClassifyFromLog must never return nil")
		}
	})
}

func TestErrorString(t *testing.T) {
	t.Parallel()

	aerr := &AgentError{
		Class:   RateLimited,
		Backend: "claude",
		Message: "rate limit exceeded",
	}
	got := aerr.Error()
	if !strings.Contains(got, "RateLimited") {
		t.Errorf("Error() = %q, want to contain 'RateLimited'", got)
	}
	if !strings.Contains(got, "claude") {
		t.Errorf("Error() = %q, want to contain 'claude'", got)
	}

	aerr.RetryAfter = 30 * time.Second
	got = aerr.Error()
	if !strings.Contains(got, "retry after") {
		t.Errorf("Error() = %q, want to contain 'retry after'", got)
	}
}

func TestReadLogTail(t *testing.T) {
	t.Parallel()

	t.Run("small file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		got, err := readLogTail(path, 100)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "line1") || !strings.Contains(got, "line3") {
			t.Errorf("expected all lines, got %q", got)
		}
	})

	t.Run("large file truncated to max lines", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		var lines []string
		for i := 0; i < 200; i++ {
			lines = append(lines, "line")
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		got, err := readLogTail(path, 10)
		if err != nil {
			t.Fatal(err)
		}
		gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		if len(gotLines) > 10 {
			t.Errorf("got %d lines, want at most 10", len(gotLines))
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		t.Parallel()
		got, err := readLogTail("/nonexistent", 100)
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.log")
		if err := os.WriteFile(path, []byte(""), 0600); err != nil {
			t.Fatalf("write log: %v", err)
		}

		got, err := readLogTail(path, 100)
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestClassifyFromOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		output    string
		exitCode  int
		backend   string
		wantClass ErrorClass
		wantRetry time.Duration
	}{
		{
			name:      "claude rate limit with message",
			output:    "Error: 429 Too Many Requests: rate_limit_error",
			exitCode:  1,
			backend:   "claude",
			wantClass: RateLimited,
		},
		{
			name:      "claude rate limit with retry-after",
			output:    "Error: rate limit exceeded\nretry-after: 30",
			exitCode:  1,
			backend:   "claude",
			wantClass: RateLimited,
			wantRetry: 30 * time.Second,
		},
		{
			name:      "claude auth failure",
			output:    "Error: 401 Unauthorized: invalid api key",
			exitCode:  1,
			backend:   "claude",
			wantClass: AuthFailure,
		},
		{
			name:      "claude billing error",
			output:    "Error: 402 Payment Required: billing error",
			exitCode:  1,
			backend:   "claude",
			wantClass: BillingError,
		},
		{
			name:      "claude model not found",
			output:    "Error: 404 model claude-99 does not exist",
			exitCode:  1,
			backend:   "claude",
			wantClass: ModelNotFound,
		},
		{
			name:      "claude selected model wording",
			output:    `{"type":"assistant","message":{"content":[{"type":"text","text":"There's an issue with the selected model (definitely-not-a-real-model). It may not exist or you may not have access to it. Run --model to pick a different model."}]},"error":"invalid_request"}`,
			exitCode:  1,
			backend:   "claude",
			wantClass: ModelNotFound,
		},
		{
			name:      "claude context overflow",
			output:    "Error: context_length_exceeded: max tokens 200000",
			exitCode:  1,
			backend:   "claude",
			wantClass: ContextOverflow,
		},
		{
			name:      "claude server error 500",
			output:    "Error: 500 internal server error",
			exitCode:  1,
			backend:   "claude",
			wantClass: Transient,
		},
		{
			name:      "claude overloaded with retry-after",
			output:    "Error: 529 overloaded_error: API is temporarily overloaded\nretry-after: 45",
			exitCode:  1,
			backend:   "claude",
			wantClass: RateLimited,
			wantRetry: 45 * time.Second,
		},
		{
			name:      "codex rate limit",
			output:    "Error: 429 rate_limit: too many requests",
			exitCode:  1,
			backend:   "codex",
			wantClass: RateLimited,
		},
		{
			name:      "opencode auth failure",
			output:    "Error: 401 unauthorized",
			exitCode:  1,
			backend:   "opencode",
			wantClass: AuthFailure,
		},
		{
			name:      "gemini rate limit",
			output:    "Error: 429 RESOURCE_EXHAUSTED: rate limit exceeded",
			exitCode:  1,
			backend:   "gemini",
			wantClass: RateLimited,
		},
		{
			name:      "cursor auth failure",
			output:    "Error: CURSOR_API_KEY is invalid",
			exitCode:  1,
			backend:   "cursor",
			wantClass: AuthFailure,
		},
		{
			name:      "unknown output falls back to exit code",
			output:    "Segmentation fault",
			exitCode:  137,
			backend:   "claude",
			wantClass: Timeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(tt.output, tt.exitCode, tt.backend)
			if aerr == nil {
				t.Fatal("ClassifyFromOutput must never return nil")
			}
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
			if aerr.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", aerr.RetryAfter, tt.wantRetry)
			}
			if aerr.Backend != tt.backend {
				t.Errorf("backend = %q, want %q", aerr.Backend, tt.backend)
			}
			if aerr.ExitCode != tt.exitCode {
				t.Errorf("exitCode = %d, want %d", aerr.ExitCode, tt.exitCode)
			}
		})
	}
}

func TestClassifyFromOutput_EmptyOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exitCode  int
		wantClass ErrorClass
	}{
		{"exit code 1 with empty output", 1, Unknown},
		{"exit code 137 with empty output", 137, Timeout},
		{"exit code 143 with empty output", 143, Transient},
		{"exit code 0 with empty output", 0, Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput("", tt.exitCode, "claude")
			if aerr == nil {
				t.Fatal("ClassifyFromOutput must never return nil")
			}
			if aerr.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", aerr.Class, tt.wantClass)
			}
		})
	}
}

func TestClassifyFromOutput_NeverReturnsNil(t *testing.T) {
	t.Parallel()

	// Verify ClassifyFromOutput never returns nil across a range of inputs.
	cases := []struct {
		output   string
		exitCode int
		backend  string
	}{
		{"", 0, ""},
		{"", 1, "claude"},
		{"random text", 42, "codex"},
		{"Error: something", 1, "opencode"},
		{"", 137, "unknown-backend"},
	}

	for _, c := range cases {
		aerr := ClassifyFromOutput(c.output, c.exitCode, c.backend)
		if aerr == nil {
			t.Errorf("ClassifyFromOutput(%q, %d, %q) returned nil", c.output, c.exitCode, c.backend)
		}
	}
}

func TestErrorClassString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class ErrorClass
		want  string
	}{
		{RateLimited, "RateLimited"},
		{AuthFailure, "AuthFailure"},
		{BillingError, "BillingError"},
		{ModelNotFound, "ModelNotFound"},
		{ContextOverflow, "ContextOverflow"},
		{Timeout, "Timeout"},
		{Transient, "Transient"},
		{NoWork, "NoWork"},
		{Unknown, "Unknown"},
		{Outcome{Harness: wrapper.ErrorClass(99)}, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.class.String(); got != tt.want {
				t.Errorf("Outcome(%v).String() = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

func TestClassifyFromOutput_IncompatibleBackendCLIIsTerminalModelFailure(t *testing.T) {
	message := `The 'gpt-5.6-sol' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again.`
	got := ClassifyFromOutput(message, 1, "codex")
	if got.Class != ModelNotFound {
		t.Fatalf("class = %s, want ModelNotFound", got.Class)
	}
	if got.Message != "backend CLI is incompatible with the selected model" {
		t.Fatalf("message = %q", got.Message)
	}
}

// writeRunLog builds an append-only daemon log out of consecutive run blocks
// and returns its path plus the byte offset at which each block starts.
func writeRunLog(t *testing.T, blocks ...string) (string, []int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "worker-agent.log")
	var (
		buf     strings.Builder
		offsets []int64
	)
	for _, b := range blocks {
		offsets = append(offsets, int64(buf.Len()))
		buf.WriteString(b)
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path, offsets
}

// TestClassifyFromLogAt_SkipsPriorRunAuthBanner is the misclassification this
// exists to stop: the per-role log is append-only and spans days, so a logged-out
// run from yesterday sits in the same file as today's timeout. Read whole, the
// stale banner wins and the agent is walled for an auth problem it does not have.
func TestClassifyFromLogAt_SkipsPriorRunAuthBanner(t *testing.T) {
	t.Parallel()

	priorRun := "[loom] starting task PUPPET-1\n" +
		AuthRequiredMarker + ": Not logged in · Run /login\n" +
		"Not logged in · Run /login\n"
	thisRun := "[loom] starting task PUPPET-2\n" +
		"error: context deadline exceeded\n"

	path, offsets := writeRunLog(t, priorRun, thisRun)

	// Control: the whole-file read is what produced the false verdict.
	if got := ClassifyFromLog(path, 1, "claude"); got.Class != AuthFailure {
		t.Fatalf("whole-file class = %s, want AuthFailure (control for the bug)", got.Class)
	}

	got := ClassifyFromLogAt(path, offsets[1], 1, "claude")
	if got.Class != Timeout {
		t.Fatalf("class = %s, want Timeout", got.Class)
	}
	if strings.Contains(got.RawOutput, "Not logged in") {
		t.Errorf("raw output still carries the prior run's banner:\n%s", got.RawOutput)
	}
}

// TestClassifyFromLogAt_OffsetPastEOFFallsBack covers rotation: the recorded
// offset points past the end of a log that has since been replaced, so the run's
// own bytes are gone. Classifying nothing would hide a real failure, so the
// whole file is read instead.
func TestClassifyFromLogAt_OffsetPastEOFFallsBack(t *testing.T) {
	t.Parallel()

	path, _ := writeRunLog(t, "error: context deadline exceeded\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	got := ClassifyFromLogAt(path, info.Size()+4096, 1, "claude")
	want := ClassifyFromLog(path, 1, "claude")
	if got.Class != want.Class || got.RawOutput != want.RawOutput {
		t.Fatalf("offset past EOF: class/raw = %s/%q, want %s/%q",
			got.Class, got.RawOutput, want.Class, want.RawOutput)
	}
	if got.Class != Timeout {
		t.Fatalf("class = %s, want Timeout", got.Class)
	}
}

// TestClassifyFromLogAt_ZeroOffsetMatchesClassifyFromLog is the compatibility
// invariant: offset 0 is the value every pre-existing caller and test produces,
// and it must read byte-for-byte what it always did — including past the
// 100-line and 64KiB tail caps.
func TestClassifyFromLogAt_ZeroOffsetMatchesClassifyFromLog(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":       "",
		"single line": "error: context deadline exceeded\n",
		"over 100 lines": strings.Repeat("chatter\n", 500) +
			"error: context deadline exceeded\n",
		"over 64KiB": strings.Repeat("x", 80*1024) +
			"\nerror: context deadline exceeded\n",
		"no trailing newline": "Invalid API key · Please run /login",
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeRunLog(t, body)

			gotTail, gotErr := readLogTailAt(path, 100, 0)
			wantTail, wantErr := readLogTail(path, 100)
			if gotTail != wantTail {
				t.Fatalf("tail differs at offset 0:\ngot  %q\nwant %q", gotTail, wantTail)
			}
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("err differs: got %v, want %v", gotErr, wantErr)
			}

			got := ClassifyFromLogAt(path, 0, 1, "claude")
			want := ClassifyFromLog(path, 1, "claude")
			if got.Class != want.Class || got.RawOutput != want.RawOutput || got.Message != want.Message {
				t.Fatalf("classification differs at offset 0:\ngot  %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestReadLogTailAt_ReadsOnlyFromOffset checks the read window directly, so a
// future change to the tail caps cannot quietly start including pre-offset bytes.
func TestReadLogTailAt_ReadsOnlyFromOffset(t *testing.T) {
	t.Parallel()

	path, offsets := writeRunLog(t, "first run\n", "second run\n")

	tail, err := readLogTailAt(path, 100, offsets[1])
	if err != nil {
		t.Fatalf("readLogTailAt: %v", err)
	}
	if strings.Contains(tail, "first run") {
		t.Errorf("tail includes pre-offset bytes: %q", tail)
	}
	if !strings.Contains(tail, "second run") {
		t.Errorf("tail is missing the run's own bytes: %q", tail)
	}

	// An offset exactly at EOF has nothing of its own to report.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if tail, err := readLogTailAt(path, 100, info.Size()); err != nil || tail != "" {
		t.Errorf("readLogTailAt at EOF = %q, %v; want empty and no error", tail, err)
	}
}
