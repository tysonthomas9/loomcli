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

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
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
	truncated := false
	reason := ""
	if kind == EvidenceTranscript {
		preparedContent, truncated, reason, err = policy.canonicalTranscript(preparedContent)
		if err != nil {
			return PreparedEvidence{}, err
		}
	} else if len(preparedContent) > policy.maxBytes {
		preparedContent = append([]byte(nil), preparedContent[:policy.maxBytes]...)
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
	result := cloneMetadata(source)
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
	events := make([]transcript.Event, 0, 256)
	for {
		var event transcript.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, false, "", fmt.Errorf("decode canonical transcript: %w", errors.Join(ErrEvidenceCorrupt, err))
		}
		if err := transcript.ValidateCanonicalEvent(event); err != nil {
			return nil, false, "", fmt.Errorf("validate canonical transcript: %w", errors.Join(ErrEvidenceCorrupt, err))
		}
		events = append(events, event)
		if len(events) > transcript.MaxCanonicalEvents {
			break
		}
	}
	if len(events) == 0 {
		return nil, false, "", fmt.Errorf("canonical transcript has no events: %w", ErrEvidenceCorrupt)
	}
	return policy.encodeTranscript(events, len(content) > policy.maxBytes || len(events) > transcript.MaxCanonicalEvents)
}

func (policy *EvidencePolicy) encodeTranscript(
	events []transcript.Event,
	sourceExceeded bool,
) ([]byte, bool, string, error) {
	var output bytes.Buffer
	truncated := sourceExceeded
	reason := ""
	for index, event := range events {
		if index >= transcript.MaxCanonicalEvents-1 {
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
		marker := policy.transcriptTruncationMarker(index+2, "canonical_output_limit")
		if output.Len()+len(encoded)+len(marker) > policy.maxBytes {
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
	event := transcript.Event{
		Seq: seq, Timestamp: policy.now().UTC(), Role: transcript.RoleSystem,
		Type: transcript.EventSessionMeta,
		Text: "Transcript truncated by Loom (" + reason + ").",
	}
	encoded, _ := json.Marshal(event)
	return append(encoded, '\n')
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
