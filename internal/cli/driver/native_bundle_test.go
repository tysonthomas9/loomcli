package driver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/driver/nativearchive"
)

func TestArchiveNativeDriverDistProducesCanonicalRegularFileArchive(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "chunks", "worker.mjs"), []byte("export const worker = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := archiveNativeDriverDist(dist)
	if err != nil {
		t.Fatalf("archiveNativeDriverDist: %v", err)
	}
	got := readNativeArchiveFiles(t, data)
	if string(got["server.mjs"]) != "export {};\n" ||
		string(got["chunks/worker.mjs"]) != "export const worker = true;\n" {
		t.Fatalf("archive files = %#v", got)
	}
}

func TestArchiveNativeDriverDistRejectsSymlinksAndOversizedExtraction(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		dist := t.TempDir()
		target := filepath.Join(dist, "server.mjs")
		if err := os.WriteFile(target, []byte("export {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dist, "escape.mjs")); err != nil {
			t.Fatal(err)
		}
		if _, err := archiveNativeDriverDist(dist); err == nil ||
			!strings.Contains(err.Error(), "unsupported file") {
			t.Fatalf("symlink error = %v", err)
		}
	})

	t.Run("oversized sparse file", func(t *testing.T) {
		dist := t.TempDir()
		file, err := os.Create(filepath.Join(dist, "server.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(nativearchive.MaxExtractBytes + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := archiveNativeDriverDist(dist); err == nil ||
			!strings.Contains(err.Error(), "upload limits") {
			t.Fatalf("oversized dist error = %v", err)
		}
	})
}

func TestRunDriverRegisterUsesExactManagementRouteAndOpenModeHasNoCredential(t *testing.T) {
	dist := nativeDriverCLIDist(t)
	var posts int
	var captured map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/config":
			writeNativeDriverCLIJSON(t, w, http.StatusOK, map[string]string{"mode": "open"})
		case r.Method == http.MethodPost:
			posts++
			if r.URL.EscapedPath() != "/api/workspaces/space%2Fname/workflow-catalog/native-drivers" {
				t.Fatalf("management route = %s", r.URL.EscapedPath())
			}
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("open-mode Authorization = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode registration: %v", err)
			}
			if _, present := captured["workspace_key"]; present {
				t.Fatal("CLI sent caller-controlled workspace_key")
			}
			if _, present := captured["created_by"]; present {
				t.Fatal("CLI sent caller-controlled created_by")
			}
			var archive []byte
			if err := json.Unmarshal(captured["archive"], &archive); err != nil {
				t.Fatalf("decode archive: %v", err)
			}
			if got := string(readNativeArchiveFiles(t, archive)["server.mjs"]); got != "export {};\n" {
				t.Fatalf("server.mjs = %q", got)
			}
			writeNativeDriverCLIJSON(t, w, http.StatusCreated, map[string]any{
				"driver": map[string]any{
					"workspace_key": "space/name",
					"driver_id":     "demo",
					"name":          "demo",
				},
				"version": map[string]any{
					"workspace_key": "space/name",
					"driver_id":     "demo",
					"version_id":    "driver-version-1",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("LOOM_SERVER_URL", server.URL)
	t.Setenv("LOOM_WORKSPACE", "space/name")
	restoreNativeDriverCLIState(t)
	driverRegisterFlueDist = dist
	driverRegisterName = "demo"

	command := &cobra.Command{}
	command.SetContext(context.Background())
	if err := runDriverRegister(command, nil); err != nil {
		t.Fatalf("runDriverRegister: %v", err)
	}
	if posts != 1 {
		t.Fatalf("registration posts = %d, want 1", posts)
	}
}

func TestRunDriverRegisterFailsClosedWhenManagementEndpointIsMissing(t *testing.T) {
	dist := nativeDriverCLIDist(t)
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/config" {
			writeNativeDriverCLIJSON(t, w, http.StatusOK, map[string]string{"mode": "open"})
			return
		}
		posts++
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("LOOM_SERVER_URL", server.URL)
	t.Setenv("LOOM_WORKSPACE", "TEST")
	restoreNativeDriverCLIState(t)
	driverRegisterFlueDist = dist
	driverRegisterName = "demo"

	command := &cobra.Command{}
	command.SetContext(context.Background())
	err := runDriverRegister(command, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing endpoint error = %v", err)
	}
	if posts != 1 {
		t.Fatalf("missing endpoint posts = %d, want exactly 1 and no fallback", posts)
	}
}

func readNativeArchiveFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	files := map[string][]byte{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		if header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
			t.Fatalf("non-canonical archive ownership on %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read %q: %v", header.Name, err)
		}
		files[header.Name] = content
	}
	return files
}

func nativeDriverCLIDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dist
}

func restoreNativeDriverCLIState(t *testing.T) {
	t.Helper()
	original := struct {
		dist, manifest, name, id, workflow, sourceRef, sourceDigest string
		activate, trusted, untrusted, jsonOutput                    bool
	}{
		driverRegisterFlueDist, driverRegisterManifest, driverRegisterName,
		driverRegisterID, driverRegisterWorkflow, driverRegisterSourceRef,
		driverRegisterSourceDigest, driverRegisterActivate, driverRegisterTrusted,
		driverRegisterUntrusted, driverRegisterJSON,
	}
	t.Cleanup(func() {
		driverRegisterFlueDist, driverRegisterManifest, driverRegisterName = original.dist, original.manifest, original.name
		driverRegisterID, driverRegisterWorkflow = original.id, original.workflow
		driverRegisterSourceRef, driverRegisterSourceDigest = original.sourceRef, original.sourceDigest
		driverRegisterActivate, driverRegisterTrusted = original.activate, original.trusted
		driverRegisterUntrusted, driverRegisterJSON = original.untrusted, original.jsonOutput
	})
	driverRegisterFlueDist, driverRegisterManifest, driverRegisterName = "", "", ""
	driverRegisterID, driverRegisterWorkflow = "", ""
	driverRegisterSourceRef, driverRegisterSourceDigest = "", ""
	driverRegisterActivate, driverRegisterTrusted, driverRegisterUntrusted, driverRegisterJSON = false, false, false, false
}

func writeNativeDriverCLIJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
