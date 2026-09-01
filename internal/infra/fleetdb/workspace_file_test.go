package fleetdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestWorkspaceFileStorePublishesAndDownloadsBinaryTree(t *testing.T) {
	t.Parallel()

	content := []byte{0, 255, 'x', '\n'}
	digest := workspaceFileTestDigest(content)
	createdAt := time.Now().UTC()
	var uploaded []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Actor") != "alice" {
			t.Errorf("X-Actor = %q, want alice", r.Header.Get("X-Actor"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/FLEET/file-uploads":
			var request workspaceFileUploadRequestWire
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.ContentHash != digest || request.SizeBytes != int64(len(content)) || request.MediaType != "application/octet-stream" {
				t.Errorf("upload request = %#v", request)
			}
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "opaque-upload", "method": http.MethodPut,
				"url":        "/api/v1/FLEET/file-transfers/opaque-capability",
				"headers":    map[string][]string{"Content-Type": {"application/octet-stream"}},
				"expires_at": createdAt.Add(15 * time.Minute),
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/FLEET/file-transfers/opaque-capability":
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/FLEET/file-trees":
			var request publishWorkspaceFileTreeRequestWire
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if len(request.Files) != 1 || request.Files[0].Path != "bin/archive.zip" || !request.Files[0].Executable || request.Files[0].ContentHash != digest {
				t.Errorf("publish request = %#v", request)
			}
			w.Header().Set("ETag", `"wft1_binary"`)
			w.Header().Set("Location", "/api/v1/FLEET/file-trees/wft1_binary")
			writeWorkspaceFileTestTree(w, http.StatusCreated, createdAt, digest, len(content))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/FLEET/file-trees/wft1_binary":
			writeWorkspaceFileTestTree(w, http.StatusOK, createdAt, digest, len(content))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/FLEET/file-trees/wft1_binary/files/bin/archive.zip":
			writeWorkspaceFileTestJSON(w, http.StatusOK, workspaceFileTestFile(digest, len(content)))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/FLEET/file-trees/wft1_binary/downloads/bin/archive.zip":
			writeWorkspaceFileTestJSON(w, http.StatusOK, map[string]any{
				"method": http.MethodGet, "url": "/api/v1/FLEET/file-downloads/opaque-capability", "expires_at": createdAt.Add(15 * time.Minute),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/FLEET/file-downloads/opaque-capability":
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := newWorkspaceFileTestClient(t, server.URL, "alice", "")
	filesStore := client.WorkspaceFiles()
	result, err := filesStore.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{
		Path: "bin/archive.zip", Bytes: content, MediaType: "application/octet-stream", Executable: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, content) {
		t.Fatalf("uploaded bytes = %v, want %v", uploaded, content)
	}
	if result.Status != domain.WorkspaceFileTreePublished || result.ETag != `"wft1_binary"` || result.Location != "/api/v1/FLEET/file-trees/wft1_binary" {
		t.Fatalf("publish result = %#v", result)
	}
	tree, err := filesStore.GetTree(t.Context(), "FLEET", result.Tree.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || !tree.Files[0].Executable {
		t.Fatalf("tree = %#v", tree)
	}
	downloaded, err := filesStore.Download(t.Context(), "FLEET", tree.Revision, tree.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatalf("downloaded bytes = %v, want %v", downloaded, content)
	}
}

func TestWorkspaceFileStoreRenameCreatesNewTreeAndReusesBlob(t *testing.T) {
	registry.MarkEvidence(t, 22)

	content := []byte("shared bytes")
	digest := workspaceFileTestDigest(content)
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	var publications atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute),
			})
		case r.Method == http.MethodPut && r.URL.Path == "/transfer":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
			var request publishWorkspaceFileTreeRequestWire
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode publication: %v", err)
				return
			}
			if len(request.Files) != 1 || request.Files[0].ContentHash != digest {
				t.Errorf("publication = %#v, want one shared-content file", request.Files)
				return
			}
			sequence := publications.Add(1)
			revision := "wft1_original"
			if sequence == 2 {
				revision = "wft1_renamed"
			}
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"workspace_key": "FLEET", "revision": revision, "created_by": "alice", "created_at": createdAt,
				"files": []map[string]any{{
					"path": request.Files[0].Path, "blob_ref": "blob_shared_bytes", "content_hash": digest,
					"size_bytes": len(content), "media_type": "text/plain", "revision": "wff1_shared",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	files := newWorkspaceFileTestStore(t, server.URL, "alice", "")
	original, err := files.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "docs/old.md", Bytes: content, MediaType: "text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := files.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "docs/new.md", Bytes: content, MediaType: "text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	if original.Tree.Revision == renamed.Tree.Revision {
		t.Fatalf("rename retained tree revision %q", original.Tree.Revision)
	}
	if got, want := original.Tree.Files[0].Path, "docs/old.md"; got != want {
		t.Fatalf("original path = %q, want %q", got, want)
	}
	if got, want := renamed.Tree.Files[0].Path, "docs/new.md"; got != want {
		t.Fatalf("renamed path = %q, want %q", got, want)
	}
	if got, want := renamed.Tree.Files[0].BlobRef, original.Tree.Files[0].BlobRef; got != want {
		t.Fatalf("rename blob ref = %q, want reused %q", got, want)
	}
	if got, want := renamed.Tree.Files[0].ContentHash, original.Tree.Files[0].ContentHash; got != want {
		t.Fatalf("rename content hash = %q, want reused %q", got, want)
	}
}

func TestWorkspaceFileStorePreservesPublishStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		httpStatus int
		want       domain.WorkspaceFileTreeStatus
	}{
		{name: "existing", httpStatus: http.StatusOK, want: domain.WorkspaceFileTreeExisting},
		{name: "published", httpStatus: http.StatusCreated, want: domain.WorkspaceFileTreePublished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content := []byte("status")
			digest := workspaceFileTestDigest(content)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
					writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
						"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute),
					})
				case r.Method == http.MethodPut && r.URL.Path == "/transfer":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
					w.Header().Set("ETag", `"tree"`)
					w.Header().Set("Location", "/tree")
					writeWorkspaceFileTestJSON(w, tc.httpStatus, map[string]any{
						"workspace_key": "FLEET", "revision": "wft1_status", "created_by": "alice", "created_at": time.Now(),
						"files": []map[string]any{{"path": "a", "blob_ref": "blob", "content_hash": digest, "size_bytes": len(content), "revision": "wff1_status"}},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			filesStore := newWorkspaceFileTestStore(t, server.URL, "alice", "secret")
			result, err := filesStore.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "a", Bytes: content}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.want || result.ETag != `"tree"` || result.Location != "/tree" {
				t.Fatalf("result = %#v, want status %q and response headers", result, tc.want)
			}
		})
	}
}

func TestWorkspaceFileStoreWaitsUntilPendingTreeIsReadable(t *testing.T) {
	t.Parallel()

	content := []byte("pending")
	digest := workspaceFileTestDigest(content)
	createdAt := time.Now().UTC()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": createdAt.Add(time.Minute),
			})
		case r.Method == http.MethodPut && r.URL.Path == "/transfer":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
			w.Header().Set("ETag", `"wft1_pending"`)
			w.Header().Set("Location", "/api/v1/FLEET/file-trees/wft1_pending")
			writeWorkspaceFileTestJSON(w, http.StatusAccepted, map[string]any{
				"workspace_key": "FLEET", "revision": "wft1_pending", "created_by": "alice", "created_at": createdAt,
				"files": []map[string]any{{"path": "a", "blob_ref": "blob", "content_hash": digest, "size_bytes": len(content), "revision": "wff1_pending"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/FLEET/file-trees/wft1_pending":
			if reads.Add(1) == 1 {
				writeWorkspaceFileTestJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "pending"}})
				return
			}
			writeWorkspaceFileTestJSON(w, http.StatusOK, map[string]any{
				"workspace_key": "FLEET", "revision": "wft1_pending", "created_by": "alice", "created_at": createdAt,
				"files": []map[string]any{{"path": "a", "blob_ref": "blob", "content_hash": digest, "size_bytes": len(content), "revision": "wff1_pending"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := newWorkspaceFileTestStore(t, server.URL, "alice", "").Publish(
		t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "a", Bytes: content}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.WorkspaceFileTreePublished {
		t.Fatalf("Publish status = %q, want %q", result.Status, domain.WorkspaceFileTreePublished)
	}
	if reads.Load() != 2 {
		t.Fatalf("tree reads = %d, want 2", reads.Load())
	}
}

func TestWorkspaceFileStorePendingWaitHonorsCancellationAndDeadline(t *testing.T) {
	registry.MarkEvidence(t, 65)

	for _, tc := range []struct {
		name      string
		context   func(context.Context, <-chan struct{}) (context.Context, context.CancelFunc)
		wantError error
	}{
		{
			name: "cancellation",
			context: func(parent context.Context, firstRead <-chan struct{}) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				go func() {
					<-firstRead
					cancel()
				}()
				return ctx, cancel
			},
			wantError: context.Canceled,
		},
		{
			name: "deadline",
			context: func(parent context.Context, _ <-chan struct{}) (context.Context, context.CancelFunc) {
				return context.WithTimeout(parent, 50*time.Millisecond)
			},
			wantError: context.DeadlineExceeded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("pending")
			digest := workspaceFileTestDigest(content)
			firstRead := make(chan struct{})
			var signalOnce atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
					writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
						"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute),
					})
				case r.Method == http.MethodPut && r.URL.Path == "/transfer":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
					writeWorkspaceFileTestJSON(w, http.StatusAccepted, map[string]any{
						"workspace_key": "FLEET", "revision": "wft1_pending", "created_by": "alice", "created_at": time.Now(),
						"files": []map[string]any{{"path": "a", "blob_ref": "blob", "content_hash": digest, "size_bytes": len(content), "revision": "wff1_pending"}},
					})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/file-trees/wft1_pending"):
					if signalOnce.CompareAndSwap(false, true) {
						close(firstRead)
					}
					writeWorkspaceFileTestJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "pending"}})
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			ctx, cancel := tc.context(t.Context(), firstRead)
			defer cancel()
			started := time.Now()
			_, err := newWorkspaceFileTestStore(t, server.URL, "alice", "").Publish(
				ctx, "FLEET", []domain.WorkspaceFileInput{{Path: "a", Bytes: content}},
			)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("Publish error = %v, want %v", err, tc.wantError)
			}
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("pending wait returned after %s, want context-bounded return under 1s", elapsed)
			}
		})
	}

	t.Run("internal bound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeWorkspaceFileTestJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": "not_found", "message": "still pending"},
			})
		}))
		t.Cleanup(server.Close)
		filesStore := newWorkspaceFileTestClient(t, server.URL, "alice", "").WorkspaceFiles().(*workspaceFileStore)

		started := time.Now()
		_, err := filesStore.waitForTreeWithPolicy(t.Context(), "FLEET", "wft1_pending", pendingTreeWaitPolicy{
			PollInterval: 5 * time.Millisecond,
			Timeout:      40 * time.Millisecond,
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waitForTreeWithPolicy error = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(started); elapsed < 30*time.Millisecond || elapsed >= time.Second {
			t.Fatalf("internal pending bound returned after %s, want 30ms <= elapsed < 1s", elapsed)
		}
	})
}

func TestWorkspaceFileStoreSupportsAbsoluteProviderGrantsWithoutCredentialLeak(t *testing.T) {
	t.Parallel()
	registry.MarkEvidence(t, 38, 39, 45)

	content := []byte{0x50, 0x4b, 0, 0xff}
	digest := workspaceFileTestDigest(content)
	var uploaded []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("provider Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("provider X-API-Key = %q, want empty", got)
		}
		if got := r.Header.Get("X-Fleet-API-Key"); got != "" {
			t.Errorf("provider X-Fleet-API-Key = %q, want empty", got)
		}
		if got := r.Header.Get("X-Actor"); got != "" {
			t.Errorf("provider X-Actor = %q, want empty", got)
		}
		if got := r.Header.Get("X-Capability"); got != "provider-only" {
			t.Errorf("provider X-Capability = %q", got)
		}
		if r.URL.Path == "/fail" {
			http.Error(w, "provider echoed provider-only and query-secret", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodPut:
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)

	fleet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fleet-token" || r.Header.Get("X-API-Key") != "secret" || r.Header.Get("X-Fleet-API-Key") != "secret" || r.Header.Get("X-Actor") != "alice" {
			t.Errorf("Fleet credentials = Authorization %q, X-API-Key %q, X-Fleet-API-Key %q, X-Actor %q", r.Header.Get("Authorization"), r.Header.Get("X-API-Key"), r.Header.Get("X-Fleet-API-Key"), r.Header.Get("X-Actor"))
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "upload", "method": http.MethodPut, "url": provider.URL + "/object",
				"headers": map[string][]string{"X-Capability": {"provider-only"}}, "expires_at": time.Now().Add(time.Minute),
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, workspaceFileAbsoluteTree(digest, len(content)))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/files/"):
			writeWorkspaceFileTestJSON(w, http.StatusOK, workspaceFileAbsoluteFile(digest, len(content)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/downloads/"):
			writeWorkspaceFileTestJSON(w, http.StatusOK, map[string]any{
				"method": http.MethodGet, "url": provider.URL + "/object",
				"headers": map[string][]string{"X-Capability": {"provider-only"}}, "expires_at": time.Now().Add(time.Minute),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fleet.Close)

	client := newWorkspaceFileTestClient(t, fleet.URL, "alice", "secret")
	client.SetAuthToken("fleet-token")
	filesStore := client.WorkspaceFiles()
	result, err := filesStore.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{
		Path: "archive.zip", Bytes: content, MediaType: "application/zip", Executable: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, content) {
		t.Fatalf("uploaded = %v, want %v", uploaded, content)
	}
	downloaded, err := filesStore.Download(t.Context(), "FLEET", result.Tree.Revision, "archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatalf("downloaded = %v, want %v", downloaded, content)
	}
	err = client.executeWorkspaceFileTransfer(t.Context(), &workspaceFileTransferGrantWire{
		Method: http.MethodPut, URL: provider.URL + "/fail?X-Signature=query-secret",
		Headers: map[string][]string{"X-Capability": {"provider-only"}}, ExpiresAt: time.Now().Add(time.Minute),
	}, bytes.NewReader(content))
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("provider failure error = %v, want HTTP 503", err)
	}
	if strings.Contains(err.Error(), "provider-only") || strings.Contains(err.Error(), "query-secret") {
		t.Fatalf("provider failure leaked signed values: %v", err)
	}
}

func TestWorkspaceFileStoreDownloadVerifiesIntegrityAndClosesBody(t *testing.T) {
	t.Parallel()
	closeFailure := io.ErrClosedPipe
	cases := []struct {
		name       string
		declared   []byte
		downloaded []byte
		closeErr   error
		wantErr    error
	}{
		{name: "wrong hash", declared: []byte("good"), downloaded: []byte("evil"), wantErr: domain.ErrIntegrity},
		{name: "too short", declared: []byte("expected"), downloaded: []byte("short"), wantErr: domain.ErrIntegrity},
		{name: "too long", declared: []byte("short"), downloaded: []byte("too-long"), wantErr: domain.ErrIntegrity},
		{name: "close failure", declared: []byte("good"), downloaded: []byte("good"), closeErr: closeFailure, wantErr: closeFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := &workspaceFileTrackingBody{Reader: bytes.NewReader(tc.downloaded), closeErr: tc.closeErr}
			httpClient := &http.Client{Transport: workspaceFileRoundTripper(func(r *http.Request) (*http.Response, error) {
				switch {
				case strings.Contains(r.URL.Path, "/files/"):
					return workspaceFileHTTPJSON(http.StatusOK, map[string]any{
						"path": "file", "blob_ref": "blob", "content_hash": workspaceFileTestDigest(tc.declared), "size_bytes": len(tc.declared), "revision": "opaque-file",
					}), nil
				case strings.Contains(r.URL.Path, "/downloads/"):
					return workspaceFileHTTPJSON(http.StatusOK, map[string]any{"method": http.MethodGet, "url": "https://provider.invalid/object", "expires_at": time.Now().Add(time.Minute)}), nil
				case r.URL.Host == "provider.invalid":
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL)
				}
			})}
			client := newWorkspaceFileTestClientWithHTTP(t, "http://fleet.invalid", "alice", "", httpClient)
			_, err := client.WorkspaceFiles().Download(t.Context(), "FLEET", "opaque-tree", "file")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Download error = %v, want %v", err, tc.wantErr)
			}
			if !body.closed.Load() {
				t.Fatal("download body was not closed")
			}
		})
	}
}

func TestWorkspaceFileStoreMapsHTTPAndContextErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, code: "not_found", want: domain.ErrNotFound},
		{name: "invalid", status: http.StatusBadRequest, code: "validation_failed", want: domain.ErrInvalid},
		{name: "conflict", status: http.StatusConflict, code: "conflict", want: domain.ErrConflict},
		{name: "expired", status: http.StatusGone, code: "expired", want: domain.ErrGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeWorkspaceFileTestJSON(w, tc.status, map[string]any{"error": map[string]any{"code": tc.code, "message": tc.name}})
			}))
			t.Cleanup(server.Close)
			_, err := newWorkspaceFileTestStore(t, server.URL, "alice", "").GetTree(t.Context(), "FLEET", "opaque-tree")
			if !errors.Is(err, tc.want) {
				t.Fatalf("GetTree error = %v, want %v", err, tc.want)
			}
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := newWorkspaceFileTestClient(t, "http://127.0.0.1:1", "alice", "")
	_, err := client.WorkspaceFiles().GetTree(ctx, "FLEET", "opaque-tree")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetTree canceled error = %v", err)
	}
}

func TestWorkspaceFileStoreRejectsInvalidManifestBeforeUpload(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	client := newWorkspaceFileTestClientWithHTTP(t, "http://fleet.invalid", "alice", "", &http.Client{Transport: workspaceFileRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not be called")
	})})
	_, err := client.WorkspaceFiles().Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{
		{Path: "scripts", Bytes: []byte("file")}, {Path: "scripts/run", Bytes: []byte("nested")},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Publish error = %v, want ErrInvalid", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("HTTP calls = %d, want 0", got)
	}
}

func TestWorkspaceFileStoreUploadFailurePublishesNoTree(t *testing.T) {
	registry.MarkEvidence(t, 61)

	var publicationRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute),
			})
		case r.Method == http.MethodPut && r.URL.Path == "/transfer":
			http.Error(w, "upload failed", http.StatusServiceUnavailable)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/file-trees"):
			publicationRequests.Add(1)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, err := newWorkspaceFileTestStore(t, server.URL, "alice", "").Publish(
		t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "file", Bytes: []byte("not published")}},
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Publish error = %v, want upload HTTP 503", err)
	}
	if got := publicationRequests.Load(); got != 0 {
		t.Fatalf("tree publication requests = %d, want 0 after upload failure", got)
	}
}

func TestWorkspaceFileStoreRejectsMissingWireResponses(t *testing.T) {
	t.Parallel()
	if err := validateWorkspaceFileGrant(nil, http.MethodGet, time.Now(), defaultWorkspaceFileGrantMaxTTL); err == nil {
		t.Fatal("nil grant accepted")
	}
	if err := validateWorkspaceFileGrant(&workspaceFileTransferGrantWire{}, http.MethodGet, time.Now(), defaultWorkspaceFileGrantMaxTTL); err == nil {
		t.Fatal("empty grant accepted")
	}
	if _, err := workspaceFileTreeFromWire(nil); err == nil {
		t.Fatal("nil tree accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeWorkspaceFileTestJSON(w, http.StatusOK, map[string]any{})
	}))
	t.Cleanup(server.Close)
	files := newWorkspaceFileTestStore(t, server.URL, "alice", "")
	if _, err := files.Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "file"}}); err == nil {
		t.Fatal("Publish accepted empty upload grant")
	}
	if _, err := files.GetTree(t.Context(), "FLEET", "opaque-tree"); err == nil {
		t.Fatal("GetTree accepted empty tree response")
	}
}

func TestWorkspaceFileStoreValidatesResponseManifestWithoutParsingOpaqueTokens(t *testing.T) {
	t.Parallel()
	digest := workspaceFileTestDigest(nil)
	var collide atomic.Bool
	collide.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		files := []map[string]any{{"path": "a", "blob_ref": "opaque", "content_hash": digest, "revision": "opaque-file-token"}}
		if collide.Load() {
			files = []map[string]any{
				{"path": "A", "blob_ref": "opaque-a", "content_hash": digest, "revision": "opaque-file-a"},
				{"path": "a", "blob_ref": "opaque-b", "content_hash": digest, "revision": "opaque-file-b"},
			}
		}
		writeWorkspaceFileTestJSON(w, http.StatusOK, map[string]any{
			"workspace_key": "FLEET", "revision": "server-owned-opaque-revision", "files": files, "future_field": "ignored",
		})
	}))
	t.Cleanup(server.Close)
	files := newWorkspaceFileTestStore(t, server.URL, "alice", "")
	_, err := files.GetTree(t.Context(), "FLEET", "server-owned-opaque-revision")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("GetTree error = %v, want invalid manifest", err)
	}
	collide.Store(false)
	tree, err := files.GetTree(t.Context(), "FLEET", "server-owned-opaque-revision")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Revision != "server-owned-opaque-revision" || tree.Files[0].Revision != "opaque-file-token" {
		t.Fatalf("opaque tokens changed: tree=%q file=%q", tree.Revision, tree.Files[0].Revision)
	}
}

func TestWorkspaceFileStoreRejectsResponseIdentityMismatch(t *testing.T) {
	t.Parallel()
	digest := workspaceFileTestDigest(nil)
	cases := []struct {
		name string
		path string
		body map[string]any
		run  func(store.WorkspaceFileStore) error
	}{
		{
			name: "get workspace",
			path: "/file-trees/",
			body: map[string]any{"workspace_key": "OTHER", "revision": "opaque-tree", "files": []map[string]any{{"path": "file", "blob_ref": "blob", "content_hash": digest}}},
			run: func(s store.WorkspaceFileStore) error {
				_, err := s.GetTree(t.Context(), "FLEET", "opaque-tree")
				return err
			},
		},
		{
			name: "get revision",
			path: "/file-trees/",
			body: map[string]any{"workspace_key": "FLEET", "revision": "different-tree", "files": []map[string]any{{"path": "file", "blob_ref": "blob", "content_hash": digest}}},
			run: func(s store.WorkspaceFileStore) error {
				_, err := s.GetTree(t.Context(), "FLEET", "opaque-tree")
				return err
			},
		},
		{
			name: "stat path",
			path: "/files/",
			body: map[string]any{"path": "other", "blob_ref": "blob", "content_hash": digest},
			run: func(s store.WorkspaceFileStore) error {
				_, err := s.Stat(t.Context(), "FLEET", "opaque-tree", "file")
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, tc.path) {
					http.NotFound(w, r)
					return
				}
				writeWorkspaceFileTestJSON(w, http.StatusOK, tc.body)
			}))
			t.Cleanup(server.Close)
			if err := tc.run(newWorkspaceFileTestStore(t, server.URL, "alice", "")); !errors.Is(err, domain.ErrIntegrity) {
				t.Fatalf("operation error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestWorkspaceFileStoreRejectsPublishedWorkspaceMismatch(t *testing.T) {
	t.Parallel()
	digest := workspaceFileTestDigest(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute),
			})
		case r.URL.Path == "/transfer":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/file-trees"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"workspace_key": "OTHER", "revision": "opaque-tree",
				"files": []map[string]any{{"path": "file", "blob_ref": "blob", "content_hash": digest}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	_, err := newWorkspaceFileTestStore(t, server.URL, "alice", "").Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "file"}})
	if !errors.Is(err, domain.ErrIntegrity) {
		t.Fatalf("Publish error = %v, want ErrIntegrity", err)
	}
}

func TestWorkspaceFileTransfersRejectRedirectsAndNon2xx(t *testing.T) {
	t.Parallel()
	registry.MarkEvidence(t, 40)
	var redirectTargetCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
		case "/target":
			redirectTargetCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/gone":
			writeWorkspaceFileTestJSON(w, http.StatusGone, map[string]any{"error": map[string]any{"code": "expired", "message": "expired"}})
		}
	}))
	t.Cleanup(server.Close)
	client := newWorkspaceFileTestClient(t, server.URL, "alice", "")
	if err := client.executeWorkspaceFileTransfer(t.Context(), &workspaceFileTransferGrantWire{Method: http.MethodPut, URL: "/redirect?X-Amz-Signature=secret", ExpiresAt: time.Now().Add(time.Minute)}, bytes.NewReader(nil)); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect error = %v", err)
	} else if strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirect error leaked signed query: %v", err)
	}
	if redirectTargetCalls.Load() != 0 {
		t.Fatal("workspace file transfer followed redirect")
	}
	if err := client.executeWorkspaceFileTransfer(t.Context(), &workspaceFileTransferGrantWire{Method: http.MethodPut, URL: "/gone", ExpiresAt: time.Now().Add(time.Minute)}, bytes.NewReader(nil)); !errors.Is(err, domain.ErrGone) {
		t.Fatalf("local non-2xx error = %v, want ErrGone", err)
	}

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(provider.Close)
	if err := client.executeWorkspaceFileTransfer(t.Context(), &workspaceFileTransferGrantWire{Method: http.MethodPut, URL: provider.URL, ExpiresAt: time.Now().Add(time.Minute)}, bytes.NewReader(nil)); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("provider non-2xx error = %v", err)
	}
}

func TestWorkspaceFileStoreRejectsUnsafeUploadGrantsBeforeTransfer(t *testing.T) {
	t.Parallel()
	registry.MarkEvidence(t, 41, 42, 44)
	for _, tc := range []struct {
		name    string
		method  string
		target  string
		expires time.Time
		want    string
	}{
		{name: "wrong method", method: http.MethodGet, target: "https://objects.example/upload", expires: time.Now().Add(time.Minute), want: "method"},
		{name: "expired", method: http.MethodPut, target: "https://objects.example/upload", expires: time.Now().Add(-time.Minute), want: "expired"},
		{name: "insecure remote", method: http.MethodPut, target: "http://objects.example/upload", expires: time.Now().Add(time.Minute), want: "HTTPS"},
		{name: "malformed URL", method: http.MethodPut, target: "https://objects.example/upload#fragment", expires: time.Now().Add(time.Minute), want: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var providerCalls atomic.Int64
			client := newWorkspaceFileTestClientWithHTTP(t, "https://fleet.example", "alice", "", &http.Client{Transport: workspaceFileRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "fleet.example" {
					return workspaceFileHTTPJSON(http.StatusCreated, map[string]any{
						"upload_token": "upload", "method": tc.method, "url": tc.target, "expires_at": tc.expires,
					}), nil
				}
				providerCalls.Add(1)
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
			})})
			_, err := client.WorkspaceFiles().Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "file"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Publish error = %v, want containing %q", err, tc.want)
			}
			if got := providerCalls.Load(); got != 0 {
				t.Fatalf("provider calls = %d, want 0", got)
			}
		})
	}
	if err := validateWorkspaceFileGrant(&workspaceFileTransferGrantWire{
		Method: http.MethodPut, URL: "https://objects.example/download", ExpiresAt: time.Now().Add(time.Minute),
	}, http.MethodGet, time.Now(), defaultWorkspaceFileGrantMaxTTL); err == nil || !strings.Contains(err.Error(), "method") {
		t.Fatalf("download grant accepted PUT: %v", err)
	}
	if err := validateWorkspaceFileGrant(&workspaceFileTransferGrantWire{
		Method: http.MethodPut, URL: "http://127.0.0.1:9000/upload", ExpiresAt: time.Now().Add(time.Minute),
	}, http.MethodPut, time.Now(), defaultWorkspaceFileGrantMaxTTL); err != nil {
		t.Fatalf("explicit loopback HTTP grant rejected: %v", err)
	}
}

func TestWorkspaceFileGrantExpiryHonorsConfiguredMaximum(t *testing.T) {
	registry.MarkEvidence(t, 43)

	maxTTL := 2 * time.Minute
	client, err := New(Config{BaseURL: "https://fleet.example", Actor: "alice", WorkspaceFileGrantMaxTTL: maxTTL})
	if err != nil {
		t.Fatal(err)
	}
	if client.workspaceFileGrantMaxTTL != maxTTL {
		t.Fatalf("configured grant max TTL = %s, want %s", client.workspaceFileGrantMaxTTL, maxTTL)
	}
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	grant := &workspaceFileTransferGrantWire{
		Method: http.MethodPut, URL: "https://objects.example/upload", ExpiresAt: now.Add(maxTTL),
	}
	if err := validateWorkspaceFileGrant(grant, http.MethodPut, now, client.workspaceFileGrantMaxTTL); err != nil {
		t.Fatalf("grant at exact configured maximum rejected: %v", err)
	}
	grant.ExpiresAt = now.Add(maxTTL + time.Nanosecond)
	if err := validateWorkspaceFileGrant(grant, http.MethodPut, now, client.workspaceFileGrantMaxTTL); err == nil || !strings.Contains(err.Error(), "maximum TTL") {
		t.Fatalf("grant above configured maximum error = %v, want maximum TTL rejection", err)
	}
}

func TestWorkspaceFileStoreUsesDynamicFleetCredentials(t *testing.T) {
	t.Parallel()
	content := []byte("dynamic")
	digest := workspaceFileTestDigest(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-token" || r.Header.Get("X-API-Key") != "new-key" || r.Header.Get("X-Fleet-API-Key") != "new-key" {
			t.Errorf("stale credentials on %s: Authorization=%q X-API-Key=%q X-Fleet-API-Key=%q", r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-API-Key"), r.Header.Get("X-Fleet-API-Key"))
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/file-uploads"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{"upload_token": "upload", "method": http.MethodPut, "url": "/transfer", "expires_at": time.Now().Add(time.Minute)})
		case r.URL.Path == "/transfer":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/file-trees"):
			writeWorkspaceFileTestJSON(w, http.StatusCreated, map[string]any{
				"workspace_key": "FLEET", "revision": "opaque-tree", "files": []map[string]any{{"path": "file", "blob_ref": "blob", "content_hash": digest, "size_bytes": len(content), "revision": "opaque-file"}},
			})
		}
	}))
	t.Cleanup(server.Close)
	client := newWorkspaceFileTestClient(t, server.URL, "alice", "old-key")
	client.SetAuthToken("new-token")
	client.SetAPIKey("new-key")
	if _, err := client.WorkspaceFiles().Publish(t.Context(), "FLEET", []domain.WorkspaceFileInput{{Path: "file", Bytes: content}}); err != nil {
		t.Fatal(err)
	}
}

func workspaceFileTestDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func workspaceFileTestFile(digest string, size int) map[string]any {
	return map[string]any{
		"path": "bin/archive.zip", "blob_ref": "blob:v1:opaque", "content_hash": digest,
		"size_bytes": size, "media_type": "application/octet-stream", "executable": true, "revision": "wff1_binary",
	}
}

func writeWorkspaceFileTestTree(w http.ResponseWriter, status int, createdAt time.Time, digest string, size int) {
	writeWorkspaceFileTestJSON(w, status, map[string]any{
		"workspace_key": "FLEET", "revision": "wft1_binary", "created_by": "alice", "created_at": createdAt,
		"files": []map[string]any{workspaceFileTestFile(digest, size)},
	})
}

func writeWorkspaceFileTestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func newWorkspaceFileTestStore(t *testing.T, serverURL, actor, apiKey string) store.WorkspaceFileStore {
	t.Helper()
	return newWorkspaceFileTestClient(t, serverURL, actor, apiKey).WorkspaceFiles()
}

func newWorkspaceFileTestClient(t *testing.T, serverURL, actor, apiKey string) *Client {
	t.Helper()
	return newWorkspaceFileTestClientWithHTTP(t, serverURL, actor, apiKey, nil)
}

func newWorkspaceFileTestClientWithHTTP(t *testing.T, serverURL, actor, apiKey string, httpClient *http.Client) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: serverURL, Actor: actor, APIKey: apiKey, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func workspaceFileAbsoluteFile(digest string, size int) map[string]any {
	return map[string]any{
		"path": "archive.zip", "blob_ref": "blob:opaque", "content_hash": digest,
		"size_bytes": size, "media_type": "application/zip", "executable": true, "revision": "wff1_absolute",
	}
}

func workspaceFileAbsoluteTree(digest string, size int) map[string]any {
	return map[string]any{
		"workspace_key": "FLEET", "revision": "wft1_absolute", "created_by": "alice", "created_at": time.Now(),
		"files": []map[string]any{workspaceFileAbsoluteFile(digest, size)},
	}
}

type workspaceFileTrackingBody struct {
	io.Reader
	closed   atomic.Bool
	closeErr error
}

func (b *workspaceFileTrackingBody) Close() error {
	b.closed.Store(true)
	return b.closeErr
}

type workspaceFileRoundTripper func(*http.Request) (*http.Response, error)

func (f workspaceFileRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func workspaceFileHTTPJSON(status int, body any) *http.Response {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}
