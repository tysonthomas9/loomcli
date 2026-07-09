package svcimpl

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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

var (
	errBoundedWalkLimit            = errors.New("bounded file walk limit reached")
	defaultFileWalkExcludeSegments = map[string]bool{
		".git":         true,
		"node_modules": true,
	}
)

type fileIndexCacheEntry struct {
	paths     []string
	truncated bool
	expiresAt time.Time
}

type boundedFileWalkOptions struct {
	maxFiles        int
	timeBudget      time.Duration
	excludeSegments map[string]bool
	includePatterns []string
	excludePatterns []string
}

type boundedFileWalkResult struct {
	filesVisited int
	truncated    bool
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
	store        *rootedFileStore
	matcher      *searchMatcher
	results      []service.FileSearchFileResult
	bytesScanned int64
	matchCount   int
	limitHit     bool
}

func (s *fileServiceImpl) IndexFilesScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo string) (*service.FileIndexResult, error) {
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if cached, ok := s.cachedIndex(root.path); ok {
		return cached, nil
	}

	paths := make([]string, 0, 1024)
	walkResult, err := walkScopeFiles(ctx, root.store, boundedFileWalkOptions{
		maxFiles:        fileIndexMaxEntries,
		timeBudget:      fileIndexWalkBudget,
		excludeSegments: defaultFileWalkExcludeSegments,
	}, func(relPath string, _ os.FileInfo) error {
		paths = append(paths, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := &service.FileIndexResult{
		Paths:     paths,
		Truncated: walkResult.truncated,
	}
	s.storeIndex(root.path, result)
	return result, nil
}

func (s *fileServiceImpl) SearchFilesScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo string, req service.FileSearchRequest) (*service.FileSearchResult, error) {
	if repo == "" {
		repo = req.Repo
	}
	root, err := s.resolveScopedRoot(wsID, scope, target, repo)
	if err != nil {
		return nil, err
	}

	search, err := newFileSearchExecution(req)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return runFileSearch(ctx, root.store, search)
}

func newFileSearchExecution(req service.FileSearchRequest) (fileSearchExecution, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return fileSearchExecution{}, service.ErrValidation("query is required")
	}

	includePatterns, err := normalizeGlobPatterns(req.Include)
	if err != nil {
		return fileSearchExecution{}, err
	}
	excludeSegments := defaultFileWalkExcludeSegments
	var excludePatterns []string
	if req.Exclude != nil {
		excludeSegments = nil
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
			maxFiles:        fileSearchMaxFiles,
			timeBudget:      fileSearchWalkBudget,
			excludeSegments: excludeSegments,
			includePatterns: includePatterns,
			excludePatterns: excludePatterns,
		},
	}, nil
}

func runFileSearch(ctx context.Context, store *rootedFileStore, search fileSearchExecution) (*service.FileSearchResult, error) {
	accumulator := &fileSearchAccumulator{store: store, matcher: search.matcher}
	walkResult, err := walkScopeFiles(ctx, store, search.walkOpts, accumulator.visit)
	if err != nil {
		return nil, err
	}
	return accumulator.result(walkResult), nil
}

func (a *fileSearchAccumulator) visit(relPath string, info os.FileInfo) error {
	if info.Size() > fileSearchMaxFileBytes {
		a.limitHit = true
		return nil
	}
	if a.bytesScanned+info.Size() > fileSearchMaxBytes {
		a.limitHit = true
		return errBoundedWalkLimit
	}

	data, _, _, err := a.store.Read(filepath.FromSlash(relPath), fileSearchMaxFileBytes)
	if err != nil {
		return service.ErrInternal("failed to read file during search", err)
	}
	a.bytesScanned += int64(len(data))
	if misc.IsBinaryContent(data) {
		return nil
	}

	remaining := fileSearchMaxMatches - a.matchCount
	if remaining <= 0 {
		a.limitHit = true
		return errBoundedWalkLimit
	}
	matches, clipped := a.matcher.find(string(data), remaining)
	if clipped {
		a.limitHit = true
	}
	if len(matches) > 0 {
		a.matchCount += len(matches)
		a.results = append(a.results, service.FileSearchFileResult{
			Path:    relPath,
			Matches: matches,
		})
	}
	if clipped {
		return errBoundedWalkLimit
	}
	return nil
}

func (a *fileSearchAccumulator) result(walkResult boundedFileWalkResult) *service.FileSearchResult {
	return &service.FileSearchResult{
		Results:  a.results,
		LimitHit: a.limitHit || walkResult.truncated,
	}
}

func (s *fileServiceImpl) cachedIndex(root string) (*service.FileIndexResult, bool) {
	s.indexCacheMu.Lock()
	defer s.indexCacheMu.Unlock()
	entry, ok := s.indexCache[root]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(s.indexCache, root)
		}
		return nil, false
	}
	return &service.FileIndexResult{
		Paths:     append([]string(nil), entry.paths...),
		Truncated: entry.truncated,
	}, true
}

func (s *fileServiceImpl) storeIndex(root string, result *service.FileIndexResult) {
	s.indexCacheMu.Lock()
	defer s.indexCacheMu.Unlock()
	s.indexCache[root] = fileIndexCacheEntry{
		paths:     append([]string(nil), result.Paths...),
		truncated: result.Truncated,
		expiresAt: time.Now().Add(fileIndexCacheTTL),
	}
}

func (s *fileServiceImpl) invalidateIndex(root string) {
	s.indexCacheMu.Lock()
	defer s.indexCacheMu.Unlock()
	delete(s.indexCache, root)
}

func walkScopeFiles(ctx context.Context, store *rootedFileStore, opts boundedFileWalkOptions, visit fileWalkVisitor) (boundedFileWalkResult, error) {
	result := boundedFileWalkResult{}
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
		return result, service.ErrTimeout("file walk canceled")
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
	return w.handleFile(info, relPath)
}

func (w *boundedFileWalker) checkWalkState(ctx context.Context, walkErr error) error {
	if walkErr != nil {
		return service.ErrInternal("failed to walk files", walkErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.opts.timeBudget > 0 && time.Since(w.started) > w.opts.timeBudget {
		w.result.truncated = true
		return errBoundedWalkLimit
	}
	return nil
}

func (w *boundedFileWalker) handleDirectory(entry os.DirEntry, relPath string) (bool, error) {
	if strings.EqualFold(entry.Name(), ".git") {
		return false, nil
	}
	if w.opts.excludeSegments != nil && w.opts.excludeSegments[entry.Name()] {
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
		w.result.truncated = true
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
	return !globMatchesAny(w.opts.excludePatterns, relPath)
}

func normalizeGlobPatterns(patterns []string) ([]string, error) {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, service.ErrValidation("invalid glob pattern")
		}
		normalized = append(normalized, pattern)
	}
	return normalized, nil
}

func globMatchesAny(patterns []string, relPath string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, relPath); ok {
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
	if regex {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, service.ErrValidation("invalid regex query")
		}
		matcher.regex = re
	}
	return matcher, nil
}

func (m *searchMatcher) find(content string, maxMatches int) ([]service.FileSearchMatch, bool) {
	if maxMatches <= 0 {
		return nil, true
	}
	lines := strings.Split(content, "\n")
	matches := make([]service.FileSearchMatch, 0)
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		var lineMatches []service.FileSearchMatch
		if m.regex != nil {
			lineMatches = m.regexLineMatches(i+1, line, maxMatches-len(matches))
		} else {
			lineMatches = m.literalLineMatches(i+1, line, maxMatches-len(matches))
		}
		matches = append(matches, lineMatches...)
		if len(matches) >= maxMatches {
			return matches, true
		}
	}
	return matches, false
}

func (m *searchMatcher) literalLineMatches(lineNumber int, line string, remaining int) []service.FileSearchMatch {
	if remaining <= 0 {
		return nil
	}
	haystack := line
	needle := m.query
	if !m.caseSensitive {
		haystack = strings.ToLower(line)
		needle = strings.ToLower(m.query)
	}
	matches := make([]service.FileSearchMatch, 0)
	offset := 0
	for remaining > 0 {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			break
		}
		start := offset + idx
		matches = append(matches, service.FileSearchMatch{
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

func (m *searchMatcher) regexLineMatches(lineNumber int, line string, remaining int) []service.FileSearchMatch {
	if remaining <= 0 || m.regex == nil {
		return nil
	}
	indexes := m.regex.FindAllStringIndex(line, remaining)
	matches := make([]service.FileSearchMatch, 0, len(indexes))
	for _, idx := range indexes {
		start := idx[0]
		matches = append(matches, service.FileSearchMatch{
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
	if len(line) <= maxPreview {
		return line
	}
	return line[:maxPreview] + "..."
}
