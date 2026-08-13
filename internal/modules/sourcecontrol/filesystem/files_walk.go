package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// File Browser v2 navigation caps. Every capped endpoint surfaces a response
// flag when these values clip the walk, scan, or result set.
var (
	fileIndexMaxEntries = 50_000
	fileIndexWalkBudget = 2 * time.Second
	fileIndexCacheTTL   = 10 * time.Second

	fileSearchMaxFiles     = 10_000
	fileSearchMaxBytes     = int64(32 << 20)
	fileSearchMaxFileBytes = int64(1 << 20)
	fileSearchMaxMatches   = 5_000
	fileSearchWalkBudget   = 5 * time.Second
)

var errBoundedWalkLimit = errors.New("bounded file walk limit reached")

type boundedFileWalkOptions struct {
	maxFiles           int
	timeBudget         time.Duration
	excludeNodeModules bool
	includePatterns    []string
	excludePatterns    []string
	allowSensitive     bool
}

type boundedFileWalkResult struct {
	filesVisited   int
	partialReasons []FilePartialReason
}

type fileWalkVisitor func(relPath string, info os.FileInfo) error

type boundedFileWalker struct {
	opts    boundedFileWalkOptions
	started time.Time
	result  *boundedFileWalkResult
	visit   fileWalkVisitor
}

type fileSearchExecution struct {
	matcher  *searchMatcher
	walkOpts boundedFileWalkOptions
}

type fileSearchAccumulator struct {
	store          *rootedFileRoot
	matcher        *searchMatcher
	results        []FileSearchFileResult
	bytesScanned   int64
	matchCount     int
	partialReasons []FilePartialReason
}

func (s *fileServiceImpl) IndexFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string) (*FileIndexResult, error) {
	return s.indexFilesScoped(ctx, wsID, scope, target, repo, fileAccess{})
}

func (s *fileServiceImpl) indexFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string, access fileAccess) (*FileIndexResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	allowSensitive := access.sensitive
	if cached, ok := s.indexCache.get(root.identity, allowSensitive); ok {
		return cached, nil
	}

	for {
		generation := s.indexCache.currentGeneration()
		key := fileIndexBuildKey(root.identity, allowSensitive, generation)
		build := s.indexBuilds.DoChan(key, func() (any, error) {
			if cached, ok := s.indexCache.get(root.identity, allowSensitive); ok {
				return cached, nil
			}
			result, err := s.indexBuilder(context.Background(), root.path, root.identity, allowSensitive)
			if err == nil {
				s.indexCache.putIfGeneration(root.identity, allowSensitive, result, generation)
			}
			return result, err
		})
		select {
		case <-ctx.Done():
			return nil, newTimeout("file index canceled")
		case outcome := <-build:
			if outcome.Err != nil {
				return nil, outcome.Err
			}
			if s.indexCache.currentGeneration() != generation {
				continue
			}
			return cloneFileIndexResult(outcome.Val.(*FileIndexResult)), nil
		}
	}
}

func (s *fileServiceImpl) buildFileIndex(ctx context.Context, rootPath, expectedIdentity string, allowSensitive bool) (*FileIndexResult, error) {
	root, err := openScopedRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if root.identity != expectedIdentity {
		return nil, newForbidden("scope root changed before indexing")
	}
	paths := make([]string, 0, 1024)
	walkResult, err := walkScopeFiles(ctx, root.store, boundedFileWalkOptions{
		maxFiles:           fileIndexMaxEntries,
		timeBudget:         fileIndexWalkBudget,
		excludeNodeModules: true,
		allowSensitive:     allowSensitive,
	}, func(relPath string, _ os.FileInfo) error {
		paths = append(paths, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &FileIndexResult{
		Paths:          paths,
		Truncated:      len(walkResult.partialReasons) > 0,
		PartialReasons: walkResult.partialReasons,
	}, nil
}

func (s *fileServiceImpl) SearchFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string, req FileSearchRequest) (*FileSearchResult, error) {
	return s.searchFilesScoped(ctx, wsID, scope, target, repo, req, fileAccess{})
}

func (s *fileServiceImpl) searchFilesScoped(ctx context.Context, wsID string, scope FileScope, target, repo string, req FileSearchRequest, access fileAccess) (*FileSearchResult, error) {
	if repo == "" {
		repo = req.Repo
	}
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	search, err := newFileSearchExecution(req)
	if err != nil {
		return nil, err
	}
	return runFileSearch(ctx, root.store, search, access)
}

func newFileSearchExecution(req FileSearchRequest) (fileSearchExecution, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return fileSearchExecution{}, newInvalid("query is required")
	}

	includePatterns, err := normalizeGlobPatterns(req.Include)
	if err != nil {
		return fileSearchExecution{}, err
	}
	excludeNodeModules := true
	var excludePatterns []string
	if req.Exclude != nil {
		excludeNodeModules = false
		excludePatterns, err = normalizeGlobPatterns(*req.Exclude)
		if err != nil {
			return fileSearchExecution{}, err
		}
	}

	matcher, err := newSearchMatcher(query, req.Regex, req.CaseSensitive)
	if err != nil {
		return fileSearchExecution{}, err
	}

	return fileSearchExecution{
		matcher: matcher,
		walkOpts: boundedFileWalkOptions{
			maxFiles:           fileSearchMaxFiles,
			timeBudget:         fileSearchWalkBudget,
			excludeNodeModules: excludeNodeModules,
			includePatterns:    includePatterns,
			excludePatterns:    excludePatterns,
			allowSensitive:     true,
		},
	}, nil
}

func runFileSearch(ctx context.Context, store *rootedFileRoot, search fileSearchExecution, access fileAccess) (*FileSearchResult, error) {
	search.walkOpts.allowSensitive = access.sensitive
	accumulator := &fileSearchAccumulator{store: store, matcher: search.matcher}
	walkResult, err := walkScopeFiles(ctx, store, search.walkOpts, accumulator.visit)
	if err != nil {
		return nil, err
	}
	return accumulator.result(walkResult), nil
}

func (a *fileSearchAccumulator) visit(relPath string, info os.FileInfo) error {
	remainingBytes := fileSearchMaxBytes - a.bytesScanned
	if remainingBytes <= 0 {
		a.addReason(FilePartialByteLimit)
		return errBoundedWalkLimit
	}
	readLimit := fileSearchMaxFileBytes
	if remainingBytes < readLimit {
		readLimit = remainingBytes
	}
	data, _, truncated, err := a.store.Read(filepath.FromSlash(relPath), readLimit)
	if err != nil {
		return newInternal("failed to read file during search", err)
	}
	a.bytesScanned += int64(len(data))
	if truncated {
		if readLimit < fileSearchMaxFileBytes {
			a.addReason(FilePartialByteLimit)
		} else {
			a.addReason(FilePartialFileSize)
		}
	}
	data, valid := validSearchUTF8(data, truncated)
	if !valid || IsBinaryContent(data) {
		if truncated && readLimit < fileSearchMaxFileBytes {
			return errBoundedWalkLimit
		}
		return nil
	}

	remaining := fileSearchMaxMatches - a.matchCount
	if remaining <= 0 {
		a.addReason(FilePartialResultCount)
		return errBoundedWalkLimit
	}
	matches, clipped := a.matcher.find(string(data), remaining)
	if clipped {
		a.addReason(FilePartialResultCount)
	}
	if len(matches) > 0 {
		a.matchCount += len(matches)
		a.results = append(a.results, FileSearchFileResult{
			Path:    relPath,
			Matches: matches,
		})
	}
	if clipped || (truncated && readLimit < fileSearchMaxFileBytes) {
		return errBoundedWalkLimit
	}
	return nil
}

func (a *fileSearchAccumulator) result(walkResult boundedFileWalkResult) *FileSearchResult {
	return &FileSearchResult{
		Results:        a.results,
		LimitHit:       len(a.partialReasons) > 0 || len(walkResult.partialReasons) > 0,
		PartialReasons: mergePartialReasons(a.partialReasons, walkResult.partialReasons),
	}
}

func (s *fileServiceImpl) invalidateIndex(root string) {
	if s.indexCache != nil {
		s.indexCache.invalidateOverlapping(root)
	}
}

func walkScopeFiles(ctx context.Context, store *rootedFileRoot, opts boundedFileWalkOptions, visit fileWalkVisitor) (boundedFileWalkResult, error) {
	result := boundedFileWalkResult{partialReasons: make([]FilePartialReason, 0)}
	walker := &boundedFileWalker{
		opts:    opts,
		started: time.Now(),
		result:  &result,
		visit:   visit,
	}
	err := store.Walk(
		ctx.Err,
		func(relPath string, entry os.DirEntry) (bool, error) {
			return walker.walkDirectory(ctx, filepath.ToSlash(relPath), entry)
		},
		func(relPath string, info os.FileInfo) error {
			return walker.walkFile(ctx, filepath.ToSlash(relPath), info)
		},
	)
	if errors.Is(err, errBoundedWalkLimit) {
		return result, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if errors.Is(err, context.DeadlineExceeded) {
			result.addReason(FilePartialDeadline)
		} else {
			result.addReason(FilePartialCanceled)
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (w *boundedFileWalker) walkDirectory(ctx context.Context, relPath string, entry os.DirEntry) (bool, error) {
	if err := w.checkWalkState(ctx, nil); err != nil {
		return false, err
	}
	return w.handleDirectory(entry, relPath)
}

func (w *boundedFileWalker) walkFile(ctx context.Context, relPath string, info os.FileInfo) error {
	if err := w.checkWalkState(ctx, nil); err != nil {
		return err
	}
	if err := w.handleFile(info, relPath); err != nil {
		return err
	}
	return ctx.Err()
}

func (w *boundedFileWalker) checkWalkState(ctx context.Context, walkErr error) error {
	if walkErr != nil {
		return newInternal("failed to walk files", walkErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.opts.timeBudget > 0 && time.Since(w.started) > w.opts.timeBudget {
		w.result.addReason(FilePartialDeadline)
		return errBoundedWalkLimit
	}
	return nil
}

func (w *boundedFileWalker) handleDirectory(entry os.DirEntry, relPath string) (bool, error) {
	if strings.EqualFold(entry.Name(), ".git") {
		return false, nil
	}
	if w.opts.excludeNodeModules && strings.EqualFold(entry.Name(), "node_modules") {
		return false, nil
	}
	if !w.opts.allowSensitive && IsSensitiveFilePath(relPath) {
		return false, nil
	}
	if globMatchesAny(w.opts.excludePatterns, relPath) {
		return false, nil
	}
	return true, nil
}

func (w *boundedFileWalker) handleFile(info os.FileInfo, relPath string) error {
	if !w.shouldVisitFile(info, relPath) {
		return nil
	}
	if w.opts.maxFiles > 0 && w.result.filesVisited >= w.opts.maxFiles {
		w.result.addReason(FilePartialFileCount)
		return errBoundedWalkLimit
	}
	w.result.filesVisited++
	return w.visit(relPath, info)
}

func (w *boundedFileWalker) shouldVisitFile(info os.FileInfo, relPath string) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	if len(w.opts.includePatterns) > 0 && !globMatchesAny(w.opts.includePatterns, relPath) {
		return false
	}
	if !w.opts.allowSensitive && IsSensitiveFilePath(relPath) {
		return false
	}
	return !globMatchesAny(w.opts.excludePatterns, relPath)
}

func normalizeGlobPatterns(patterns []string) ([]string, error) {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, err := doublestar.Match(pattern, ""); err != nil {
			return nil, newInvalid("invalid glob pattern")
		}
		normalized = append(normalized, pattern)
	}
	return normalized, nil
}

func globMatchesAny(patterns []string, relPath string) bool {
	for _, pattern := range patterns {
		if ok, _ := doublestar.Match(pattern, relPath); ok {
			return true
		}
	}
	return false
}

type searchMatcher struct {
	query         string
	caseSensitive bool
	regex         *regexp.Regexp
}

func newSearchMatcher(query string, regex, caseSensitive bool) (*searchMatcher, error) {
	matcher := &searchMatcher{query: query, caseSensitive: caseSensitive}
	if regex || !caseSensitive {
		pattern := query
		if !regex {
			pattern = regexp.QuoteMeta(pattern)
		}
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, newInvalid("invalid regex query")
		}
		matcher.regex = re
	}
	return matcher, nil
}

func (m *searchMatcher) find(content string, maxMatches int) ([]FileSearchMatch, bool) {
	if maxMatches <= 0 {
		return nil, true
	}
	lines := strings.Split(content, "\n")
	matches := make([]FileSearchMatch, 0)
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		remaining := maxMatches - len(matches)
		probeLimit := remaining + 1
		if remaining == 0 {
			probeLimit = 1
		}
		var lineMatches []FileSearchMatch
		if m.regex != nil {
			lineMatches = m.regexLineMatches(i+1, line, probeLimit)
		} else {
			lineMatches = m.literalLineMatches(i+1, line, probeLimit)
		}
		if remaining == 0 {
			if len(lineMatches) > 0 {
				return matches, true
			}
			continue
		}
		if len(lineMatches) > remaining {
			matches = append(matches, lineMatches[:remaining]...)
			return matches, true
		}
		matches = append(matches, lineMatches...)
	}
	return matches, false
}

func (m *searchMatcher) literalLineMatches(lineNumber int, line string, remaining int) []FileSearchMatch {
	if remaining <= 0 {
		return nil
	}
	haystack := line
	needle := m.query
	if !m.caseSensitive {
		haystack = strings.ToLower(line)
		needle = strings.ToLower(m.query)
	}
	matches := make([]FileSearchMatch, 0)
	offset := 0
	for remaining > 0 {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			break
		}
		start := offset + idx
		matches = append(matches, FileSearchMatch{
			Line:    lineNumber,
			Col:     utf8.RuneCountInString(line[:start]) + 1,
			Preview: previewLine(line),
		})
		remaining--
		offset = start + len(needle)
		if len(needle) == 0 {
			offset++
		}
		if offset > len(haystack) {
			break
		}
	}
	return matches
}

func (m *searchMatcher) regexLineMatches(lineNumber int, line string, remaining int) []FileSearchMatch {
	if remaining <= 0 || m.regex == nil {
		return nil
	}
	indexes := m.regex.FindAllStringIndex(line, remaining)
	matches := make([]FileSearchMatch, 0, len(indexes))
	for _, idx := range indexes {
		start := idx[0]
		matches = append(matches, FileSearchMatch{
			Line:    lineNumber,
			Col:     utf8.RuneCountInString(line[:start]) + 1,
			Preview: previewLine(line),
		})
	}
	return matches
}

func previewLine(line string) string {
	line = strings.TrimSpace(line)
	const maxPreview = 240
	if utf8.RuneCountInString(line) <= maxPreview {
		return line
	}
	runes := []rune(line)
	return string(runes[:maxPreview]) + "..."
}

func validSearchUTF8(data []byte, truncated bool) ([]byte, bool) {
	if utf8.Valid(data) {
		return data, true
	}
	if !truncated {
		return nil, false
	}
	start := len(data) - 1
	for start > 0 && len(data)-start < utf8.UTFMax && data[start]&0xc0 == 0x80 {
		start--
	}
	if utf8.FullRune(data[start:]) {
		return nil, false
	}
	if candidate := data[:start]; utf8.Valid(candidate) {
		return candidate, true
	}
	return nil, false
}

func (a *fileSearchAccumulator) addReason(reason FilePartialReason) {
	a.partialReasons = appendPartialReason(a.partialReasons, reason)
}

func (r *boundedFileWalkResult) addReason(reason FilePartialReason) {
	r.partialReasons = appendPartialReason(r.partialReasons, reason)
}

func appendPartialReason(reasons []FilePartialReason, reason FilePartialReason) []FilePartialReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func mergePartialReasons(groups ...[]FilePartialReason) []FilePartialReason {
	merged := make([]FilePartialReason, 0)
	for _, group := range groups {
		for _, reason := range group {
			merged = appendPartialReason(merged, reason)
		}
	}
	return merged
}
