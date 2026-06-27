package redact

import (
	"os"
	"strings"
	"testing"
)

func TestEnabledTruthTable(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want bool
	}{
		{name: "unset defaults on", want: true},
		{name: "empty defaults on", env: "", set: true, want: true},
		{name: "on", env: "on", set: true, want: true},
		{name: "one", env: "1", set: true, want: true},
		{name: "off", env: "off", set: true, want: false},
		{name: "zero", env: "0", set: true, want: false},
		{name: "false", env: "false", set: true, want: false},
		{name: "no", env: "no", set: true, want: false},
		{name: "trimmed case insensitive", env: " FALSE ", set: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("LOOM_REDACT_TRANSCRIPTS", tt.env)
			} else {
				old, ok := os.LookupEnv("LOOM_REDACT_TRANSCRIPTS")
				if err := os.Unsetenv("LOOM_REDACT_TRANSCRIPTS"); err != nil {
					t.Fatalf("unset env: %v", err)
				}
				t.Cleanup(func() {
					if ok {
						_ = os.Setenv("LOOM_REDACT_TRANSCRIPTS", old)
					} else {
						_ = os.Unsetenv("LOOM_REDACT_TRANSCRIPTS")
					}
				})
			}
			if got := Enabled(); got != tt.want {
				t.Fatalf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactRuntimeMetadataTextAllowlist(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "")
	metadata := map[string]string{
		"response_text":         "model said " + highEntropySecret,
		"sandbox_id":            highEntropySecret,
		"patch_content_hash":    "sha256:" + strings.Repeat("a", 64),
		"github_pr_number":      "123",
		"artifact.content_hash": "sha256:" + strings.Repeat("b", 64),
	}

	RedactRuntimeMetadataText(metadata)

	if strings.Contains(metadata["response_text"], highEntropySecret) {
		t.Fatalf("response_text leaked secret: %q", metadata["response_text"])
	}
	if metadata["sandbox_id"] != highEntropySecret {
		t.Fatalf("sandbox_id was redacted: %q", metadata["sandbox_id"])
	}
	if metadata["patch_content_hash"] != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("patch_content_hash was modified: %q", metadata["patch_content_hash"])
	}
	if metadata["github_pr_number"] != "123" {
		t.Fatalf("github_pr_number was modified: %q", metadata["github_pr_number"])
	}
	if metadata["artifact.content_hash"] != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("artifact content hash was modified: %q", metadata["artifact.content_hash"])
	}
}

func TestRedactRuntimeMetadataTextDisabled(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	metadata := map[string]string{"response_text": "model said " + highEntropySecret}

	RedactRuntimeMetadataText(metadata)

	if !strings.Contains(metadata["response_text"], highEntropySecret) {
		t.Fatalf("disabled redaction modified response_text: %q", metadata["response_text"])
	}
}
