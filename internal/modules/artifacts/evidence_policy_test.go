package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type redactorFunc func(context.Context, RedactionRequest) (RedactionResult, error)

func (fn redactorFunc) RedactEvidence(ctx context.Context, request RedactionRequest) (RedactionResult, error) {
	return fn(ctx, request)
}

func TestEvidencePolicyOwnsRedactionAndReservedMetadata(t *testing.T) {
	redactor := redactorFunc(func(_ context.Context, request RedactionRequest) (RedactionResult, error) {
		return RedactionResult{Content: bytes.ReplaceAll(request.Content, []byte("secret"), []byte("[REDACTED]")), Changed: true}, nil
	})
	policy, err := NewEvidencePolicy(redactor)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := policy.Prepare(t.Context(), "logs", "application/custom", []byte("token=secret"), map[string]string{
		"runner":                       "codex",
		MetadataEvidenceKind:           "forged",
		MetadataEvidenceCaptureStatus:  "failed",
		MetadataEvidenceTruncated:      "true",
		MetadataEvidenceTruncateReason: "forged",
		MetadataEvidenceOriginalBytes:  "0",
		MetadataEvidenceLimitBytes:     "0",
		"loom.evidence.failure_class":  "forged",
		"loom.evidence.future_field":   "forged",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Content) != "token=[REDACTED]" || prepared.RedactionStatus != "redacted" {
		t.Fatalf("prepared content/status = %q/%q", prepared.Content, prepared.RedactionStatus)
	}
	if prepared.MIMEType != "text/plain; charset=utf-8" || prepared.Metadata["runner"] != "codex" {
		t.Fatalf("prepared MIME/metadata = %q/%v", prepared.MIMEType, prepared.Metadata)
	}
	if prepared.Metadata[MetadataEvidenceKind] != string(EvidenceLog) ||
		prepared.Metadata[MetadataEvidenceCaptureStatus] != "finalized" ||
		prepared.Metadata[MetadataEvidenceTruncated] != "false" ||
		prepared.Metadata[MetadataEvidenceOriginalBytes] != "12" ||
		prepared.Metadata[MetadataEvidenceLimitBytes] != "66060288" {
		t.Fatalf("reserved metadata was not policy-owned: %v", prepared.Metadata)
	}
	if _, ok := prepared.Metadata[MetadataEvidenceTruncateReason]; ok {
		t.Fatalf("untruncated evidence retained forged reason: %v", prepared.Metadata)
	}
	if _, ok := prepared.Metadata["loom.evidence.failure_class"]; ok {
		t.Fatalf("finalized evidence retained forged failure class: %v", prepared.Metadata)
	}
	if _, ok := prepared.Metadata["loom.evidence.future_field"]; ok {
		t.Fatalf("finalized evidence retained future reserved field: %v", prepared.Metadata)
	}
}

func TestNormalizeFailOwnsCompleteReservedMetadataNamespace(t *testing.T) {
	command, err := normalizeFail(FailCommand{
		ArtifactID:   "artifact-1",
		FailureClass: EvidenceFailureCorrupt,
		Metadata: map[string]string{
			"runner":                      "codex",
			MetadataEvidenceKind:          "forged",
			MetadataEvidenceTruncated:     "true",
			"loom.evidence.failure_class": "forged",
			"loom.evidence.future_field":  "forged",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Metadata["runner"] != "codex" ||
		command.Metadata[MetadataEvidenceCaptureStatus] != "capture_failed" ||
		command.Metadata["loom.evidence.failure_class"] != EvidenceFailureCorrupt {
		t.Fatalf("normalized failure metadata = %v", command.Metadata)
	}
	for _, key := range []string{MetadataEvidenceKind, MetadataEvidenceTruncated, "loom.evidence.future_field"} {
		if _, ok := command.Metadata[key]; ok {
			t.Fatalf("normalized failure retained caller-owned reserved key %q: %v", key, command.Metadata)
		}
	}
}

func TestEvidencePolicyFailsClosedWhenRedactionFails(t *testing.T) {
	want := errors.New("redactor unavailable")
	policy, err := NewEvidencePolicy(redactorFunc(func(context.Context, RedactionRequest) (RedactionResult, error) {
		return RedactionResult{}, want
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.Prepare(t.Context(), "log", "", []byte("content"), nil)
	if !errors.Is(err, ErrCaptureFailed) || !errors.Is(err, want) {
		t.Fatalf("Prepare error = %v, want capture failure and adapter cause", err)
	}
}

func TestEvidencePolicyRejectsCorruptCanonicalTranscript(t *testing.T) {
	policy := testEvidencePolicyWithLimit(t, 1024)
	_, err := policy.Prepare(t.Context(), "transcript", "", []byte("{\"seq\":1,\"role\":\"assistant\"}\n"), nil)
	if !errors.Is(err, ErrEvidenceCorrupt) {
		t.Fatalf("Prepare error = %v, want ErrEvidenceCorrupt", err)
	}
}

func TestEvidencePolicyTruncatesTranscriptWithValidProvenance(t *testing.T) {
	policy := testEvidencePolicyWithLimit(t, 700)
	var source bytes.Buffer
	for index := range 12 {
		value, err := json.Marshal(Event{
			Seq: index + 10, Timestamp: time.Date(2026, 8, 4, 10, 0, index, 0, time.UTC),
			Role: RoleAssistant, Type: EventText,
			Text: strings.Repeat("x", 180),
		})
		if err != nil {
			t.Fatal(err)
		}
		source.Write(value)
		source.WriteByte('\n')
	}
	prepared, err := policy.Prepare(t.Context(), "transcript", "text/plain", source.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Truncated || len(prepared.Content) > policy.maxBytes ||
		prepared.Metadata[MetadataEvidenceTruncated] != "true" ||
		prepared.Metadata[MetadataEvidenceTruncateReason] != "canonical_output_limit" {
		t.Fatalf("truncation result = truncated=%t bytes=%d metadata=%v", prepared.Truncated, len(prepared.Content), prepared.Metadata)
	}
	lines := bytes.Split(bytes.TrimSuffix(prepared.Content, []byte{'\n'}), []byte{'\n'})
	if len(lines) < 2 {
		t.Fatalf("truncated transcript = %q, want evidence plus marker", prepared.Content)
	}
	for index, line := range lines {
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("line %d is not JSON: %v", index, err)
		}
		if err := ValidateCanonicalEvent(event); err != nil {
			t.Fatalf("line %d is not canonical: %v", index, err)
		}
		if event.Seq != index+1 {
			t.Fatalf("line %d sequence = %d", index, event.Seq)
		}
		if index == len(lines)-1 && (event.Role != RoleSystem || event.Type != EventSessionMeta || !strings.Contains(event.Text, "truncated")) {
			t.Fatalf("final event is not truncation provenance: %+v", event)
		}
		if index == len(lines)-1 && event.Text != transcriptTruncationNotice {
			t.Fatalf("truncation notice = %q, want %q", event.Text, transcriptTruncationNotice)
		}
	}
}

func TestEvidencePolicyDoesNotTruncateFinalEventThatExactlyFits(t *testing.T) {
	event := Event{
		Seq: 1, Timestamp: time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC),
		Role: RoleAssistant, Type: EventText, Text: "exact boundary",
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	policy := testEvidencePolicyWithLimit(t, len(encoded)+1)
	content, truncated, reason, err := policy.encodeTranscript([]Event{event}, false)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || reason != "" || !bytes.Equal(content, append(encoded, '\n')) {
		t.Fatalf("encoded = %q truncated=%t reason=%q", content, truncated, reason)
	}
}

func TestEvidencePolicyTruncatesGenericEvidenceDeterministically(t *testing.T) {
	policy := testEvidencePolicyWithLimit(t, 8)
	prepared, err := policy.Prepare(t.Context(), "report", "text/markdown", []byte("0123456789"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Content) != "01234567" || !prepared.Truncated ||
		prepared.Metadata[MetadataEvidenceTruncateReason] != "canonical_output_limit" {
		t.Fatalf("prepared = %+v content=%q", prepared, prepared.Content)
	}
}

func TestEvidencePolicyPreservesUTF8BoundaryWhenTruncatingText(t *testing.T) {
	policy := testEvidencePolicyWithLimit(t, 9)
	prepared, err := policy.Prepare(t.Context(), "log", "", []byte("12345678éx"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Content) != "12345678" || !utf8.Valid(prepared.Content) || !prepared.Truncated {
		t.Fatalf("prepared content = %q (valid=%t, truncated=%t)", prepared.Content, utf8.Valid(prepared.Content), prepared.Truncated)
	}
}

func TestEvidencePolicyRejectsInvalidUTF8Text(t *testing.T) {
	policy := testEvidencePolicyWithLimit(t, 32)
	_, err := policy.Prepare(t.Context(), "diff", "", []byte{'o', 'k', 0xff}, nil)
	if !errors.Is(err, ErrEvidenceCorrupt) {
		t.Fatalf("Prepare error = %v, want ErrEvidenceCorrupt", err)
	}
}

func testEvidencePolicyWithLimit(t *testing.T, limit int) *EvidencePolicy {
	t.Helper()
	policy, err := NewEvidencePolicy(testRedactor{})
	if err != nil {
		t.Fatal(err)
	}
	policy.maxBytes = limit
	policy.now = func() time.Time { return time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC) }
	return policy
}
