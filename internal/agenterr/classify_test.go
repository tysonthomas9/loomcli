package agenterr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			wantClass: Transient,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := classifyClaude(tt.log, tt.exitCode)
			if tt.wantClass == Unknown && tt.log == "" {
				if result != nil {
					t.Errorf("expected nil for empty log, got %v", result)
				}
				return
			}
			if tt.wantClass == Unknown && result == nil {
				return
			}
			if result == nil {
				t.Fatalf("expected class %s, got nil", tt.wantClass)
			}
			if result.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", result.Class, tt.wantClass)
			}
			if result.RetryAfter != tt.wantRetry {
				t.Errorf("retryAfter = %v, want %v", result.RetryAfter, tt.wantRetry)
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
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 rate_limit: too many requests",
			exitCode:  1,
			wantClass: RateLimited,
		},
		{
			name:      "tokens per minute",
			log:       "Error: tokens per min limit exceeded",
			exitCode:  1,
			wantClass: RateLimited,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := classifyCodex(tt.log, tt.exitCode)
			if tt.wantClass == Unknown && result == nil {
				return
			}
			if result == nil {
				t.Fatalf("expected class %s, got nil", tt.wantClass)
			}
			if result.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", result.Class, tt.wantClass)
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
	}{
		{
			name:      "rate limit",
			log:       "Error: 429 too many requests",
			exitCode:  1,
			wantClass: RateLimited,
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
			result := classifyOpenCode(tt.log, tt.exitCode)
			if tt.wantClass == Unknown && result == nil {
				return
			}
			if result == nil {
				t.Fatalf("expected class %s, got nil", tt.wantClass)
			}
			if result.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", result.Class, tt.wantClass)
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
			got := classifyByExitCode(tt.exitCode)
			if got != tt.wantClass {
				t.Errorf("classifyByExitCode(%d) = %s, want %s", tt.exitCode, got, tt.wantClass)
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
		if !aerr.IsFatal() {
			t.Error("expected IsFatal() == true for AuthFailure")
		}
	})

	t.Run("missing log file", func(t *testing.T) {
		t.Parallel()
		aerr := ClassifyFromLog("/nonexistent/log.txt", 1, "claude")
		if aerr.Class != Unknown {
			t.Errorf("class = %s, want Unknown", aerr.Class)
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

	t.Run("never returns nil", func(t *testing.T) {
		t.Parallel()
		aerr := ClassifyFromLog("", 0, "")
		if aerr == nil {
			t.Fatal("ClassifyFromLog must never return nil")
		}
	})
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	retryable := []ErrorClass{RateLimited, Timeout, Transient}
	notRetryable := []ErrorClass{AuthFailure, BillingError, ModelNotFound, ContextOverflow, NoWork, Unknown}

	for _, c := range retryable {
		if !c.IsRetryable() {
			t.Errorf("%s.IsRetryable() = false, want true", c)
		}
	}
	for _, c := range notRetryable {
		if c.IsRetryable() {
			t.Errorf("%s.IsRetryable() = true, want false", c)
		}
	}
}

func TestIsFatal(t *testing.T) {
	t.Parallel()

	fatal := []ErrorClass{AuthFailure, BillingError}
	notFatal := []ErrorClass{RateLimited, ModelNotFound, ContextOverflow, Timeout, Transient, NoWork, Unknown}

	for _, c := range fatal {
		if !c.IsFatal() {
			t.Errorf("%s.IsFatal() = false, want true", c)
		}
	}
	for _, c := range notFatal {
		if c.IsFatal() {
			t.Errorf("%s.IsFatal() = true, want false", c)
		}
	}
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

		got, err := readLogTail(path, 100, 64*1024)
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

		got, err := readLogTail(path, 10, 64*1024)
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
		got, err := readLogTail("/nonexistent", 100, 64*1024)
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

		got, err := readLogTail(path, 100, 64*1024)
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
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
		{ErrorClass(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.class.String(); got != tt.want {
				t.Errorf("ErrorClass(%d).String() = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}
