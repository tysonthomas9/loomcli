package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const maxRunPayloadBytes = 4 << 20

type Module struct {
	store     store.Store
	builtinMu sync.Mutex
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}/versions", m.createWorkflowVersion)
	mux.HandleFunc("POST /api/workspaces/{ws}/workflows/{name}", m.createWorkflowRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}", m.getRun)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/events", m.getRunEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/runs/{runId}/stream", m.streamRunEvents)
}

type createWorkflowVersionRequest struct {
	Files      map[string]string `json:"files"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Activate   *bool             `json:"activate,omitempty"`
}

func (m *Module) createWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	entrypoint, files, activate, err := parseCreateWorkflowVersionRequest(w, r, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, buildOutput, err := m.buildAndRegister(r.Context(), ws, name, entrypoint, files, activate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, createWorkflowVersionResponse(result, buildOutput))
}

func parseCreateWorkflowVersionRequest(w http.ResponseWriter, r *http.Request, name string) (string, map[string]string, bool, error) {
	if name == "" {
		return "", nil, false, fmt.Errorf("workflow name is required")
	}
	var req createWorkflowVersionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes)).Decode(&req); err != nil {
		return "", nil, false, fmt.Errorf("invalid JSON body")
	}
	if len(req.Files) == 0 {
		return "", nil, false, fmt.Errorf("files is required")
	}
	entrypoint := workflowVersionEntrypoint(name, req.Entrypoint)
	if err := validateWorkflowEntrypoint(name, entrypoint); err != nil {
		return "", nil, false, err
	}
	files, err := validateWorkflowFiles(req.Files)
	if err != nil {
		return "", nil, false, err
	}
	if _, ok := files[entrypoint]; !ok {
		return "", nil, false, fmt.Errorf("entrypoint file is missing")
	}
	return entrypoint, files, workflowVersionActivateDefault(req.Activate), nil
}

func workflowVersionEntrypoint(name, raw string) string {
	entrypoint := strings.TrimSpace(raw)
	if entrypoint == "" {
		return filepath.ToSlash(filepath.Join("workflows", name+".ts"))
	}
	return entrypoint
}

func workflowVersionActivateDefault(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func createWorkflowVersionResponse(result *driver.RegisterFlueResult, buildOutput string) map[string]any {
	return map[string]any{
		"driver":            result.Driver,
		"version":           result.Version,
		"bundle":            result.Bundle,
		"created_driver":    result.CreatedDriver,
		"created_version":   result.CreatedVersion,
		"reused_version":    result.ReusedVersion,
		"activated":         result.Activated,
		"build_diagnostics": buildOutput,
	}
}

func (m *Module) buildAndRegister(ctx context.Context, ws, name, entrypoint string, files map[string]string, activate bool) (*driver.RegisterFlueResult, string, error) {
	digest := sourceDigest(files)
	return m.buildAndRegisterWithSource(ctx, ws, name, entrypoint, files, activate, "api://workflows/"+name+"/versions/"+digest, digest, "api")
}

func (m *Module) buildAndRegisterWithSource(ctx context.Context, ws, name, entrypoint string, files map[string]string, activate bool, sourceRef, digest, createdBy string) (*driver.RegisterFlueResult, string, error) {
	serverWorkDir, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("resolve server work dir: %w", err)
	}
	buildParent := filepath.Join(serverWorkDir, ".loom", "workflow-builds")
	if err := os.MkdirAll(buildParent, 0o755); err != nil {
		return nil, "", fmt.Errorf("create workflow build root: %w", err)
	}
	buildRoot, err := os.MkdirTemp(buildParent, name+"-*")
	if err != nil {
		return nil, "", fmt.Errorf("create workflow build project: %w", err)
	}
	defer os.RemoveAll(buildRoot) //nolint:errcheck
	if err := writeWorkflowBuildProject(buildRoot, files); err != nil {
		return nil, "", err
	}
	outputDir := filepath.Join(buildRoot, "dist")
	output, err := runFlueBuild(ctx, buildRoot, outputDir)
	if err != nil {
		return nil, output, err
	}
	result, err := driver.RegisterFlueDriver(ctx, m.store, driver.RegisterFlueOptions{
		WorkspaceKey: ws,
		WorkDir:      serverWorkDir,
		DistPath:     outputDir,
		DriverName:   name,
		WorkflowName: strings.TrimSuffix(filepath.Base(entrypoint), filepath.Ext(entrypoint)),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		CreatedBy:    createdBy,
		Activate:     activate,
	})
	if err != nil {
		return nil, output, err
	}
	return result, output, nil
}

func writeWorkflowBuildProject(root string, files map[string]string) error {
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk"}}`+"\n"), 0o644); err != nil {
		return fmt.Errorf("write generated package.json: %w", err)
	}
	sdkRoot, err := loomSDKRoot()
	if err != nil {
		return err
	}
	loomScope := filepath.Join(root, "node_modules", "@loom")
	if err := os.MkdirAll(loomScope, 0o755); err != nil {
		return fmt.Errorf("create generated node_modules: %w", err)
	}
	if err := os.Symlink(sdkRoot, filepath.Join(loomScope, "sdk")); err != nil {
		return fmt.Errorf("link @loom/sdk: %w", err)
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func loomSDKRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("LOOM_SDK_ROOT")); root != "" {
		return root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(cwd, "sdk")
	if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("local @loom/sdk package not found; set LOOM_SDK_ROOT")
}

func runFlueBuild(ctx context.Context, root, outputDir string) (string, error) {
	command, err := flueCommand()
	if err != nil {
		return "", err
	}
	args := append(append([]string{}, command[1:]...), "build", "--target", "node", "--root", root, "--output", outputDir)
	cmd := exec.CommandContext(ctx, command[0], args...) //nolint:gosec // command is deployment/operator configuration.
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return output, fmt.Errorf("flue build failed: %s", output)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "server.mjs")); err != nil {
		return output, fmt.Errorf("flue build missing dist/server.mjs: %w", err)
	}
	return output, nil
}

func flueCommand() ([]string, error) {
	if encoded := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			return nil, fmt.Errorf("decode LOOM_REAL_FLUE_CMD_JSON: %w", err)
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("LOOM_REAL_FLUE_CMD_JSON must contain at least one command element")
		}
		return parsed, nil
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD")); raw != "" {
		return []string{raw}, nil
	}
	path, err := exec.LookPath("flue")
	if err != nil {
		return nil, fmt.Errorf("flue not found on PATH; set LOOM_REAL_FLUE_CMD_JSON or LOOM_REAL_FLUE_CMD")
	}
	return []string{path}, nil
}

func (m *Module) createWorkflowRun(w http.ResponseWriter, r *http.Request) {
	payload, err := readRawJSONBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	driverID, err := m.resolveWorkflowDriverID(r.Context(), ws, name)
	if err != nil {
		writeDomainError(w, err, "workflow not found")
		return
	}
	run, err := driver.CreateDriverRun(r.Context(), m.store, driver.RunOptions{
		WorkspaceKey:   ws,
		DriverID:       driverID,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		SourceKind:     "api",
		SourceRef:      r.URL.Path,
		Payload:        payload,
	})
	if err != nil {
		writeDomainError(w, err, "create workflow run failed")
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (m *Module) resolveWorkflowDriverID(ctx context.Context, ws, name string) (string, error) {
	driverID, err := resolveDriverID(ctx, m.store, ws, name)
	if err == nil {
		return driverID, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	if err := m.ensureBuiltinWorkflow(ctx, ws, name); err != nil {
		return "", err
	}
	return resolveDriverID(ctx, m.store, ws, name)
}

func (m *Module) ensureBuiltinWorkflow(ctx context.Context, ws, name string) error {
	spec, ok := builtinWorkflows[name]
	if !ok {
		return domain.ErrNotFound
	}

	m.builtinMu.Lock()
	defer m.builtinMu.Unlock()

	if _, err := resolveDriverID(ctx, m.store, ws, name); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	digest := sourceDigest(spec.files)
	sourceRef := "builtin://workflows/" + name + "/versions/" + digest
	if _, _, err := m.buildAndRegisterWithSource(ctx, ws, name, spec.entrypoint, spec.files, true, sourceRef, digest, "system"); err != nil {
		return fmt.Errorf("register built-in workflow %q: %w", name, err)
	}
	return nil
}

func (m *Module) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := m.store.DriverRuns().Get(r.Context(), r.PathValue("ws"), r.PathValue("runId"))
	if err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (m *Module) getRunEvents(w http.ResponseWriter, r *http.Request) {
	page, err := m.loadRunEvents(r.Context(), r, 100)
	if err != nil {
		writeDomainError(w, err, "run events unavailable")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (m *Module) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "run event stream is unavailable for this store")
		return
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(r.Context(), ws, runID); err != nil {
		writeDomainError(w, err, "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after == "" {
		after = "0"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		page, err := reader.Events(r.Context(), ws, runID, after, 100)
		if err != nil {
			writeSSE(w, "error", map[string]string{"error": err.Error()})
			flusher.Flush()
			return
		}
		for _, event := range page.Events {
			writeSSE(w, "event", event)
		}
		if page.Cursor != "" {
			after = page.Cursor
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) loadRunEvents(ctx context.Context, r *http.Request, defaultLimit int) (*domain.PlatformEventsPage, error) {
	reader, ok := m.store.DriverRuns().(store.DriverRunEventsReader)
	if !ok {
		return nil, store.ErrDriverRunEventsUnavailable
	}
	ws := r.PathValue("ws")
	runID := r.PathValue("runId")
	if _, err := m.store.DriverRuns().Get(ctx, ws, runID); err != nil {
		return nil, err
	}
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			return nil, fmt.Errorf("invalid limit: %w", domain.ErrInvalid)
		}
		limit = parsed
	}
	return reader.Events(ctx, ws, runID, strings.TrimSpace(r.URL.Query().Get("after")), limit)
}

func readRawJSONBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRunPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("payload must be valid JSON")
	}
	out := make(json.RawMessage, len(body))
	copy(out, body)
	return out, nil
}

func resolveDriverID(ctx context.Context, st store.Store, ws, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("workflow name is required: %w", domain.ErrInvalid)
	}
	driverRecord, err := st.Drivers().Get(ctx, ws, name)
	if err == nil {
		return driverRecord.DriverID, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	drivers, err := st.Drivers().List(ctx, ws, store.DriverFilter{Name: name, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(drivers) == 0 {
		return "", domain.ErrNotFound
	}
	return drivers[0].DriverID, nil
}

func validateWorkflowEntrypoint(name, entrypoint string) error {
	want := filepath.ToSlash(filepath.Join("workflows", name+".ts"))
	if filepath.ToSlash(entrypoint) != want {
		return fmt.Errorf("entrypoint must be %s", want)
	}
	return nil
}

func validateWorkflowFiles(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for raw, content := range in {
		rel, err := validateWorkflowFilePath(raw)
		if err != nil {
			return nil, err
		}
		if strings.ContainsRune(content, '\x00') {
			return nil, fmt.Errorf("%s contains binary content", rel)
		}
		out[rel] = content
	}
	return out, nil
}

func validateWorkflowFilePath(raw string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(raw))
	if rel == "" {
		return "", fmt.Errorf("file path is required")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s must be relative", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("%s is invalid", rel)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s must not contain path traversal", rel)
		}
		if part == "node_modules" {
			return "", fmt.Errorf("%s must not include node_modules", rel)
		}
	}
	switch filepath.Base(clean) {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb":
		return "", fmt.Errorf("%s is not allowed in workflow source uploads", clean)
	}
	return clean, nil
}

func sourceDigest(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write([]byte(files[key]))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func writeSSE(w io.Writer, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeDomainError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrDriverRunEventsUnavailable):
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fallback)
	}
}
