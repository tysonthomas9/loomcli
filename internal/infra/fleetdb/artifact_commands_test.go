package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArtifactCommandCreatePreservesExecutionArtifactFields(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/artifact-commands/create" {
			t.Fatalf("request = %s %s, want artifact create route", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "secret-token" {
			t.Fatalf("X-Lease-Token = %q, want secret-token", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		if _, exposed := raw["lease_token"]; exposed {
			t.Fatal("artifact create body exposed lease_token")
		}
		var request struct {
			CommandID   string `json:"command_id"`
			ArtifactID  string `json:"artifact_id"`
			SessionID   string `json:"session_id"`
			TaskID      string `json:"task_id"`
			Type        string `json:"type"`
			URI         string `json:"uri"`
			SizeBytes   *int64 `json:"size_bytes"`
			Checksum    string `json:"checksum"`
			ContentHash string `json:"content_hash"`
		}
		body, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.ArtifactID != "artifact-1" || request.SessionID != "session-1" ||
			request.TaskID != "task-1" || request.Type != "transcript" || request.URI != "s3://bucket/artifact-1" ||
			request.SizeBytes == nil || *request.SizeBytes != 0 || request.Checksum != "sha256:checksum" || request.ContentHash != "sha256:content" {
			t.Fatalf("artifact create execution fields = %+v", request)
		}
		writeJSON(t, w, map[string]any{
			"artifact": artifactCreateTestSnapshot(now, request.SessionID, request.TaskID, request.URI, *request.SizeBytes, request.Checksum, request.ContentHash),
			"receipt": map[string]any{
				"workspace_key": "WS", "command_id": request.CommandID, "artifact_id": request.ArtifactID,
				"command_type": "artifact_create", "request_fingerprint": "sha256:request", "artifact_revision": 1,
				"committed_at": now,
			},
		})
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := client.ArtifactCommands().Create(context.Background(), artifactCreateTestOwner(), ArtifactCreateCommand{
		ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "task-1", Type: "transcript",
		URI: "s3://bucket/artifact-1", SizeBytes: 0, Checksum: "sha256:checksum", ContentHash: "sha256:content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SessionID != "session-1" || artifact.TaskID != "task-1" || artifact.URI != "s3://bucket/artifact-1" ||
		artifact.SizeBytes != 0 || artifact.Checksum != "sha256:checksum" || artifact.ContentHash != "sha256:content" {
		t.Fatalf("artifact response = %+v", artifact)
	}
}

func TestArtifactCommandCreateRejectsDivergentExecutionArtifactFields(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "session id", mutate: func(artifact map[string]any) { artifact["session_id"] = "wrong" }},
		{name: "task id", mutate: func(artifact map[string]any) { artifact["task_id"] = "wrong" }},
		{name: "uri", mutate: func(artifact map[string]any) { artifact["uri"] = "wrong" }},
		{name: "size bytes", mutate: func(artifact map[string]any) { artifact["size_bytes"] = int64(8) }},
		{name: "checksum", mutate: func(artifact map[string]any) { artifact["checksum"] = "wrong" }},
		{name: "content hash", mutate: func(artifact map[string]any) { artifact["content_hash"] = "wrong" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					CommandID  string `json:"command_id"`
					ArtifactID string `json:"artifact_id"`
				}
				decodeJSONBody(t, r, &request)
				artifact := artifactCreateTestSnapshot(now, "session-1", "task-1", "s3://artifact", 7, "checksum", "content")
				test.mutate(artifact)
				writeJSON(t, w, map[string]any{
					"artifact": artifact,
					"receipt": map[string]any{
						"workspace_key": "WS", "command_id": request.CommandID, "artifact_id": request.ArtifactID,
						"command_type": "artifact_create", "request_fingerprint": "sha256:request", "artifact_revision": 1,
						"committed_at": now,
					},
				})
			}))
			defer server.Close()

			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ArtifactCommands().Create(context.Background(), artifactCreateTestOwner(), ArtifactCreateCommand{
				ArtifactID: "artifact-1", SessionID: "session-1", TaskID: "task-1", Type: "transcript",
				URI: "s3://artifact", SizeBytes: 7, Checksum: "checksum", ContentHash: "content",
			})
			if !errors.Is(err, ErrArtifactsUnavailable) {
				t.Fatalf("Create() error = %v, want ErrArtifactsUnavailable", err)
			}
		})
	}
}

func TestArtifactCommandCreateRejectsNegativeSizeBeforeRequest(t *testing.T) {
	client, err := New(Config{BaseURL: "http://unused.invalid", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ArtifactCommands().Create(context.Background(), artifactCreateTestOwner(), ArtifactCreateCommand{
		ArtifactID: "artifact-1", Type: "transcript", SizeBytes: -1,
	})
	if !errors.Is(err, ErrArtifactsInvalid) {
		t.Fatalf("Create() error = %v, want ErrArtifactsInvalid", err)
	}
}

func TestArtifactCommandUploadGoesDirectlyToReceiptFirstRoute(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	content := []byte("hello")
	digest := artifactContentDigest(content)
	requestCount := 0
	var firstCommandID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/artifacts/artifact-1/commands/upload" {
			t.Fatalf("request = %s %s, want direct artifact upload command", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain" {
			t.Fatalf("Content-Type = %q, want artifact MIME type text/plain", got)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "secret-token" {
			t.Fatalf("X-Lease-Token = %q, want write-only token header", got)
		}
		if got := r.Header.Get("X-Expected-Revision"); got != "1" {
			t.Fatalf("X-Expected-Revision = %q, want original upload CAS 1", got)
		}
		commandID := r.Header.Get("X-Command-ID")
		if commandID == "" {
			t.Fatal("missing X-Command-ID")
		}
		if firstCommandID == "" {
			firstCommandID = commandID
		} else if commandID != firstCommandID {
			t.Fatalf("exact upload retry command ID changed from %q to %q", firstCommandID, commandID)
		}
		writeJSON(t, w, map[string]any{
			"artifact": artifactUploadTestSnapshot(now, 2, digest, "uploading"),
			"receipt": map[string]any{
				"workspace_key": "WS", "command_id": commandID, "artifact_id": "artifact-1",
				"command_type": "artifact_upload", "request_fingerprint": "sha256:request",
				"artifact_revision": 2, "committed_at": now,
			},
			"replayed": true,
		})
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := client.ArtifactCommands().Upload(t.Context(), artifactCreateTestOwner(), ArtifactUploadCommand{
		ArtifactID: "artifact-1", Content: content, MIMEType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Revision != 2 || artifact.ContentHash != digest {
		t.Fatalf("Upload() = %#v", artifact)
	}
	if _, err := client.ArtifactCommands().Upload(t.Context(), artifactCreateTestOwner(), ArtifactUploadCommand{
		ArtifactID: "artifact-1", Content: content, MIMEType: "text/plain",
	}); err != nil {
		t.Fatalf("exact Upload() retry: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("upload requests = %d, want exact retry to reach receipt-first route", requestCount)
	}
}

func TestArtifactCommandFinalizeReachesReceiptAfterOwnerTermination(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	digest := artifactContentDigest([]byte("hello"))
	postCount := 0
	var commandID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/artifacts/artifact-1":
			if got := r.Header.Get("X-Lease-Token"); got != "" {
				t.Fatalf("owner token leaked into replay-preparation read: %q", got)
			}
			writeJSON(t, w, artifactUploadTestSnapshot(now, 4, digest, "finalized"))
		case r.Method == http.MethodGet:
			t.Fatalf("owner-fenced GET ran before receipt replay: %s", r.URL.Path)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/artifacts/artifact-1/commands/finalize":
			postCount++
			if got := r.Header.Get("X-Lease-Token"); got != "secret-token" {
				t.Fatalf("X-Lease-Token = %q, want token only on command header", got)
			}
			var raw map[string]json.RawMessage
			decodeJSONBody(t, r, &raw)
			if _, exposed := raw["lease_token"]; exposed {
				t.Fatal("finalize body exposed lease_token")
			}
			var request struct {
				CommandID        string `json:"command_id"`
				ExpectedRevision uint64 `json:"expected_revision"`
			}
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			if commandID == "" {
				commandID = request.CommandID
			} else if request.CommandID != commandID {
				t.Fatalf("finalize command id changed from %q to %q", commandID, request.CommandID)
			}
			if request.ExpectedRevision != 2 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			artifact := artifactUploadTestSnapshot(now, 3, digest, "finalized")
			writeJSON(t, w, map[string]any{
				"artifact": artifact,
				"receipt": map[string]any{
					"workspace_key": "WS", "command_id": request.CommandID, "artifact_id": "artifact-1",
					"command_type": "artifact_finalize", "request_fingerprint": "sha256:request",
					"artifact_revision": 3, "committed_at": now,
				},
				"replayed": true,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := client.ArtifactCommands().Finalize(t.Context(), artifactCreateTestOwner(), ArtifactFinalizeCommand{
		ArtifactID: "artifact-1", ContentHash: &digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Revision != 3 || postCount != 3 {
		t.Fatalf("Finalize() = %#v, posts=%d; want receipt replay at original revision", artifact, postCount)
	}
}

func TestArtifactCommandFailUsesOwnerFencedReceiptContract(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		switch step {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/artifacts/artifact-1" || r.Header.Get("X-Lease-Token") != "" {
				t.Fatalf("prepare request = %s %s token=%q", r.Method, r.URL.Path, r.Header.Get("X-Lease-Token"))
			}
			writeJSON(t, w, artifactUploadTestSnapshot(now, 1, "", "pending"))
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/artifacts/artifact-1/commands/fail" {
				t.Fatalf("fail request = %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("X-Lease-Token") != "secret-token" {
				t.Fatalf("fail token = %q", r.Header.Get("X-Lease-Token"))
			}
			var raw map[string]json.RawMessage
			decodeJSONBody(t, r, &raw)
			if _, exposed := raw["lease_token"]; exposed {
				t.Fatal("artifact fail body exposed lease_token")
			}
			var request struct {
				CommandID        string            `json:"command_id"`
				ExpectedRevision uint64            `json:"expected_revision"`
				FailureClass     string            `json:"failure_class"`
				FailureMessage   string            `json:"failure_message"`
				Metadata         map[string]string `json:"metadata"`
			}
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			if request.CommandID == "" || request.ExpectedRevision != 1 || request.FailureClass != "capture_failed" ||
				request.FailureMessage != "evidence preparation failed" {
				t.Fatalf("fail command = %+v", request)
			}
			artifact := artifactUploadTestSnapshot(now, 2, "", "failed")
			artifact["metadata"] = map[string]string{
				"loom.evidence.capture_status": "capture_failed",
				"loom.evidence.failure_class":  request.FailureClass,
			}
			writeJSON(t, w, map[string]any{
				"artifact": artifact,
				"receipt": map[string]any{
					"workspace_key": "WS", "command_id": request.CommandID, "artifact_id": "artifact-1",
					"command_type": "artifact_fail", "request_fingerprint": "sha256:request",
					"artifact_revision": 2, "committed_at": now,
				},
			})
		default:
			t.Fatalf("unexpected request %d: %s %s", step, r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := client.ArtifactCommands().Fail(t.Context(), artifactCreateTestOwner(), ArtifactFailCommand{
		ArtifactID: "artifact-1", FailureClass: "capture_failed", FailureMessage: "evidence preparation failed",
		Metadata: map[string]string{"loom.evidence.capture_status": "capture_failed"},
	})
	if err != nil || artifact.DurableStatus != "failed" || artifact.FinalizedAt != nil || step != 2 {
		t.Fatalf("Fail() = %#v, %v, requests=%d", artifact, err, step)
	}
}

func TestArtifactCommandReferenceUsesDurableOwnerFencedContract(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			if got := r.Header.Get("X-Lease-Token"); got != "" {
				t.Fatalf("generic artifact read leaked X-Lease-Token = %q", got)
			}
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/artifacts/artifact-1" {
				t.Fatalf("initial request = %s %s", r.Method, r.URL.Path)
			}
			writeJSON(t, w, artifactReferenceTestSnapshot(now, 3))
		case 2:
			if got := r.Header.Get("X-Lease-Token"); got != "secret-token" {
				t.Fatalf("reference command X-Lease-Token = %q, want secret-token", got)
			}
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/artifacts/artifact-1/commands/reference" {
				t.Fatalf("reference request = %s %s", r.Method, r.URL.Path)
			}
			var raw map[string]json.RawMessage
			decodeJSONBody(t, r, &raw)
			if _, exposed := raw["lease_token"]; exposed {
				t.Fatal("artifact reference body exposed lease_token")
			}
			var request struct {
				CommandID        string `json:"command_id"`
				TaskRunID        string `json:"task_run_id"`
				NodeID           string `json:"node_id"`
				LeaseID          string `json:"lease_id"`
				FencingToken     int64  `json:"fencing_token"`
				ExpectedRevision uint64 `json:"expected_revision"`
				Kind             string `json:"kind"`
				TargetRef        string `json:"target_ref"`
			}
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			if request.CommandID == "" || request.TaskRunID != "task-run-1" || request.NodeID != "node-1" ||
				request.LeaseID != "lease-1" || request.FencingToken != 7 || request.ExpectedRevision != 3 ||
				request.Kind != "task-output" || request.TargetRef != "task-run://task-run-1/output" {
				t.Fatalf("reference command = %+v", request)
			}
			writeJSON(t, w, artifactReferenceTestResponse(now, request.CommandID, 4, request.TargetRef, false))
		default:
			t.Fatalf("unexpected request %d: %s %s", requestCount, r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ArtifactCommands().Reference(context.Background(), artifactCreateTestOwner(), ArtifactReferenceCommand{
		ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact == nil || result.Artifact.Revision != 4 || result.Reference == nil ||
		result.Reference.Kind != "task-output" || result.Reference.TargetRef != "task-run://task-run-1/output" || result.Replayed {
		t.Fatalf("Reference result = %#v", result)
	}
}

func TestArtifactCommandReferenceReplaysOriginalRevisionAfterLostResponse(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	postCount := 0
	var commandID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, artifactReferenceTestSnapshot(now, 4))
			return
		}
		postCount++
		var request struct {
			CommandID        string `json:"command_id"`
			ExpectedRevision uint64 `json:"expected_revision"`
			TargetRef        string `json:"target_ref"`
		}
		decodeJSONBody(t, r, &request)
		if commandID == "" {
			commandID = request.CommandID
		} else if request.CommandID != commandID {
			t.Fatalf("replay command id changed from %q to %q", commandID, request.CommandID)
		}
		switch request.ExpectedRevision {
		case 4:
			w.WriteHeader(http.StatusConflict)
		case 3:
			writeJSON(t, w, artifactReferenceTestResponse(now, request.CommandID, 4, request.TargetRef, true))
		default:
			t.Fatalf("unexpected expected revision %d", request.ExpectedRevision)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ArtifactCommands().Reference(context.Background(), artifactCreateTestOwner(), ArtifactReferenceCommand{
		ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || postCount != 2 || result.Reference == nil || result.Reference.ReferenceID != commandID {
		t.Fatalf("Reference replay = %#v, posts=%d", result, postCount)
	}
}

func TestArtifactCommandReferenceRejectsDivergentReferenceResult(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, artifactReferenceTestSnapshot(now, 3))
			return
		}
		var request struct {
			CommandID string `json:"command_id"`
			TargetRef string `json:"target_ref"`
		}
		decodeJSONBody(t, r, &request)
		result := artifactReferenceTestResponse(now, request.CommandID, 4, request.TargetRef, false)
		result["reference"].(map[string]any)["target_ref"] = "task-run://foreign/output"
		writeJSON(t, w, result)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ArtifactCommands().Reference(context.Background(), artifactCreateTestOwner(), ArtifactReferenceCommand{
		ArtifactID: "artifact-1", Kind: "task-output", TargetRef: "task-run://task-run-1/output",
	})
	if !errors.Is(err, ErrArtifactsUnavailable) {
		t.Fatalf("Reference() error = %v, want ErrArtifactsUnavailable", err)
	}
}

func artifactCreateTestOwner() ArtifactOwner {
	return ArtifactOwner{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 7,
	}
}

func artifactCreateTestSnapshot(now time.Time, sessionID, taskID, uri string, sizeBytes int64, checksum, contentHash string) map[string]any {
	return map[string]any{
		"workspace_key": "WS", "artifact_id": "artifact-1", "session_id": sessionID, "task_id": taskID,
		"owner_type": "task_run", "owner_id": "task-run-1", "type": "transcript", "uri": uri,
		"size_bytes": sizeBytes, "checksum": checksum, "content_hash": contentHash,
		"durable_status": "pending", "revision": 1, "created_at": now, "updated_at": now,
	}
}

func artifactReferenceTestSnapshot(now time.Time, revision uint64) map[string]any {
	return map[string]any{
		"workspace_key": "WS", "artifact_id": "artifact-1", "owner_type": "task_run", "owner_id": "task-run-1",
		"type": "logs", "content_hash": "sha256:content", "durable_status": "finalized", "revision": revision,
		"finalized_at": now, "created_at": now, "updated_at": now,
	}
}

func artifactUploadTestSnapshot(now time.Time, revision uint64, digest, status string) map[string]any {
	artifact := map[string]any{
		"workspace_key": "WS", "artifact_id": "artifact-1", "owner_type": "task_run", "owner_id": "task-run-1",
		"type": "logs", "uri": "artifact://artifact-1/content", "mime_type": "text/plain", "size_bytes": 5,
		"checksum": digest, "content_hash": digest, "durable_status": status, "revision": revision,
		"created_at": now, "updated_at": now,
	}
	if status == "finalized" {
		artifact["finalized_at"] = now
	}
	return artifact
}

func artifactReferenceTestResponse(now time.Time, commandID string, revision uint64, targetRef string, replayed bool) map[string]any {
	return map[string]any{
		"artifact": artifactReferenceTestSnapshot(now, revision),
		"reference": map[string]any{
			"workspace_key": "WS", "reference_id": commandID, "artifact_id": "artifact-1",
			"owner_type": "task_run", "owner_id": "task-run-1", "kind": "task-output", "target_ref": targetRef,
			"created_at": now,
		},
		"receipt": map[string]any{
			"workspace_key": "WS", "command_id": commandID, "artifact_id": "artifact-1",
			"command_type": "artifact_reference", "request_fingerprint": "sha256:request", "artifact_revision": revision,
			"reference_id": commandID, "committed_at": now,
		},
		"replayed": replayed,
	}
}
