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
