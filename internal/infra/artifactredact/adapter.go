package redact

import (
	"bytes"
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

// Adapter exposes secret scrubbing as the narrow mechanical port consumed by
// Artifacts. Policy remains in the module; this adapter only transforms bytes.
type Adapter struct{}

var _ artifacts.RedactionMechanism = Adapter{}

func (Adapter) RedactEvidence(
	ctx context.Context,
	request artifacts.RedactionRequest,
) (artifacts.RedactionResult, error) {
	if err := ctx.Err(); err != nil {
		return artifacts.RedactionResult{}, err
	}
	source := append([]byte(nil), request.Content...)
	var (
		content []byte
		err     error
	)
	if request.Kind == artifacts.EvidenceTranscript {
		content, err = JSONLBytes(source)
	} else {
		content = Bytes(source)
	}
	if err != nil {
		return artifacts.RedactionResult{}, err
	}
	return artifacts.RedactionResult{
		Content: append([]byte(nil), content...),
		Changed: !bytes.Equal(source, content),
	}, nil
}
