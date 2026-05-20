// Ported from github.com/entireio/cli redact/redact_test.go (MIT).

package redact

import (
	"bytes"
	"strings"
	"testing"
)

// highEntropySecret has Shannon entropy > 4.5.
const highEntropySecret = "sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA"

func TestBytes_NoSecrets(t *testing.T) {
	input := []byte("hello world, this is normal text")
	result := Bytes(input)
	if string(result) != string(input) {
		t.Errorf("got %q, want unchanged", result)
	}
	if &result[0] != &input[0] {
		t.Error("want same underlying slice when no redaction needed")
	}
}

func TestBytes_WithSecret(t *testing.T) {
	input := []byte("my key is " + highEntropySecret + " ok")
	result := Bytes(input)
	expected := []byte("my key is REDACTED ok")
	if !bytes.Equal(result, expected) {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestJSONLBytes_NoSecrets(t *testing.T) {
	input := []byte(`{"type":"text","content":"hello"}`)
	result, err := JSONLBytes(input)
	if err != nil {
		t.Fatalf("JSONLBytes: %v", err)
	}
	if !bytes.Equal(result, input) {
		t.Errorf("got %q, want unchanged", result)
	}
}

func TestJSONLBytes_WithSecret(t *testing.T) {
	input := []byte(`{"type":"text","content":"api key: ` + highEntropySecret + `"}`)
	result, err := JSONLBytes(input)
	if err != nil {
		t.Fatalf("JSONLBytes: %v", err)
	}
	if !strings.Contains(string(result), "REDACTED") {
		t.Errorf("want REDACTED in %q", result)
	}
	if strings.Contains(string(result), highEntropySecret) {
		t.Errorf("secret leaked in %q", result)
	}
}

func TestJSONLContent_SkipsPathFields(t *testing.T) {
	// Secret-shaped file path should NOT be redacted because it's under a "file_path" key.
	pathyValue := "/tmp/" + highEntropySecret + ".txt"
	input := `{"tool_input":{"file_path":"` + pathyValue + `"}}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if !strings.Contains(got, pathyValue) {
		t.Errorf("file_path value was redacted; got %q", got)
	}
}

func TestJSONLContent_PreservesFormatting(t *testing.T) {
	input := `{"a":1,"b":"hello"}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if got != input {
		t.Errorf("formatting changed: got %q, want %q", got, input)
	}
}

func TestString_RedactsHighEntropy(t *testing.T) {
	got := String("prefix " + highEntropySecret + " suffix")
	if !strings.Contains(got, "REDACTED") {
		t.Errorf("want REDACTED; got %q", got)
	}
}

// Regression: shouldSkipJSONLField previously used HasSuffix("id"), which
// wrongly matched "kid", "credential_id", "paid", "avoid", etc. and let
// high-entropy secrets pass through unredacted. Now uses an explicit
// allowlist of known structural-ID field names.
func TestJSONLContent_RedactsSecretInKidField(t *testing.T) {
	input := `{"kid":"` + highEntropySecret + `"}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if strings.Contains(got, highEntropySecret) {
		t.Errorf("secret leaked under kid field: got %q", got)
	}
}

func TestJSONLContent_RedactsSecretInCredentialIDField(t *testing.T) {
	input := `{"credential_id":"` + highEntropySecret + `"}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if strings.Contains(got, highEntropySecret) {
		t.Errorf("secret leaked under credential_id field: got %q", got)
	}
}

// Positive control: session_id is a structural ID in the allowlist and must
// still pass through untouched.
func TestJSONLContent_PreservesSessionID(t *testing.T) {
	input := `{"session_id":"abc-123"}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if !strings.Contains(got, "abc-123") {
		t.Errorf("session_id value was incorrectly modified: got %q", got)
	}
}

func TestJSONLContent_NestedArraysInvalidJSONAndSkippedObjects(t *testing.T) {
	input := strings.Join([]string{
		`{"items":[{"text":"token ` + highEntropySecret + `"},{"type":"image","data":"` + highEntropySecret + `"}],"signature":"` + highEntropySecret + `"}`,
		`not-json ` + highEntropySecret,
		``,
	}, "\n")

	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if strings.Contains(got, `"text":"token `+highEntropySecret+`"`) {
		t.Fatalf("nested text secret leaked: %q", got)
	}
	if !strings.Contains(got, `"type":"image","data":"`+highEntropySecret+`"`) {
		t.Fatalf("image payload should be preserved: %q", got)
	}
	if !strings.Contains(got, `"signature":"`+highEntropySecret+`"`) {
		t.Fatalf("signature field should be preserved: %q", got)
	}
	if strings.Contains(got, `not-json `+highEntropySecret) {
		t.Fatalf("invalid JSON line was not entropy-redacted: %q", got)
	}
}

func TestStringMergesOverlappingSecretRegionsAndPreservesJSONEscapes(t *testing.T) {
	githubToken := "ghp_0123456789abcdefghijklmnopqrstuvwxyz"
	input := `{"msg":"first\n` + githubToken + ` and again ` + githubToken + `"}`
	got := String(input)
	if strings.Contains(got, githubToken) {
		t.Fatalf("token leaked: %q", got)
	}
	if strings.Contains(got, `\R`) {
		t.Fatalf("redaction broke JSON escape sequence: %q", got)
	}
	if strings.Count(got, RedactedPlaceholder) != 2 {
		t.Fatalf("redaction count = %d in %q, want 2", strings.Count(got, RedactedPlaceholder), got)
	}
}

func TestStringGitleaksAndEscapeBoundaryBranches(t *testing.T) {
	escaped := String(`{"files":"controller.go\nmodel.go\tview.go"}`)
	if strings.Contains(escaped, `\R`) {
		t.Fatalf("redaction consumed JSON escape continuation: %q", escaped)
	}
	if !strings.Contains(escaped, `\nmodel.go\tview.go`) {
		t.Fatalf("ordinary escaped file list changed unexpectedly: %q", escaped)
	}

	shortEscapedToken := String(`{"files":"controller.go\nABCDEF123"}`)
	if strings.Contains(shortEscapedToken, `\REDACTED`) {
		t.Fatalf("short escaped token was redacted across JSON escape: %q", shortEscapedToken)
	}

	awsAccessKey := `aws_access_key_id = "AKIALALEMEL33243OLIA"`
	got := String(awsAccessKey + " and again " + awsAccessKey)
	if strings.Contains(got, "AKIALALEMEL33243OLIA") || strings.Count(got, RedactedPlaceholder) < 2 {
		t.Fatalf("gitleaks-style secret was not fully redacted: %q", got)
	}
}

func TestJSONLContentDeduplicatesReplacementsAndSkipsStructuralFields(t *testing.T) {
	structuralID := "MSG_0123456789abcdef0123456789abcdef"
	input := `{"message_id":"` + structuralID + `","nested":["` + highEntropySecret + `","` + highEntropySecret + `"],"type":"text"}`
	got, err := JSONLContent(input)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if !strings.Contains(got, `"message_id":"`+structuralID+`"`) {
		t.Fatalf("structural message_id should be preserved: %q", got)
	}
	if strings.Count(got, RedactedPlaceholder) != 2 {
		t.Fatalf("redaction count = %d in %q, want duplicate string occurrences replaced", strings.Count(got, RedactedPlaceholder), got)
	}
}

func TestRedactSmallHelpers(t *testing.T) {
	if shannonEntropy("") != 0 {
		t.Fatal("empty entropy should be zero")
	}
	for _, key := range []string{"signature", "id", "uuid", "thread_id", "tool_use_id", "filepath", "cwd"} {
		if !shouldSkipJSONLField(key) {
			t.Fatalf("%s should be skipped", key)
		}
	}
	if shouldSkipJSONLField("hybrid") {
		t.Fatal("hybrid should not be treated as a structural id")
	}
	if !shouldSkipJSONLObject(map[string]any{"type": "base64"}) {
		t.Fatal("base64 block should be skipped")
	}
	if !shouldSkipJSONLObject(map[string]any{"type": "image/png"}) {
		t.Fatal("image block should be skipped")
	}
	if shouldSkipJSONLObject(map[string]any{"type": "text"}) {
		t.Fatal("text block should not be skipped")
	}
	encoded, err := jsonEncodeString(`<tag>&value`)
	if err != nil {
		t.Fatalf("jsonEncodeString: %v", err)
	}
	if encoded != `"<tag>&value"` {
		t.Fatalf("jsonEncodeString = %q", encoded)
	}
}
