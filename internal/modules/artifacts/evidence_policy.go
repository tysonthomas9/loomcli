package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxEvidenceCaptureBytes is the Artifacts-owned durable evidence ceiling.
	// FleetDB accepts 64 MiB; the one MiB reserve guarantees room for canonical
	// truncation provenance and transport framing.
	MaxEvidenceCaptureBytes = 63 << 20
	maxEvidenceSourceBytes  = 64 << 20

	MetadataEvidenceKind           = "loom.evidence.kind"
	MetadataEvidenceCaptureStatus  = "loom.evidence.capture_status"
	MetadataEvidenceTruncated      = "loom.evidence.truncated"
	MetadataEvidenceOriginalBytes  = "loom.evidence.original_size_bytes"
	MetadataEvidenceLimitBytes     = "loom.evidence.capture_limit_bytes"
	MetadataEvidenceTruncateReason = "loom.evidence.truncation_reason"
	ownerEvidenceMetadataPrefix    = "loom.evidence."
	transcriptTruncationNotice     = "Transcript truncated by Loom because source history or canonical output exceeded Loom's bounded capture limits."
)

type EvidenceKind string

const (
	EvidencePrompt     EvidenceKind = "prompt"
	EvidenceTranscript EvidenceKind = "transcript"
	EvidenceDiff       EvidenceKind = "diff"
	EvidenceLog        EvidenceKind = "log"
	EvidenceReport     EvidenceKind = "report"
	EvidenceScrollback EvidenceKind = "scrollback"
)

type RedactionRequest struct {
	Kind     EvidenceKind
	MIMEType string
	Content  []byte
}

type RedactionResult struct {
	Content []byte
	Changed bool
}

// RedactionMechanism is the private mechanical adapter behind Artifacts'
// evidence policy. It may detect and replace secrets, but it cannot choose
// evidence ownership, durability, limits, or visibility.
type RedactionMechanism interface {
	RedactEvidence(context.Context, RedactionRequest) (RedactionResult, error)
}

type PreparedEvidence struct {
	Kind            EvidenceKind
	Content         []byte
	MIMEType        string
	ContentHash     string
	RedactionStatus string
	Metadata        map[string]string
	Truncated       bool
}

// OwnerEvidenceCaptureStatusKey and OwnerEvidenceFailureClassKey expose the
// Artifacts-owned vocabulary used when the lifecycle owner must durably record
// a capture failure that occurred before an Artifact row could be committed.
// Run Capture reads these keys only from an already-authorized owner snapshot.
func OwnerEvidenceCaptureStatusKey(kind EvidenceKind) string {
	return ownerEvidenceMetadataPrefix + string(kind) + ".capture_status"
}

func OwnerEvidenceFailureClassKey(kind EvidenceKind) string {
	return ownerEvidenceMetadataPrefix + string(kind) + ".failure_class"
}

func OwnerEvidenceAttemptKey(kind EvidenceKind) string {
	return ownerEvidenceMetadataPrefix + string(kind) + ".attempt"
}

func OwnerEvidenceFinalized(kind EvidenceKind, attempt int) map[string]string {
	result := map[string]string{
		OwnerEvidenceCaptureStatusKey(kind): "finalized",
		OwnerEvidenceFailureClassKey(kind):  "",
	}
	if attempt > 0 {
		result[OwnerEvidenceAttemptKey(kind)] = strconv.Itoa(attempt)
	}
	return result
}

// WithoutOwnerEvidenceMetadata prevents a backend or workflow runner from
// minting Artifacts-owned evidence state through arbitrary runtime metadata.
func WithoutOwnerEvidenceMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		if strings.HasPrefix(strings.TrimSpace(key), ownerEvidenceMetadataPrefix) {
			continue
		}
		result[key] = value
	}
	return result
}

// OwnerEvidenceCaptureFailure returns a sanitized, durable owner-metadata
// projection. Raw adapter errors are deliberately excluded so credentials or
// backend details cannot leak into TaskRun or AgentSession records.
func OwnerEvidenceCaptureFailure(kind EvidenceKind, cause error, attempt ...int) map[string]string {
	failure := evidenceFailureCommand("owner-projection", cause)
	result := map[string]string{
		OwnerEvidenceCaptureStatusKey(kind): "capture_failed",
		OwnerEvidenceFailureClassKey(kind):  failure.FailureClass,
	}
	if len(attempt) > 0 && attempt[0] > 0 {
		result[OwnerEvidenceAttemptKey(kind)] = strconv.Itoa(attempt[0])
	}
	return result
}

// EvidencePolicy owns canonicalization, redaction requirements, size bounds,
// truncation provenance, and the durable metadata vocabulary for evidence.
type EvidencePolicy struct {
	redactor RedactionMechanism
	maxBytes int
	now      func() time.Time
}

func NewEvidencePolicy(redactor RedactionMechanism) (*EvidencePolicy, error) {
	if redactor == nil {
		return nil, fmt.Errorf("compose Artifacts evidence policy: redaction mechanism is required: %w", ErrUnavailable)
	}
	return &EvidencePolicy{redactor: redactor, maxBytes: MaxEvidenceCaptureBytes, now: time.Now}, nil
}

//nolint:funlen // Policy keeps redaction, canonicalization, bounds, and provenance atomic.
func (policy *EvidencePolicy) Prepare(
	ctx context.Context,
	artifactType, mimeType string,
	content []byte,
	metadata map[string]string,
) (PreparedEvidence, error) {
	if policy == nil || policy.redactor == nil || policy.maxBytes <= 0 || policy.now == nil {
		return PreparedEvidence{}, ErrUnavailable
	}
	kind, err := EvidenceKindForArtifactType(artifactType)
	if err != nil {
		return PreparedEvidence{}, err
	}
	if len(content) == 0 {
		return PreparedEvidence{}, fmt.Errorf("%s evidence content is empty: %w", kind, ErrInvalid)
	}
	if len(content) > maxEvidenceSourceBytes {
		return PreparedEvidence{}, fmt.Errorf("%s evidence source exceeds %d bytes: %w", kind, maxEvidenceSourceBytes, ErrInvalid)
	}
	mimeType = canonicalEvidenceMIMEType(kind, mimeType)
	redacted, err := policy.redactor.RedactEvidence(ctx, RedactionRequest{
		Kind: kind, MIMEType: mimeType, Content: append([]byte(nil), content...),
	})
	if err != nil {
		return PreparedEvidence{}, fmt.Errorf("redact %s evidence: %w", kind, errors.Join(ErrCaptureFailed, err))
	}
	if len(redacted.Content) == 0 || len(redacted.Content) > maxEvidenceSourceBytes {
		return PreparedEvidence{}, fmt.Errorf("redactor returned invalid %s evidence size: %w", kind, ErrInvalidPersistedState)
	}
	preparedContent := append([]byte(nil), redacted.Content...)
	if (kind == EvidenceTranscript || strings.HasPrefix(mimeType, "text/")) && !utf8.Valid(preparedContent) {
		return PreparedEvidence{}, fmt.Errorf("%s evidence is not valid UTF-8: %w", kind, ErrEvidenceCorrupt)
	}
	truncated := false
	reason := ""
	if kind == EvidenceTranscript {
		preparedContent, truncated, reason, err = policy.canonicalTranscript(preparedContent)
		if err != nil {
			return PreparedEvidence{}, err
		}
	} else if len(preparedContent) > policy.maxBytes {
		preparedContent = truncateEvidenceContent(preparedContent, mimeType, policy.maxBytes)
		truncated = true
		reason = "canonical_output_limit"
	}
	resultMetadata := evidenceMetadata(metadata, kind, len(content), policy.maxBytes, truncated, reason)
	redactionStatus := "checked"
	if redacted.Changed {
		redactionStatus = "redacted"
	}
	return PreparedEvidence{
		Kind: kind, Content: preparedContent, MIMEType: mimeType,
		ContentHash:     artifactContentHash(preparedContent),
		RedactionStatus: redactionStatus, Metadata: resultMetadata,
		Truncated: truncated,
	}, nil
}

// EvidenceKindForArtifactType resolves Artifacts' canonical durable evidence
// vocabulary for read projections as well as writers.
func EvidenceKindForArtifactType(value string) (EvidenceKind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "prompt":
		return EvidencePrompt, nil
	case "transcript":
		return EvidenceTranscript, nil
	case "diff", "patch":
		return EvidenceDiff, nil
	case "log", "logs":
		return EvidenceLog, nil
	case "report":
		return EvidenceReport, nil
	case "scrollback":
		return EvidenceScrollback, nil
	default:
		return "", fmt.Errorf("artifact type %q is not durable evidence: %w", value, ErrInvalid)
	}
}

func canonicalEvidenceMIMEType(kind EvidenceKind, supplied string) string {
	switch kind {
	case EvidenceTranscript:
		return "application/x-ndjson"
	case EvidenceDiff:
		return "text/x-diff; charset=utf-8"
	case EvidencePrompt, EvidenceLog, EvidenceScrollback:
		return "text/plain; charset=utf-8"
	case EvidenceReport:
		if value := strings.TrimSpace(supplied); value != "" {
			return value
		}
		return "application/octet-stream"
	default:
		return strings.TrimSpace(supplied)
	}
}

func evidenceMetadata(
	source map[string]string,
	kind EvidenceKind,
	originalBytes, limitBytes int,
	truncated bool,
	reason string,
) map[string]string {
	// Every key in the loom.evidence namespace is policy-owned. Strip the
	// caller's complete namespace before projecting the canonical values so a
	// runner cannot pre-mint a current or future evidence state field.
	result := WithoutOwnerEvidenceMetadata(source)
	if result == nil {
		result = map[string]string{}
	}
	result[MetadataEvidenceKind] = string(kind)
	result[MetadataEvidenceCaptureStatus] = "finalized"
	result[MetadataEvidenceTruncated] = strconv.FormatBool(truncated)
	result[MetadataEvidenceOriginalBytes] = strconv.Itoa(originalBytes)
	result[MetadataEvidenceLimitBytes] = strconv.Itoa(limitBytes)
	if truncated {
		result[MetadataEvidenceTruncateReason] = reason
	} else {
		delete(result, MetadataEvidenceTruncateReason)
	}
	return result
}

func (policy *EvidencePolicy) canonicalTranscript(content []byte) ([]byte, bool, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	events := make([]Event, 0, 256)
	for {
		var event Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, false, "", fmt.Errorf("decode canonical transcript: %w", errors.Join(ErrEvidenceCorrupt, err))
		}
		if err := ValidateCanonicalEvent(event); err != nil {
			return nil, false, "", fmt.Errorf("validate canonical transcript: %w", errors.Join(ErrEvidenceCorrupt, err))
		}
		events = append(events, event)
		if len(events) > MaxCanonicalEvents {
			break
		}
	}
	if len(events) == 0 {
		return nil, false, "", fmt.Errorf("canonical transcript has no events: %w", ErrEvidenceCorrupt)
	}
	return policy.encodeTranscript(events, len(content) > policy.maxBytes || len(events) > MaxCanonicalEvents)
}

func (policy *EvidencePolicy) encodeTranscript(
	events []Event,
	sourceExceeded bool,
) ([]byte, bool, string, error) {
	var output bytes.Buffer
	truncated := sourceExceeded
	reason := ""
	for index, event := range events {
		if index >= MaxCanonicalEvents-1 {
			truncated = true
			reason = "canonical_event_limit"
			break
		}
		event.Seq = index + 1
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, false, "", fmt.Errorf("encode canonical transcript: %w", errors.Join(ErrEvidenceCorrupt, err))
		}
		encoded = append(encoded, '\n')
		needed := output.Len() + len(encoded)
		if sourceExceeded || index < len(events)-1 {
			needed += len(policy.transcriptTruncationMarker(index+2, "canonical_output_limit"))
		}
		if needed > policy.maxBytes {
			truncated = true
			reason = "canonical_output_limit"
			break
		}
		output.Write(encoded)
	}
	if sourceExceeded && reason == "" {
		reason = "canonical_output_limit"
	}
	if truncated {
		marker := policy.transcriptTruncationMarker(outputLineCount(output.Bytes())+1, reason)
		for output.Len()+len(marker) > policy.maxBytes && output.Len() > 0 {
			removeLastJSONLLine(&output)
			marker = policy.transcriptTruncationMarker(outputLineCount(output.Bytes())+1, reason)
		}
		if len(marker) > policy.maxBytes {
			return nil, false, "", fmt.Errorf("transcript limit cannot contain truncation marker: %w", ErrInvalid)
		}
		output.Write(marker)
	}
	return append([]byte(nil), output.Bytes()...), truncated, reason, nil
}

func (policy *EvidencePolicy) transcriptTruncationMarker(seq int, reason string) []byte {
	_ = reason // The machine-readable reason is recorded in reserved metadata.
	event := Event{
		Seq: seq, Timestamp: policy.now().UTC(), Role: RoleSystem,
		Type: EventSessionMeta,
		Text: transcriptTruncationNotice,
	}
	encoded, _ := json.Marshal(event)
	return append(encoded, '\n')
}

func truncateEvidenceContent(content []byte, mimeType string, limit int) []byte {
	if limit <= 0 || len(content) <= limit {
		return append([]byte(nil), content...)
	}
	end := limit
	if strings.HasPrefix(mimeType, "text/") {
		for end > 0 && !utf8.RuneStart(content[end]) {
			end--
		}
	}
	return append([]byte(nil), content[:end]...)
}

func outputLineCount(value []byte) int {
	return bytes.Count(value, []byte{'\n'})
}

func removeLastJSONLLine(output *bytes.Buffer) {
	value := output.Bytes()
	if len(value) == 0 {
		return
	}
	value = bytes.TrimSuffix(value, []byte{'\n'})
	if index := bytes.LastIndexByte(value, '\n'); index >= 0 {
		value = value[:index+1]
	} else {
		value = nil
	}
	output.Reset()
	output.Write(value)
}
