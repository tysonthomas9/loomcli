package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// UploadContentArtifact creates (or reuses), uploads, and finalizes a content
// artifact in one call — the create→UploadContent→Finalize sequence shared by the
// driver host bridge and Interaction transcript upload. On an
// already-existing, finalized, owner-matching artifact it returns that artifact
// instead of re-uploading, so a retried finalize is idempotent.
//
// `in` must be fully populated by the caller (WorkspaceKey, ArtifactID, OwnerType,
// OwnerID, Type, MIMEType, …); this helper owns only the three-step durability
// dance, not the artifact's identity.
func UploadContentArtifact(ctx context.Context, as ArtifactStore, in ArtifactCreate, content []byte) (*domain.Artifact, error) {
	if as == nil {
		return nil, fmt.Errorf("artifact store required for %s artifact: %w", in.Type, domain.ErrInvalid)
	}
	if _, err := as.Create(ctx, in); err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("create %s artifact: %w", in.Type, err)
		}
		existing, getErr := as.Get(ctx, in.WorkspaceKey, in.ArtifactID)
		if getErr != nil {
			return nil, fmt.Errorf("get existing %s artifact: %w", in.Type, getErr)
		}
		if !reusableContentArtifact(existing, in) {
			return nil, fmt.Errorf("create %s artifact: %w", in.Type, err)
		}
		if existing.DurableStatus == "finalized" {
			return existing, nil
		}
		// Reusable but not yet finalized — fall through to re-upload + finalize.
	}
	uploaded, err := as.UploadContent(ctx, in.WorkspaceKey, in.ArtifactID, ArtifactContentUpload{
		Body:     bytes.NewReader(content),
		MIMEType: in.MIMEType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload %s artifact: %w", in.Type, err)
	}
	finalized, err := as.Finalize(ctx, in.WorkspaceKey, in.ArtifactID, ArtifactFinalize{
		ContentHash: &uploaded.ContentHash,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize %s artifact: %w", in.Type, err)
	}
	return finalized, nil
}

// reusableContentArtifact reports whether an existing artifact (hit on
// ErrAlreadyExists) is the same logical artifact `in` describes, so a retry can
// reuse it rather than fail. Owner/workspace/type must match; an empty SessionID
// on either side is treated as compatible.
func reusableContentArtifact(existing *domain.Artifact, in ArtifactCreate) bool {
	if existing == nil {
		return false
	}
	if existing.WorkspaceKey != in.WorkspaceKey ||
		existing.OwnerType != in.OwnerType ||
		existing.OwnerID != in.OwnerID ||
		existing.Type != in.Type {
		return false
	}
	return in.SessionID == "" || existing.SessionID == "" || existing.SessionID == in.SessionID
}
