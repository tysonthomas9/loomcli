package svcimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const maxControlPlaneArtifactBytes = 16 << 20

func (s *sessionServiceImpl) readTranscriptRef(ctx context.Context, wsID, ref string) ([]byte, error) {
	return s.readArtifactRef(ctx, wsID, ref)
}

func (s *sessionServiceImpl) readArtifactRef(ctx context.Context, wsID, ref string) ([]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("empty artifact ref")
	}
	if strings.HasPrefix(ref, "artifact://") {
		artifactID := strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
		if artifactID == "" {
			return nil, errors.New("empty artifact ref")
		}
		return s.readArtifactID(ctx, wsID, artifactID)
	}
	return readArtifactURI(ctx, ref)
}

func (s *sessionServiceImpl) readArtifactID(ctx context.Context, wsID, artifactID string) ([]byte, error) {
	if artifactID == "" {
		return nil, errors.New("empty artifact ID")
	}
	if s.store == nil {
		return nil, errors.New("artifact store unavailable")
	}
	var contentErr error
	if reader, ok := s.store.Artifacts().(store.ArtifactContentReader); ok {
		data, err := reader.ReadContent(ctx, wsID, artifactID)
		if err == nil {
			return data, nil
		}
		contentErr = err
		if !errors.Is(err, domain.ErrNotFound) {
			logger.Warn("failed to read artifact content; falling back to artifact URI", "workspace", wsID, "artifact_id", artifactID, "err", err)
		}
	}
	artifact, err := s.store.Artifacts().Get(ctx, wsID, artifactID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		if contentErr != nil {
			return nil, fmt.Errorf("read artifact content failed: %w; artifact metadata lookup failed: %w", contentErr, err)
		}
		return nil, err
	}
	if strings.TrimSpace(artifact.URI) == "" {
		if errors.Is(contentErr, domain.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		if contentErr != nil {
			return nil, fmt.Errorf("read artifact content failed: %w; artifact has no URI fallback", contentErr)
		}
		return nil, domain.ErrNotFound
	}
	data, err := readArtifactURI(ctx, artifact.URI)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, domain.ErrNotFound
		}
		if contentErr != nil {
			return nil, fmt.Errorf("read artifact content failed: %w; artifact URI fallback failed: %w", contentErr, err)
		}
		return nil, err
	}
	return data, nil
}

func readArtifactURI(ctx context.Context, rawURI string) ([]byte, error) {
	rawURI = strings.TrimSpace(rawURI)
	switch {
	case strings.HasPrefix(rawURI, "file://"):
		parsed, err := url.Parse(rawURI)
		if err != nil {
			return nil, err
		}
		path := parsed.Path
		if path == "" {
			path = parsed.Host
		}
		if path == "" {
			return nil, errors.New("empty file artifact ref")
		}
		return os.ReadFile(path) //nolint:gosec // refs are emitted by the trusted runner/control-plane path.
	case strings.HasPrefix(rawURI, "http://"), strings.HasPrefix(rawURI, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURI, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, errors.New("artifact ref returned non-success status")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneArtifactBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > maxControlPlaneArtifactBytes {
			return nil, errors.New("artifact is too large")
		}
		return body, nil
	default:
		return nil, errors.New("unsupported artifact ref")
	}
}

func parseCanonicalTranscriptBytes(data []byte) ([]transcript.Event, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []transcript.Event{}, nil
	}
	if trimmed[0] == '[' {
		var events []transcript.Event
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return nil, err
		}
		return events, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	events := make([]transcript.Event, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event transcript.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func controlPlaneDiffArtifactRef(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	return normalizeArtifactRef(firstNonEmptySessionValue(
		metadata["patch_artifact_id"],
		metadata["diff_artifact_id"],
		metadata["patch_ref"],
		metadata["diff_ref"],
	))
}

func normalizeArtifactRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "artifact://") {
		ref = strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
	}
	return ref
}

func (s *sessionServiceImpl) diffArtifactIDForTaskRun(ctx context.Context, wsID, taskRunID string) (string, error) {
	taskRunID = strings.TrimSpace(taskRunID)
	if taskRunID == "" || s.store == nil {
		return "", nil
	}
	artifacts, err := s.store.Artifacts().List(ctx, wsID, store.ArtifactFilter{
		OwnerType: "task_run",
		OwnerID:   taskRunID,
		Type:      "patch",
		Status:    "finalized",
		Limit:     1,
	})
	if err != nil {
		return "", service.ErrInternal("failed to list patch artifacts", err)
	}
	for _, artifact := range artifacts {
		if artifact != nil && strings.TrimSpace(artifact.ArtifactID) != "" {
			return artifact.ArtifactID, nil
		}
	}
	return "", nil
}

func (s *sessionServiceImpl) readControlPlaneArtifactText(ctx context.Context, wsID, artifactID string) (string, error) {
	artifactID = normalizeArtifactRef(artifactID)
	if artifactID == "" {
		return "", errors.New("empty artifact ref")
	}
	if isSupportedControlPlaneURI(artifactID) {
		data, err := readArtifactURI(ctx, artifactID)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := s.readArtifactRef(ctx, wsID, "artifact://"+artifactID)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isSupportedControlPlaneURI(ref string) bool {
	return strings.HasPrefix(ref, "file://") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}
