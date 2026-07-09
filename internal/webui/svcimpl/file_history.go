package svcimpl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	fileHistoryGitLogLimit = 100
	fileHistorySaveLimit   = 20
	fileBlameMaxLines      = 5000
)

type fileCheckoutRef struct {
	root    string
	relPath string
}

type fileSaveSnapshot struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	Target    string `json:"target,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Time      string `json:"time"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

var errNoSaveSnapshot = errors.New("no save snapshot")

func (s *fileServiceImpl) ReadFileAtRevScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path, rev string) (*service.FileReadResult, error) {
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result, err := s.fileOps.GitShowFileAtRev(checkout.root, strings.TrimSpace(rev), checkout.relPath, maxRequestBody)
	if err != nil {
		return nil, service.ErrInternal("failed to read file at revision", err)
	}
	if misc.IsBinaryContent(result.Content) {
		return &service.FileReadResult{Path: cleanPath, Size: result.Size, Binary: true, Truncated: result.Truncated}, nil
	}
	return &service.FileReadResult{
		Path:      cleanPath,
		Content:   string(result.Content),
		Size:      result.Size,
		Truncated: result.Truncated,
	}, nil
}

func (s *fileServiceImpl) DiffFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path, from, to string) (*service.FileDiffResult, error) {
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	patch, err := s.fileOps.GitDiffFile(checkout.root, checkout.relPath, from, to)
	if err != nil {
		return nil, service.ErrInternal("failed to run git diff", err)
	}
	return &service.FileDiffResult{Path: cleanPath, Patch: patch}, nil
}

func (s *fileServiceImpl) BlameFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileBlameResult, error) {
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, fi, truncated, err := root.store.Read(cleanPath, maxRequestBody)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxRequestBody {
		return blameSkipped(cleanPath, "too_large", "File is over the 1 MB blame limit."), nil
	}
	if truncated {
		return blameSkipped(cleanPath, "too_large", "File is over the 1 MB blame limit."), nil
	}
	if misc.IsBinaryContent(data) {
		return blameSkipped(cleanPath, "binary", "Binary files cannot be blamed."), nil
	}
	if countLines(string(data)) > fileBlameMaxLines {
		return blameSkipped(cleanPath, "too_many_lines", "File is over the 5000 line blame limit."), nil
	}
	output, err := s.fileOps.GitBlamePorcelain(checkout.root, checkout.relPath)
	if err != nil {
		return nil, service.ErrInternal("failed to run git blame", err)
	}
	return &service.FileBlameResult{
		Path:  cleanPath,
		Lines: parseBlamePorcelain(output),
	}, nil
}

func (s *fileServiceImpl) HistoryFileScoped(_ context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileHistoryResult, error) {
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	logOutput, err := s.fileOps.GitLogFile(checkout.root, checkout.relPath, fileHistoryGitLogLimit)
	if err != nil {
		return nil, service.ErrInternal("failed to run git log", err)
	}
	entries := parseGitLogHistory(logOutput)
	saves, err := s.loadSaveHistory(wsID, scope, target, repo, cleanPath)
	if err != nil {
		return nil, err
	}
	entries = append(entries, saves...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Time > entries[j].Time
	})
	return &service.FileHistoryResult{Path: cleanPath, Entries: entries}, nil
}

func blameSkipped(path, reason, message string) *service.FileBlameResult {
	return &service.FileBlameResult{
		Path:    path,
		Skipped: true,
		Reason:  reason,
		Message: message,
		Lines:   []service.FileBlameLine{},
	}
}

func (s *fileServiceImpl) resolveScopedContainingCheckout(wsID string, scope service.FileScope, target, repo, path string) (*scopedRoot, string, fileCheckoutRef, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, "", fileCheckoutRef{}, err
	}
	cleanPath, err := normalizeFilePath(path, false)
	if err != nil {
		root.Close()
		return nil, "", fileCheckoutRef{}, err
	}
	if err := root.store.ensureNoSymlinks(cleanPath, true); err != nil {
		root.Close()
		return nil, "", fileCheckoutRef{}, err
	}
	var checkout fileCheckoutRef
	switch scope {
	case service.ScopeRepo, service.ScopeAgent:
		if err := validateGitCheckoutRoot(root.path); err != nil {
			root.Close()
			return nil, "", fileCheckoutRef{}, err
		}
		repoRel, err := filepath.Rel(root.path, root.store.absolute(cleanPath))
		if err != nil || repoRel == "." || strings.HasPrefix(repoRel, ".."+string(filepath.Separator)) || filepath.IsAbs(repoRel) {
			root.Close()
			return nil, "", fileCheckoutRef{}, service.ErrForbidden("path outside git checkout")
		}
		checkout = fileCheckoutRef{root: root.path, relPath: filepath.ToSlash(repoRel)}
	case service.ScopeWorkspace:
		checkout, err = resolveContainingCheckout(root, cleanPath)
		if err != nil {
			root.Close()
			return nil, "", fileCheckoutRef{}, err
		}
	default:
		root.Close()
		return nil, "", fileCheckoutRef{}, service.ErrValidation("unsupported scope " + string(scope))
	}
	return root, cleanPath, checkout, nil
}

func resolveContainingCheckout(root *scopedRoot, relPath string) (fileCheckoutRef, error) {
	start := relPath
	if fi, err := root.store.stat(relPath); err != nil || !fi.IsDir() {
		start = filepath.Dir(relPath)
	}
	for current := start; ; current = filepath.Dir(current) {
		currentAbs := root.store.absolute(current)
		if err := validateGitCheckoutRoot(currentAbs); err == nil {
			repoRel, relErr := filepath.Rel(currentAbs, root.store.absolute(relPath))
			if relErr != nil || repoRel == "." || strings.HasPrefix(repoRel, ".."+string(filepath.Separator)) || filepath.IsAbs(repoRel) {
				return fileCheckoutRef{}, service.ErrForbidden("path outside git checkout")
			}
			return fileCheckoutRef{root: currentAbs, relPath: filepath.ToSlash(repoRel)}, nil
		}
		if current == "." || filepath.Dir(current) == current {
			break
		}
	}
	return fileCheckoutRef{}, service.ErrNotFound("containing git checkout not found")
}

func parseGitLogHistory(output string) []service.FileHistoryEntry {
	records := strings.Split(output, "\x1e")
	entries := make([]service.FileHistoryEntry, 0, len(records))
	for _, record := range records {
		record = strings.Trim(record, "\r\n")
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, service.FileHistoryEntry{
			Kind:    "commit",
			SHA:     parts[0],
			Author:  parts[1],
			Time:    time.Unix(ts, 0).UTC().Format(time.RFC3339),
			Summary: parts[3],
		})
	}
	return entries
}

type blameCommitInfo struct {
	author  string
	time    string
	summary string
}

type blameHeader struct {
	sha       string
	finalLine int
	lineCount int
}

func parseBlamePorcelain(output string) []service.FileBlameLine {
	lines := strings.Split(output, "\n")
	entries := make([]service.FileBlameLine, 0)
	seen := make(map[string]blameCommitInfo)
	for i := 0; i < len(lines); {
		line := strings.TrimRight(lines[i], "\r")
		if line == "" || strings.HasPrefix(line, "\t") {
			i++
			continue
		}
		header, ok := parseBlameHeader(line)
		if !ok {
			i++
			continue
		}
		info, next := parseBlameMetadata(lines, i+1, seen[header.sha])
		i = skipBlameContent(lines, next)
		seen[header.sha] = info
		entries = append(entries, service.FileBlameLine{
			Line:    header.finalLine,
			Lines:   header.lineCount,
			SHA:     header.sha,
			Author:  info.author,
			Time:    info.time,
			Summary: info.summary,
		})
	}
	return entries
}

func parseBlameHeader(line string) (blameHeader, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 || len(fields[0]) < 4 {
		return blameHeader{}, false
	}
	finalLine, err := strconv.Atoi(fields[2])
	if err != nil {
		return blameHeader{}, false
	}
	lineCount := 1
	if len(fields) >= 4 {
		if n, err := strconv.Atoi(fields[3]); err == nil && n > 0 {
			lineCount = n
		}
	}
	return blameHeader{sha: fields[0], finalLine: finalLine, lineCount: lineCount}, true
}

func parseBlameMetadata(lines []string, start int, info blameCommitInfo) (blameCommitInfo, int) {
	i := start
	for i < len(lines) {
		meta := strings.TrimRight(lines[i], "\r")
		if meta == "" || strings.HasPrefix(meta, "\t") {
			break
		}
		applyBlameMetadata(&info, meta)
		i++
	}
	return info, i
}

func applyBlameMetadata(info *blameCommitInfo, meta string) {
	key, value, ok := strings.Cut(meta, " ")
	if !ok {
		return
	}
	switch key {
	case "author":
		info.author = value
	case "author-time":
		if ts, err := strconv.ParseInt(value, 10, 64); err == nil {
			info.time = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
	case "summary":
		info.summary = value
	}
}

func skipBlameContent(lines []string, start int) int {
	i := start
	for i < len(lines) && strings.HasPrefix(lines[i], "\t") {
		i++
	}
	return i
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	count := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}

func (s *fileServiceImpl) snapshotBeforeOverwrite(wsID string, scope service.FileScope, target, repo string, store *rootedFileStore, path string) error {
	snapshot, err := readSaveSnapshotCandidate(store, path)
	if errors.Is(err, errNoSaveSnapshot) {
		return nil
	}
	if err != nil {
		return err
	}
	cleanPath := path
	dataDir, err := s.fileOps.ResolveLoomDataDir()
	if err != nil {
		return service.ErrInternal("failed to resolve loom data directory", err)
	}
	dir := saveHistoryDir(dataDir, wsID, scope, target, repo, cleanPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return service.ErrInternal("failed to create file history directory", err)
	}
	snapshot.ID = time.Now().UTC().Format("20060102T150405.000000000Z")
	snapshot.Path = cleanPath
	snapshot.Scope = string(scope)
	snapshot.Target = target
	snapshot.Repo = repo
	snapshot.Time = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return service.ErrInternal("failed to encode file history snapshot", err)
	}
	if err := os.WriteFile(filepath.Join(dir, snapshot.ID+".json"), data, 0600); err != nil {
		return service.ErrInternal("failed to write file history snapshot", err)
	}
	return pruneSaveHistory(dir)
}

func readSaveSnapshotCandidate(store *rootedFileStore, path string) (*fileSaveSnapshot, error) {
	data, fi, truncated, err := store.Read(path, maxRequestBody)
	if err != nil {
		var serviceErr *service.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Kind == service.KindNotFound {
			return nil, errNoSaveSnapshot
		}
		return nil, err
	}
	return &fileSaveSnapshot{
		Content:   string(data),
		Size:      fi.Size(),
		Binary:    misc.IsBinaryContent(data),
		Truncated: truncated,
	}, nil
}

func (s *fileServiceImpl) loadSaveHistory(wsID string, scope service.FileScope, target, repo, path string) ([]service.FileHistoryEntry, error) {
	dataDir, err := s.fileOps.ResolveLoomDataDir()
	if err != nil {
		return nil, service.ErrInternal("failed to resolve loom data directory", err)
	}
	dir := saveHistoryDir(dataDir, wsID, scope, target, repo, path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, service.ErrInternal("failed to read file history", err)
	}
	out := make([]service.FileHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		snapshotPath, ok := saveHistorySnapshotPath(dir, entry.Name())
		if !ok {
			continue
		}
		// #nosec G304 -- snapshot names come from os.ReadDir on the scoped
		// history directory and are basename-validated before joining.
		data, err := os.ReadFile(snapshotPath)
		if err != nil {
			return nil, service.ErrInternal("failed to read file history snapshot", err)
		}
		var snapshot fileSaveSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			continue
		}
		item := service.FileHistoryEntry{
			Kind:      "save",
			ID:        snapshot.ID,
			Author:    "browser",
			Time:      snapshot.Time,
			Summary:   "Browser save",
			Size:      snapshot.Size,
			Binary:    snapshot.Binary,
			Truncated: snapshot.Truncated,
		}
		if !snapshot.Binary && !snapshot.Truncated {
			item.Content = snapshot.Content
		}
		out = append(out, item)
	}
	return out, nil
}

func saveHistoryDir(dataDir, wsID string, scope service.FileScope, target, repo, path string) string {
	return filepath.Join(
		dataDir,
		"file-history",
		safeHistorySegment(wsID),
		safeHistorySegment(string(scope)),
		safeHistorySegment(saveHistoryTargetKey(scope, target, repo)),
		fileHistoryPathKey(path),
	)
}

func saveHistoryTargetKey(scope service.FileScope, target, repo string) string {
	if scope == service.ScopeAgent && strings.TrimSpace(repo) != "" {
		return target + "@" + strings.TrimSpace(repo)
	}
	return target
}

func safeHistorySegment(value string) string {
	if strings.TrimSpace(value) == "" {
		return "_"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(value)
}

func fileHistoryPathKey(path string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(path)))
}

func saveHistorySnapshotPath(dir, name string) (string, bool) {
	if name == "" || name != filepath.Base(name) || filepath.Ext(name) != ".json" {
		return "", false
	}
	if strings.ContainsAny(name, `/\:`) {
		return "", false
	}
	return filepath.Join(dir, name), true
}

func pruneSaveHistory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return service.ErrInternal("failed to list file history snapshots", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if path, ok := saveHistorySnapshotPath(dir, entry.Name()); ok {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	for len(files) > fileHistorySaveLimit {
		if err := os.Remove(files[0]); err != nil {
			return service.ErrInternal("failed to prune file history snapshot", err)
		}
		files = files[1:]
	}
	return nil
}
