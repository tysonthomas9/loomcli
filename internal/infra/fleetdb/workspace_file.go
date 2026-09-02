package fleetdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workspaceFileStore struct{ client *Client }

var _ store.WorkspaceFileStore = (*workspaceFileStore)(nil)

const (
	pendingTreePollInterval = 25 * time.Millisecond
	pendingTreeWaitTimeout  = 10 * time.Second
)

type pendingTreeWaitPolicy struct {
	PollInterval time.Duration
	Timeout      time.Duration
}

// These wire types are temporary local mirrors of FleetDB's workspace-file
// JSON contract. Keep them private so the later generated-client cutover can
// replace this file without exposing transport details to callers.
type workspaceFileSpecWire struct {
	Path           string `json:"path"`
	ContentHash    string `json:"content_hash"`
	ChecksumCRC32C string `json:"checksum_crc32c"`
	SizeBytes      int64  `json:"size_bytes"`
	MediaType      string `json:"media_type,omitempty"`
	Executable     bool   `json:"executable,omitempty"`
}

type workspaceFileTransferGrantWire struct {
	UploadToken string              `json:"upload_token,omitempty"`
	Method      string              `json:"method"`
	URL         string              `json:"url"`
	Headers     map[string][]string `json:"headers,omitempty"`
	ExpiresAt   time.Time           `json:"expires_at"`
}

type workspaceFileUploadGrantWire struct {
	Path     string                         `json:"path"`
	Transfer workspaceFileTransferGrantWire `json:"transfer"`
}

type workspaceFileUploadSessionWire struct {
	ID        string                         `json:"id"`
	Revision  string                         `json:"revision"`
	Files     []workspaceFileUploadGrantWire `json:"files"`
	ExpiresAt time.Time                      `json:"expires_at"`
}

type workspaceFileWire struct {
	Path        string `json:"path"`
	BlobRef     string `json:"blob_ref"`
	ContentHash string `json:"content_hash"`
	SizeBytes   int64  `json:"size_bytes"`
	MediaType   string `json:"media_type,omitempty"`
	Executable  bool   `json:"executable,omitempty"`
	Revision    string `json:"revision"`
}

type workspaceFileTreeWire struct {
	WorkspaceKey string              `json:"workspace_key"`
	Revision     string              `json:"revision"`
	Files        []workspaceFileWire `json:"files"`
	CreatedBy    string              `json:"created_by"`
	CreatedAt    time.Time           `json:"created_at"`
}

//nolint:funlen // The upload grants and one manifest publication form one store operation.
func (s *workspaceFileStore) Publish(ctx context.Context, workspaceKey string, files []domain.WorkspaceFileInput) (*domain.WorkspaceFileTreePublishResult, error) {
	canonical, err := canonicalWorkspaceFileInputs(files)
	if err != nil {
		return nil, err
	}
	specs := make([]workspaceFileSpecWire, 0, len(canonical))
	for _, file := range canonical {
		digest := workspaceFileDigest(file.Bytes)
		specs = append(specs, workspaceFileSpecWire{
			Path: file.Path, ContentHash: digest, ChecksumCRC32C: workspaceFileCRC32C(file.Bytes),
			SizeBytes: int64(len(file.Bytes)), MediaType: file.MediaType, Executable: file.Executable,
		})
	}
	var session workspaceFileUploadSessionWire
	path := workspaceFileAPIPath(workspaceKey) + "/file-tree-uploads"
	if _, _, err := s.client.doWithResponse(ctx, http.MethodPost, path, struct {
		Files []workspaceFileSpecWire `json:"files"`
	}{Files: specs}, &session, nil); err != nil {
		return nil, fmt.Errorf("begin workspace file tree upload: %w", err)
	}
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Revision) == "" {
		return nil, fmt.Errorf("begin workspace file tree upload returned an invalid session: %w", domain.ErrIntegrity)
	}
	if len(session.Files) != len(canonical) {
		return nil, fmt.Errorf("begin workspace file tree upload returned %d grants, want %d: %w", len(session.Files), len(canonical), domain.ErrIntegrity)
	}
	byPath := make(map[string]domain.WorkspaceFileInput, len(canonical))
	for _, file := range canonical {
		byPath[file.Path] = file
	}
	seen := make(map[string]struct{}, len(session.Files))
	for _, upload := range session.Files {
		if _, duplicate := seen[upload.Path]; duplicate {
			return nil, fmt.Errorf("duplicate workspace file upload grant path %q: %w", upload.Path, domain.ErrIntegrity)
		}
		seen[upload.Path] = struct{}{}
		file, ok := byPath[upload.Path]
		if !ok {
			return nil, fmt.Errorf("unexpected workspace file upload grant path %q: %w", upload.Path, domain.ErrIntegrity)
		}
		if err := validateWorkspaceFileGrant(&upload.Transfer, http.MethodPut, time.Now(), s.client.workspaceFileGrantMaxTTL); err != nil {
			return nil, fmt.Errorf("begin workspace file upload %q: %w", file.Path, err)
		}
	}
	for _, upload := range session.Files {
		file := byPath[upload.Path]
		if err := s.client.executeWorkspaceFileTransfer(ctx, &upload.Transfer, bytes.NewReader(file.Bytes)); err != nil {
			return nil, fmt.Errorf("upload workspace file %q: %w", file.Path, err)
		}
		completePath := path + "/" + pathEscape(session.ID) + "/complete/" + escapeWorkspaceFilePath(file.Path)
		if err := s.client.do(ctx, http.MethodPost, completePath, nil, nil); err != nil {
			return nil, fmt.Errorf("complete workspace file upload %q: %w", file.Path, err)
		}
	}
	var tree workspaceFileTreeWire
	publishPath := path + "/" + pathEscape(session.ID) + "/publish"
	statusCode, headers, err := s.client.doWithResponse(ctx, http.MethodPost, publishPath, nil, &tree, nil)
	// Publication is idempotent by upload-session ID. A transport failure or a
	// successful response whose body was lost is ambiguous, so replay exactly
	// that request once instead of starting a new upload session.
	if (statusCode == 0 && err != nil) || (statusCode >= 200 && statusCode < 300 && (err != nil || tree.Revision == "")) {
		tree = workspaceFileTreeWire{}
		statusCode, headers, err = s.client.doWithResponse(ctx, http.MethodPost, publishPath, nil, &tree, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("publish workspace file tree: %w", err)
	}
	var status domain.WorkspaceFileTreeStatus
	switch statusCode {
	case http.StatusOK:
		status = domain.WorkspaceFileTreeExisting
	case http.StatusCreated:
		status = domain.WorkspaceFileTreePublished
	case http.StatusAccepted:
		status = domain.WorkspaceFileTreeProjectionPending
	default:
		return nil, fmt.Errorf("publish workspace file tree: unexpected HTTP status %d", statusCode)
	}
	if tree.WorkspaceKey != workspaceKey {
		return nil, fmt.Errorf("publish workspace file tree returned workspace %q, want %q: %w", tree.WorkspaceKey, workspaceKey, domain.ErrIntegrity)
	}
	if tree.Revision != session.Revision {
		return nil, fmt.Errorf("publish workspace file tree returned revision %q, want session revision %q: %w", tree.Revision, session.Revision, domain.ErrIntegrity)
	}
	domainTree, err := workspaceFileTreeFromWire(&tree)
	if err != nil {
		return nil, fmt.Errorf("publish workspace file tree returned invalid tree: %w", err)
	}
	if status == domain.WorkspaceFileTreeProjectionPending {
		domainTree, err = s.waitForTree(ctx, workspaceKey, domainTree.Revision)
		if err != nil {
			return nil, fmt.Errorf("wait for published workspace file tree: %w", err)
		}
		status = domain.WorkspaceFileTreePublished
	}
	return &domain.WorkspaceFileTreePublishResult{
		Tree: domainTree, Status: status, ETag: headers.Get("ETag"), Location: headers.Get("Location"),
	}, nil
}

func workspaceFileCRC32C(body []byte) string {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	return base64.StdEncoding.EncodeToString(encoded[:])
}

func (s *workspaceFileStore) waitForTree(ctx context.Context, workspaceKey, revision string) (*domain.WorkspaceFileTree, error) {
	return s.waitForTreeWithPolicy(ctx, workspaceKey, revision, pendingTreeWaitPolicy{
		PollInterval: pendingTreePollInterval,
		Timeout:      pendingTreeWaitTimeout,
	})
}

func (s *workspaceFileStore) waitForTreeWithPolicy(ctx context.Context, workspaceKey, revision string, policy pendingTreeWaitPolicy) (*domain.WorkspaceFileTree, error) {
	waitCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()
	for {
		tree, err := s.GetTree(waitCtx, workspaceKey, revision)
		if err == nil {
			return tree, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		timer := time.NewTimer(policy.PollInterval)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func (s *workspaceFileStore) GetTree(ctx context.Context, workspaceKey, revision string) (*domain.WorkspaceFileTree, error) {
	path := workspaceFileCollectionPath(workspaceKey) + "/" + pathEscape(revision)
	var tree workspaceFileTreeWire
	if err := s.client.do(ctx, http.MethodGet, path, nil, &tree); err != nil {
		return nil, fmt.Errorf("get workspace file tree: %w", err)
	}
	if tree.WorkspaceKey != workspaceKey || tree.Revision != revision {
		return nil, fmt.Errorf("get workspace file tree returned identity %q/%q, want %q/%q: %w", tree.WorkspaceKey, tree.Revision, workspaceKey, revision, domain.ErrIntegrity)
	}
	return workspaceFileTreeFromWire(&tree)
}

func (s *workspaceFileStore) Stat(ctx context.Context, workspaceKey, revision, filePath string) (*domain.WorkspaceFile, error) {
	path := workspaceFileCollectionPath(workspaceKey) + "/" + pathEscape(revision) + "/files/" + escapeWorkspaceFilePath(filePath)
	var file workspaceFileWire
	if err := s.client.do(ctx, http.MethodGet, path, nil, &file); err != nil {
		return nil, fmt.Errorf("stat workspace file: %w", err)
	}
	if file.Path != filePath {
		return nil, fmt.Errorf("stat workspace file returned path %q, want %q: %w", file.Path, filePath, domain.ErrIntegrity)
	}
	out := workspaceFileFromWire(file)
	if _, err := domain.CanonicalWorkspaceFileManifest([]domain.WorkspaceFile{out}); err != nil {
		return nil, fmt.Errorf("stat workspace file returned invalid metadata: %w", err)
	}
	return &out, nil
}

func (s *workspaceFileStore) Download(ctx context.Context, workspaceKey, revision, filePath string) ([]byte, error) {
	file, err := s.Stat(ctx, workspaceKey, revision, filePath)
	if err != nil {
		return nil, err
	}
	if file.SizeBytes < 0 || file.SizeBytes == math.MaxInt64 {
		return nil, fmt.Errorf("workspace file %q has invalid declared size %d: %w", filePath, file.SizeBytes, domain.ErrIntegrity)
	}
	grant, err := s.resolveDownload(ctx, workspaceKey, revision, filePath)
	if err != nil {
		return nil, err
	}
	body, err := s.client.openWorkspaceFileDownload(ctx, grant)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(body, file.SizeBytes+1))
	closeErr := body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read workspace file download: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close workspace file download: %w", closeErr)
	}
	if int64(len(content)) != file.SizeBytes {
		return nil, fmt.Errorf("workspace file %q size is %d, want %d: %w", filePath, len(content), file.SizeBytes, domain.ErrIntegrity)
	}
	if got := workspaceFileDigest(content); got != file.ContentHash {
		return nil, fmt.Errorf("workspace file %q hash is %q, want %q: %w", filePath, got, file.ContentHash, domain.ErrIntegrity)
	}
	return content, nil
}

func (s *workspaceFileStore) resolveDownload(ctx context.Context, workspaceKey, revision, filePath string) (*workspaceFileTransferGrantWire, error) {
	path := workspaceFileCollectionPath(workspaceKey) + "/" + pathEscape(revision) + "/downloads/" + escapeWorkspaceFilePath(filePath)
	var grant workspaceFileTransferGrantWire
	if err := s.client.do(ctx, http.MethodPost, path, nil, &grant); err != nil {
		return nil, fmt.Errorf("resolve workspace file download: %w", err)
	}
	if err := validateWorkspaceFileGrant(&grant, http.MethodGet, time.Now(), s.client.workspaceFileGrantMaxTTL); err != nil {
		return nil, fmt.Errorf("resolve workspace file download: %w", err)
	}
	return &grant, nil
}

func (c *Client) executeWorkspaceFileTransfer(ctx context.Context, grant *workspaceFileTransferGrantWire, body io.Reader) error {
	resp, local, err := c.workspaceFileTransfer(ctx, grant, body)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return workspaceFileTransferStatusError(grant.Method, grant.URL, local, resp)
}

func (c *Client) openWorkspaceFileDownload(ctx context.Context, grant *workspaceFileTransferGrantWire) (io.ReadCloser, error) {
	resp, local, err := c.workspaceFileTransfer(ctx, grant, nil)
	if err != nil {
		return nil, err
	}
	if err := workspaceFileTransferStatusError(grant.Method, grant.URL, local, resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) workspaceFileTransfer(ctx context.Context, grant *workspaceFileTransferGrantWire, body io.Reader) (*http.Response, bool, error) {
	if err := validateWorkspaceFileGrant(grant, "", time.Now(), c.workspaceFileGrantMaxTTL); err != nil {
		return nil, false, err
	}
	target := grant.URL
	local := strings.HasPrefix(target, "/")
	if local {
		target = c.baseURL + target
	}
	req, err := http.NewRequestWithContext(ctx, grant.Method, target, body)
	if err != nil {
		return nil, local, fmt.Errorf("build workspace file transfer request: %w", err)
	}
	for key, values := range grant.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if local {
		c.mu.RLock()
		auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
		c.mu.RUnlock()
		auth.Apply(req)
	}
	httpClient := *c.http
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, local, fmt.Errorf("execute workspace file transfer: %w", err)
	}
	return resp, local, nil
}

func workspaceFileTransferStatusError(method, target string, local bool, resp *http.Response) error {
	target = redactedWorkspaceFileTransferTarget(target)
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	// Create-only object grants report an existing content-addressed object as
	// 412. Completion still asks Fleet to read and verify those exact bytes.
	if method == http.MethodPut && resp.StatusCode == http.StatusPreconditionFailed {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if readErr != nil {
		return fmt.Errorf("workspace file transfer %s %s: HTTP %d (read body: %w)", method, target, resp.StatusCode, readErr)
	}
	if local {
		return classifyHTTPError(method, target, resp.StatusCode, body)
	}
	// Provider response bodies are untrusted and may echo signed header or
	// query values. Keep external transfer errors deliberately body-free so
	// callers can safely log them without disclosing capabilities.
	return fmt.Errorf("workspace file provider transfer %s %s: HTTP %d", method, target, resp.StatusCode)
}

func validateWorkspaceFileGrant(grant *workspaceFileTransferGrantWire, expectedMethod string, now time.Time, maxTTL time.Duration) error {
	if grant == nil {
		return fmt.Errorf("workspace file transfer grant is required")
	}
	if strings.TrimSpace(grant.Method) == "" || strings.TrimSpace(grant.URL) == "" {
		return fmt.Errorf("workspace file transfer grant requires method and URL")
	}
	if expectedMethod != "" && grant.Method != expectedMethod {
		return fmt.Errorf("workspace file transfer grant method is %q, want %q", grant.Method, expectedMethod)
	}
	if err := validateWorkspaceFileGrantExpiry(grant.ExpiresAt, now, maxTTL); err != nil {
		return err
	}
	parsed, err := url.Parse(grant.URL)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("workspace file transfer grant URL is invalid")
	}
	if strings.HasPrefix(grant.URL, "/") {
		if parsed.IsAbs() || strings.HasPrefix(grant.URL, "//") {
			return fmt.Errorf("workspace file transfer grant URL is invalid")
		}
		return nil
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("workspace file transfer grant URL must be absolute or root-relative")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackWorkspaceFileHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("workspace file transfer grant URL must use HTTPS (HTTP is allowed only for loopback test providers)")
}

func validateWorkspaceFileGrantExpiry(expiresAt, now time.Time, maxTTL time.Duration) error {
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return fmt.Errorf("workspace file transfer grant is expired")
	}
	if maxTTL <= 0 || expiresAt.After(now.Add(maxTTL)) {
		return fmt.Errorf("workspace file transfer grant expiry exceeds maximum TTL %s", maxTTL)
	}
	return nil
}

func isLoopbackWorkspaceFileHost(host string) bool {
	address := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || address != nil && address.IsLoopback()
}

func redactedWorkspaceFileTransferTarget(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return "[invalid transfer URL]"
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.User = nil
	return parsed.String()
}

func canonicalWorkspaceFileInputs(files []domain.WorkspaceFileInput) ([]domain.WorkspaceFileInput, error) {
	preflight := make([]domain.WorkspaceFile, len(files))
	for i, file := range files {
		preflight[i] = domain.WorkspaceFile{
			Path: file.Path, BlobRef: "preflight", ContentHash: workspaceFileDigest(file.Bytes),
			SizeBytes: int64(len(file.Bytes)), MediaType: file.MediaType, Executable: file.Executable,
		}
	}
	if _, err := domain.CanonicalWorkspaceFileManifest(preflight); err != nil {
		return nil, err
	}
	canonical := append([]domain.WorkspaceFileInput(nil), files...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Path < canonical[j].Path })
	return canonical, nil
}

func workspaceFileDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func workspaceFileTreeFromWire(tree *workspaceFileTreeWire) (*domain.WorkspaceFileTree, error) {
	if tree == nil {
		return nil, fmt.Errorf("workspace file tree response is required")
	}
	if strings.TrimSpace(tree.WorkspaceKey) == "" || strings.TrimSpace(tree.Revision) == "" {
		return nil, fmt.Errorf("workspace file tree response requires workspace and revision: %w", domain.ErrIntegrity)
	}
	out := &domain.WorkspaceFileTree{
		WorkspaceKey: tree.WorkspaceKey, Revision: tree.Revision, CreatedBy: tree.CreatedBy,
		CreatedAt: tree.CreatedAt, Files: make([]domain.WorkspaceFile, len(tree.Files)),
	}
	for i, file := range tree.Files {
		out.Files[i] = workspaceFileFromWire(file)
	}
	if _, err := domain.CanonicalWorkspaceFileManifest(out.Files); err != nil {
		return nil, fmt.Errorf("invalid workspace file manifest: %w", err)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

func workspaceFileFromWire(file workspaceFileWire) domain.WorkspaceFile {
	return domain.WorkspaceFile{
		Path: file.Path, BlobRef: file.BlobRef, ContentHash: file.ContentHash,
		SizeBytes: file.SizeBytes, MediaType: file.MediaType, Executable: file.Executable, Revision: file.Revision,
	}
}

func workspaceFileAPIPath(workspaceKey string) string {
	return "/api/v1/" + pathEscape(workspaceKey)
}

func workspaceFileCollectionPath(workspaceKey string) string {
	return workspaceFileAPIPath(workspaceKey) + "/file-trees"
}

func escapeWorkspaceFilePath(filePath string) string {
	parts := strings.Split(filePath, "/")
	for i := range parts {
		parts[i] = pathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
