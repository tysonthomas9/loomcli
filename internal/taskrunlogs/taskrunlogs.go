// Package taskrunlogs stores immutable task-run and driver-run logs as
// content artifacts and resolves the persisted artifact references.
package taskrunlogs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	maxBytes = 1 << 20
	logMIME  = "text/plain; charset=utf-8"
)

// Log is persisted log content plus the artifact metadata needed by clients.
type Log struct {
	Content    string
	ModifiedAt time.Time
	Truncated  bool
}

// PutTask uploads task-run log bytes as a new immutable content artifact.
func PutTask(ctx context.Context, st store.Store, workspaceKey, taskRunID, content string) (string, error) {
	return put(ctx, st, workspaceKey, "task", taskRunID, content)
}

// PutRun uploads driver-run log bytes as a new immutable content artifact.
func PutRun(ctx context.Context, st store.Store, workspaceKey, runID, content string) (string, error) {
	return put(ctx, st, workspaceKey, "run", runID, content)
}

func put(ctx context.Context, st store.Store, workspaceKey, kind, ownerID, content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	if st == nil {
		return "", fmt.Errorf("store required for %s log: %w", kind, domain.ErrInvalid)
	}

	originalBytes := len(content)
	truncated := originalBytes > maxBytes
	if truncated {
		content = content[originalBytes-maxBytes:]
	}
	suffix, err := randomSuffix()
	if err != nil {
		return "", fmt.Errorf("generate %s log artifact id: %w", kind, err)
	}
	artifactID := fmt.Sprintf("log-%s-%s-%s", kind, ownerID, suffix)
	finalized, err := store.UploadContentArtifact(ctx, st.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: workspaceKey,
		ArtifactID:   artifactID,
		OwnerType:    ownerIDType(kind),
		OwnerID:      ownerID,
		Type:         "log",
		Summary:      kind + " log",
		MIMEType:     logMIME,
		Visibility:   "workspace",
		Metadata: map[string]string{
			"log.truncated":      strconv.FormatBool(truncated),
			"log.original_bytes": strconv.Itoa(originalBytes),
		},
	}, []byte(content))
	if err != nil {
		return "", fmt.Errorf("upload %s log: %w", kind, err)
	}
	return "artifact://" + finalized.ArtifactID, nil
}

func ownerIDType(kind string) string {
	if kind == "task" {
		return "task_run"
	}
	return "driver_run"
}

func randomSuffix() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Get resolves an artifact reference returned by PutTask or PutRun.
func Get(ctx context.Context, st store.Store, workspaceKey, ref string) (Log, error) {
	artifactID, err := artifactIDFromRef(ref)
	if err != nil {
		return Log{}, err
	}
	if st == nil {
		return Log{}, fmt.Errorf("resolve log artifact %q: %w", artifactID, domain.ErrNotFound)
	}
	artifactStore := st.Artifacts()
	artifact, err := artifactStore.Get(ctx, workspaceKey, artifactID)
	if err != nil {
		return Log{}, fmt.Errorf("get log artifact %q: %w", artifactID, err)
	}
	reader, ok := artifactStore.(store.ArtifactContentReader)
	if !ok {
		return Log{}, fmt.Errorf("read log artifact %q: content reader unavailable: %w", artifactID, domain.ErrNotFound)
	}
	content, err := reader.ReadContent(ctx, workspaceKey, artifactID)
	if err != nil {
		return Log{}, fmt.Errorf("read log artifact %q: %w", artifactID, err)
	}
	modifiedAt := artifact.UpdatedAt
	if artifact.FinalizedAt != nil {
		modifiedAt = *artifact.FinalizedAt
	}
	truncated, _ := strconv.ParseBool(artifact.Metadata["log.truncated"])
	return Log{Content: string(content), ModifiedAt: modifiedAt, Truncated: truncated}, nil
}

func artifactIDFromRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != "artifact" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		if err != nil {
			return "", fmt.Errorf("parse log ref %q: %v: %w", ref, err, domain.ErrNotFound)
		}
		return "", fmt.Errorf("parse log ref %q: %w", ref, domain.ErrNotFound)
	}
	return parsed.Host, nil
}
