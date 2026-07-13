package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	fileHistoryGitLogLimit = 100
	fileBlameMaxLines      = 5000
)

type fileCheckoutRef struct {
	root    string
	relPath string
}

func (s *fileServiceImpl) ReadFileAtRevScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path, rev string) (*service.FileReadResult, error) {
	if err := requireSensitiveFileAccess(ctx, path); err != nil {
		return nil, err
	}
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result, err := s.fileOps.GitShowFileAtRev(ctx, checkout.root, strings.TrimSpace(rev), checkout.relPath, maxRequestBody)
	if err != nil {
		return nil, mapGitInspectionError("failed to read file at revision", err)
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

func (s *fileServiceImpl) DiffFileScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path, from, to string) (*service.FileDiffResult, error) {
	if err := requireSensitiveFileAccess(ctx, path); err != nil {
		return nil, err
	}
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	patch, err := s.fileOps.GitDiffFile(ctx, checkout.root, checkout.relPath, from, to)
	if err != nil {
		return nil, mapGitInspectionError("failed to run git diff", err)
	}
	return &service.FileDiffResult{Path: cleanPath, Patch: patch.Output, Partial: patch.Partial, LimitHit: patch.LimitHit}, nil
}

func (s *fileServiceImpl) BlameFileScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileBlameResult, error) {
	if err := requireSensitiveFileAccess(ctx, path); err != nil {
		return nil, err
	}
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
	output, err := s.fileOps.GitBlamePorcelain(ctx, checkout.root, checkout.relPath)
	if err != nil {
		return nil, mapGitInspectionError("failed to run git blame", err)
	}
	return &service.FileBlameResult{
		Path:  cleanPath,
		Lines: parseBlamePorcelain(output.Output), Partial: output.Partial, LimitHit: output.LimitHit,
	}, nil
}

func (s *fileServiceImpl) HistoryFileScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo, path string) (*service.FileHistoryResult, error) {
	if err := requireSensitiveFileAccess(ctx, path); err != nil {
		return nil, err
	}
	root, cleanPath, checkout, err := s.resolveScopedContainingCheckout(wsID, scope, target, repo, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := s.ensureLegacyHistoryCleaned(); err != nil {
		return nil, err
	}
	logOutput, err := s.fileOps.GitLogFile(ctx, checkout.root, checkout.relPath, fileHistoryGitLogLimit)
	if err != nil {
		return nil, mapGitInspectionError("failed to run git log", err)
	}
	entries := parseGitLogHistory(logOutput.Output)
	return &service.FileHistoryResult{Path: cleanPath, Entries: entries, Partial: logOutput.Partial, LimitHit: logOutput.LimitHit}, nil
}

type inspectionKindError interface {
	InspectionKind() string
}

func mapGitInspectionError(operation string, err error) error {
	var inspectionErr inspectionKindError
	if errors.As(err, &inspectionErr) {
		switch inspectionErr.InspectionKind() {
		case "timeout", "canceled":
			return service.ErrTimeout(operation)
		case "validation":
			return service.ErrValidation(err.Error())
		case "not_found":
			return service.ErrNotFound(operation)
		}
	}
	return service.ErrInternal(operation, err)
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
	fields := strings.Split(output, "\x00")
	entries := make([]service.FileHistoryEntry, 0, len(fields)/4)
	for len(fields) >= 4 {
		parts := fields[:4]
		fields = fields[4:]
		parts[0] = strings.TrimLeft(parts[0], "\r\n")
		ts, err := strconv.ParseInt(parts[2], 10, 64)
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

const legacySaveHistoryDirName = "file-history"

func (s *fileServiceImpl) ensureLegacyHistoryCleaned() error {
	s.historyCleanupOnce.Do(func() {
		dataDir, err := s.fileOps.ResolveLoomDataDir()
		if err != nil {
			s.historyCleanupErr = service.ErrInternal("failed to resolve loom data directory", err)
			return
		}
		s.historyCleanupErr = cleanupLegacySaveHistory(dataDir)
	})
	return s.historyCleanupErr
}

// cleanupLegacySaveHistory removes only the dedicated plaintext snapshot root.
// Refusing a symlink keeps this one-time migration from following an alias.
func cleanupLegacySaveHistory(dataDir string) error {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return service.ErrInternal("failed to resolve legacy file history root", err)
	}
	target := filepath.Join(root, legacySaveHistoryDirName)
	if filepath.Dir(target) != root || filepath.Base(target) != legacySaveHistoryDirName {
		return service.ErrInternal("invalid legacy file history root", nil)
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return service.ErrInternal("failed to inspect legacy file history root", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("legacy file history root is a symlink")
	}
	if !info.IsDir() {
		return service.ErrInternal("legacy file history root is not a directory", nil)
	}
	if err := os.RemoveAll(target); err != nil {
		return service.ErrInternal("failed to remove legacy file history", err)
	}
	return nil
}
