package fleetdb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSessionArtifactTransportUsesGenericOwnerScopedRoutes(t *testing.T) {
	now := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	content := []byte("{\"text\":\"hello\"}\n")
	digest := artifactContentDigest(content)
	step := 0
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		step++
		base := Artifact{
			WorkspaceKey: "WS", ArtifactID: "transcript-session-1", AgentID: "agent-1",
			SessionID: "session-1", TaskID: "TASK-1", OwnerType: "session", OwnerID: "session-1",
			Type: "transcript", Summary: "interactive session transcript", MIMEType: "application/x-ndjson",
			SizeBytes: int64(len(content)), ContentHash: digest, DurableStatus: "declared",
			CreatedAt: now, UpdatedAt: now,
		}
		switch step {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/WS/artifacts" {
				t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["owner_type"] != "session" || body["owner_id"] != "session-1" ||
				body["agent_id"] != "agent-1" || body["session_id"] != "session-1" ||
				body["durable_status"] != "declared" {
				t.Fatalf("create body = %+v", body)
			}
			writeJSON(t, response, base)
		case 2:
			if request.Method != http.MethodPut || request.URL.Path != "/api/v1/WS/artifacts/transcript-session-1/content" {
				t.Fatalf("upload request = %s %s", request.Method, request.URL.Path)
			}
			got, err := io.ReadAll(request.Body)
			if err != nil || string(got) != string(content) || request.Header.Get("Content-Type") != "application/x-ndjson" {
				t.Fatalf("upload content = %q content-type %q error %v", got, request.Header.Get("Content-Type"), err)
			}
			base.DurableStatus = "uploading"
			writeJSON(t, response, base)
		case 3:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/WS/artifacts/transcript-session-1/finalize" {
				t.Fatalf("finalize request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["content_hash"] != digest {
				t.Fatalf("finalize body = %+v", body)
			}
			base.DurableStatus = "finalized"
			base.FinalizedAt = &now
			writeJSON(t, response, base)
		case 4:
			if request.Method != http.MethodGet || request.URL.Path != "/api/v1/WS/artifacts/transcript-session-1" {
				t.Fatalf("get request = %s %s", request.Method, request.URL.Path)
			}
			base.DurableStatus = "finalized"
			base.FinalizedAt = &now
			writeJSON(t, response, base)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})
	client, err := New(Config{BaseURL: "http://fleet.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.SessionArtifacts()
	owner := SessionArtifactOwner{WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1"}
	created, err := transport.CreateSession(t.Context(), owner, SessionArtifactCreateCommand{
		ArtifactID: "transcript-session-1", TaskID: "TASK-1", Type: "transcript",
		Summary: "interactive session transcript", MIMEType: "application/x-ndjson",
		SizeBytes: int64(len(content)), ContentHash: digest,
	})
	if err != nil || created.DurableStatus != "declared" {
		t.Fatalf("CreateSession = %#v, %v", created, err)
	}
	uploaded, err := transport.UploadSession(t.Context(), owner, ArtifactUploadCommand{
		ArtifactID: "transcript-session-1", Content: content, MIMEType: "application/x-ndjson",
	})
	if err != nil || uploaded.DurableStatus != "uploading" {
		t.Fatalf("UploadSession = %#v, %v", uploaded, err)
	}
	finalized, err := transport.FinalizeSession(t.Context(), owner, ArtifactFinalizeCommand{
		ArtifactID: "transcript-session-1", ContentHash: &digest,
	})
	if err != nil || finalized.DurableStatus != "finalized" || finalized.FinalizedAt == nil {
		t.Fatalf("FinalizeSession = %#v, %v", finalized, err)
	}
	got, err := transport.GetSession(t.Context(), owner, "transcript-session-1")
	if err != nil || got.OwnerID != owner.SessionID {
		t.Fatalf("GetSession = %#v, %v", got, err)
	}
	if step != 4 {
		t.Fatalf("request count = %d, want 4", step)
	}
}

func TestSessionArtifactTransportRejectsCrossOwnerResponse(t *testing.T) {
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, Artifact{
			WorkspaceKey: "WS", ArtifactID: "transcript-session-1", AgentID: "other-agent",
			SessionID: "session-1", OwnerType: "session", OwnerID: "session-1",
			Type: "transcript", DurableStatus: "finalized",
		})
	})
	client, err := New(Config{BaseURL: "http://fleet.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SessionArtifacts().GetSession(t.Context(), SessionArtifactOwner{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
	}, "transcript-session-1")
	if !errors.Is(err, ErrArtifactsUnavailable) {
		t.Fatalf("cross-owner response error = %v, want ErrArtifactsUnavailable", err)
	}
}
