package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	defaultCodeloadBaseURL = "https://codeload.github.com"

	// GitHub skill installs are held in memory until every archive entry has
	// been validated, so bound both the wire representation and the expanded
	// tar stream. These limits keep a single install comfortably below the
	// memory available to the CLI while leaving room for documentation-heavy
	// skills.
	maxSkillArchiveCompressedBytes   int64 = 32 << 20
	maxSkillArchiveDecompressedBytes int64 = 128 << 20
	defaultGitHubHTTPTimeout               = 5 * time.Minute
)

var errGitHubSkillSubpathNotFound = errors.New("GitHub skill subpath not found")

// githubSkillInstaller is the deep module behind `loom skill install`: its
// caller supplies only a source and optional name, while parsing, fetching,
// archive safety, discovery, frontmatter, and text validation stay local.
// The transport fields are injectable so tests cross the same interface
// without reaching GitHub.
type githubSkillInstaller struct {
	HTTPClient           *http.Client
	CodeloadBaseURL      string
	MaxCompressedBytes   int64
	MaxDecompressedBytes int64
}

type fetchedGitHubSkill struct {
	Name                   string
	Description            string
	Content                string
	Files                  []domain.SkillFile
	Source                 string
	DroppedFrontmatterKeys []string
}

type githubSource struct {
	Owner   string
	Repo    string
	Ref     string
	Subpath string
	TreeURL bool
}

type githubArchiveFile struct {
	content    []byte
	executable bool
}

type githubArchive struct {
	files       map[string]githubArchiveFile
	links       map[string]string
	unsupported map[string]string
}

type skillFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	DroppedKeys []string `yaml:"-"`
}

func (i githubSkillInstaller) Fetch(ctx context.Context, rawSource, nameOverride string) (fetchedGitHubSkill, error) {
	source, err := parseGitHubSource(rawSource)
	if err != nil {
		return fetchedGitHubSkill{}, err
	}
	compressed, err := i.download(ctx, source)
	if err != nil {
		return fetchedGitHubSkill{}, err
	}
	decompressed, err := readGzipArchive(compressed, i.decompressedLimit())
	if err != nil {
		return fetchedGitHubSkill{}, err
	}
	archive, err := readGitHubTar(decompressed, i.decompressedLimit())
	if err != nil {
		return fetchedGitHubSkill{}, err
	}
	skillDir, err := archive.selectSkillDir(source.Subpath)
	if err != nil {
		if source.TreeURL && errors.Is(err, errGitHubSkillSubpathNotFound) {
			return fetchedGitHubSkill{}, withTreeURLRefHint(err)
		}
		return fetchedGitHubSkill{}, err
	}
	return archive.toSkill(source, skillDir, nameOverride)
}

func (i githubSkillInstaller) download(ctx context.Context, source githubSource) ([]byte, error) {
	baseURL := strings.TrimRight(i.CodeloadBaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCodeloadBaseURL
	}
	archiveURL := baseURL + "/" + url.PathEscape(source.Owner) + "/" + url.PathEscape(source.Repo) + "/tar.gz/" + url.PathEscape(source.Ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build GitHub archive request: %w", err)
	}
	if token, set := os.LookupEnv("GITHUB_TOKEN"); set && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := i.HTTPClient
	if client == nil {
		originalHost := req.URL.Host
		client = &http.Client{
			Timeout: defaultGitHubHTTPTimeout,
			CheckRedirect: func(next *http.Request, _ []*http.Request) error {
				if strings.EqualFold(next.URL.Host, originalHost) {
					return nil
				}
				return fmt.Errorf("refusing redirect from codeload host %q to %q", originalHost, next.URL.Host)
			},
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub archive %s/%s@%s: %w", source.Owner, source.Repo, source.Ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := fmt.Errorf("fetch GitHub archive %s/%s@%s: codeload returned %s", source.Owner, source.Repo, source.Ref, resp.Status)
		if resp.StatusCode == http.StatusNotFound && source.TreeURL {
			return nil, withTreeURLRefHint(err)
		}
		return nil, err
	}
	compressed, err := readWithLimit(resp.Body, i.compressedLimit(), "compressed GitHub archive")
	if err != nil {
		return nil, err
	}
	return compressed, nil
}

func (i githubSkillInstaller) compressedLimit() int64 {
	if i.MaxCompressedBytes > 0 {
		return i.MaxCompressedBytes
	}
	return maxSkillArchiveCompressedBytes
}

func (i githubSkillInstaller) decompressedLimit() int64 {
	if i.MaxDecompressedBytes > 0 {
		return i.MaxDecompressedBytes
	}
	return maxSkillArchiveDecompressedBytes
}

func readWithLimit(r io.Reader, limit int64, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d-byte size limit", label, limit)
	}
	return data, nil
}

func readGzipArchive(compressed []byte, limit int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open GitHub archive gzip stream: %w", err)
	}
	decompressed, readErr := readWithLimit(zr, limit, "decompressed GitHub archive")
	closeErr := zr.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close GitHub archive gzip stream: %w", closeErr)
	}
	return decompressed, nil
}

func parseGitHubSource(raw string) (githubSource, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return githubSource{}, fmt.Errorf("GitHub source must not be empty")
	}

	withoutSuffix, suffixRef, hasSuffix, err := splitGitHubRefSuffix(raw)
	if err != nil {
		return githubSource{}, err
	}
	var source githubSource
	if strings.Contains(withoutSuffix, "://") {
		source, err = parseGitHubURL(withoutSuffix)
	} else {
		source, err = parseGitHubShorthand(withoutSuffix)
	}
	if err != nil {
		return githubSource{}, err
	}
	if hasSuffix {
		source.Ref = suffixRef
	}
	if source.Ref == "" {
		// Codeload treats HEAD as the repository's default branch, avoiding a
		// separate GitHub API request and keeping installation to one GET.
		source.Ref = "HEAD"
	}
	if err := validateGitHubRef(source.Ref); err != nil {
		return githubSource{}, err
	}
	return source, nil
}

func splitGitHubRefSuffix(raw string) (source, ref string, found bool, err error) {
	index := strings.LastIndexByte(raw, '@')
	if index < 0 {
		return raw, "", false, nil
	}
	if index == 0 || index == len(raw)-1 {
		return "", "", false, fmt.Errorf("GitHub source %q must put a non-empty ref after @", raw)
	}
	return raw[:index], raw[index+1:], true, nil
}

func parseGitHubURL(raw string) (githubSource, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return githubSource{}, fmt.Errorf("parse GitHub source %q: %w", raw, err)
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return githubSource{}, fmt.Errorf("GitHub source URL must be an https://github.com/owner/repo URL, got %q", raw)
	}
	segments, err := sourcePathSegments(u.Path)
	if err != nil {
		return githubSource{}, fmt.Errorf("GitHub source URL %q: %w", raw, err)
	}
	if len(segments) < 2 {
		return githubSource{}, fmt.Errorf("GitHub source URL must include owner and repository, got %q", raw)
	}
	source := githubSource{Owner: segments[0], Repo: stripDotGitSuffix(segments[1])}
	if len(segments) > 2 {
		if len(segments) < 4 || segments[2] != "tree" {
			return githubSource{}, fmt.Errorf("GitHub source URL path after the repository must be /tree/<ref>[/sub/path], got %q", raw)
		}
		source.Ref = segments[3]
		source.Subpath = strings.Join(segments[4:], "/")
		source.TreeURL = true
	}
	if err := validateGitHubRepository(source.Owner, source.Repo); err != nil {
		return githubSource{}, err
	}
	return source, nil
}

func parseGitHubShorthand(raw string) (githubSource, error) {
	segments, err := sourcePathSegments(raw)
	if err != nil {
		return githubSource{}, fmt.Errorf("GitHub source %q: %w", raw, err)
	}
	if len(segments) < 2 {
		return githubSource{}, fmt.Errorf("GitHub source must be owner/repo[/sub/path], got %q", raw)
	}
	if len(segments) >= 3 && strings.EqualFold(segments[0], "github.com") {
		segments = segments[1:]
	}
	source := githubSource{
		Owner:   segments[0],
		Repo:    stripDotGitSuffix(segments[1]),
		Subpath: strings.Join(segments[2:], "/"),
	}
	if err := validateGitHubRepository(source.Owner, source.Repo); err != nil {
		return githubSource{}, err
	}
	return source, nil
}

func stripDotGitSuffix(repo string) string {
	return strings.TrimSuffix(repo, ".git")
}

func withTreeURLRefHint(err error) error {
	return fmt.Errorf("%w; GitHub tree URLs treat one path segment after /tree/ as the ref; branches containing \"/\" must be passed via the @<ref> suffix form", err)
}

func sourcePathSegments(value string) ([]string, error) {
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, `\`) {
		return nil, fmt.Errorf("path must be a non-empty slash-separated path")
	}
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("path must be valid UTF-8")
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("path contains an unsafe component")
		}
		for _, r := range segment {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("path contains a control character")
			}
		}
	}
	return segments, nil
}

func validateGitHubRepository(owner, repo string) error {
	if !isGitHubRepositorySegment(owner) || !isGitHubRepositorySegment(repo) {
		return fmt.Errorf("GitHub source owner and repository must use letters, digits, dots, underscores, or hyphens")
	}
	return nil
}

func isGitHubRepositorySegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validateGitHubRef(ref string) error {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(ref) != ref {
		return fmt.Errorf("GitHub ref must not be empty or surrounded by whitespace")
	}
	if strings.Contains(ref, `\`) || !utf8.ValidString(ref) {
		return fmt.Errorf("GitHub ref %q must be valid UTF-8 and must not contain backslashes", ref)
	}
	for _, r := range ref {
		if unicode.IsControl(r) {
			return fmt.Errorf("GitHub ref %q contains a control character", ref)
		}
	}
	return nil
}

//nolint:gocognit,funlen // The tar walk validates every entry class in one bounded pass.
func readGitHubTar(data []byte, logicalLimit int64) (githubArchive, error) {
	archive := githubArchive{
		files:       make(map[string]githubArchiveFile),
		links:       make(map[string]string),
		unsupported: make(map[string]string),
	}
	tr := tar.NewReader(bytes.NewReader(data))
	topLevel := ""
	seen := make(map[string]struct{})
	logicalRemaining := logicalLimit
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return githubArchive{}, fmt.Errorf("read GitHub tar archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			// git archive (and therefore codeload) leads with a
			// pax_global_header recording the commit SHA; archive/tar hands it
			// to the caller as an entry, but it is metadata, not a path.
			continue
		}
		entryPath, root, err := safeTarEntryPath(hdr.Name)
		if err != nil {
			return githubArchive{}, err
		}
		if topLevel == "" {
			topLevel = root
		} else if root != topLevel {
			return githubArchive{}, fmt.Errorf("GitHub tar archive has more than one top-level directory: %q and %q", topLevel, root)
		}
		if entryPath == "" {
			if hdr.Typeflag != tar.TypeDir {
				return githubArchive{}, fmt.Errorf("GitHub tar archive top-level entry %q must be a directory", hdr.Name)
			}
			continue
		}
		if _, duplicate := seen[entryPath]; duplicate {
			return githubArchive{}, fmt.Errorf("GitHub tar archive contains duplicate entry %q", entryPath)
		}
		seen[entryPath] = struct{}{}

		switch hdr.Typeflag {
		case tar.TypeReg: // Reader.Next normalizes legacy TypeRegA to TypeReg.
			if hdr.Size > logicalRemaining {
				return githubArchive{}, fmt.Errorf("GitHub tar logical file content exceeds the %d-byte budget at entry %q: declared size %d, remaining %d",
					logicalLimit, entryPath, hdr.Size, logicalRemaining)
			}
			content, err := readWithLimit(tr, logicalRemaining, fmt.Sprintf("GitHub tar entry %q logical content", entryPath))
			if err != nil {
				return githubArchive{}, err
			}
			if int64(len(content)) != hdr.Size {
				return githubArchive{}, fmt.Errorf("GitHub tar entry %q declared %d logical bytes but produced %d", entryPath, hdr.Size, len(content))
			}
			logicalRemaining -= int64(len(content))
			archive.files[entryPath] = githubArchiveFile{
				content:    content,
				executable: hdr.Mode&0o111 != 0,
			}
		case tar.TypeDir:
		case tar.TypeSymlink:
			archive.links[entryPath] = "symlink"
		case tar.TypeLink:
			archive.links[entryPath] = "hardlink"
		default:
			archive.unsupported[entryPath] = fmt.Sprintf("tar type %d", hdr.Typeflag)
		}
	}
	if topLevel == "" {
		return githubArchive{}, fmt.Errorf("GitHub tar archive is empty")
	}
	return archive, nil
}

func safeTarEntryPath(raw string) (relative, root string, err error) {
	if raw == "" || !utf8.ValidString(raw) {
		return "", "", fmt.Errorf("GitHub tar entry path must be non-empty valid UTF-8")
	}
	if path.IsAbs(raw) || isWindowsAbsolutePath(raw) || strings.Contains(raw, `\`) {
		return "", "", fmt.Errorf("unsafe GitHub tar entry %q: absolute paths and backslashes are forbidden", raw)
	}
	name := strings.TrimSuffix(raw, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		return "", "", fmt.Errorf("unsafe GitHub tar entry %q: path must be normalized", raw)
	}
	segments := strings.Split(name, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", fmt.Errorf("unsafe GitHub tar entry %q: . and .. components are forbidden", raw)
		}
		for _, r := range segment {
			if unicode.IsControl(r) {
				return "", "", fmt.Errorf("unsafe GitHub tar entry %q: control characters are forbidden", raw)
			}
		}
	}
	if path.Clean(name) != name {
		return "", "", fmt.Errorf("unsafe GitHub tar entry %q: path must be normalized", raw)
	}
	return strings.Join(segments[1:], "/"), segments[0], nil
}

func isWindowsAbsolutePath(value string) bool {
	if len(value) < 3 || value[1] != ':' || value[2] != '/' {
		return false
	}
	return (value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')
}

func (a githubArchive) selectSkillDir(requested string) (string, error) {
	if requested != "" {
		if err := a.rejectUnsafeSelectedEntries(requested); err != nil {
			return "", err
		}
		if _, ok := a.files[path.Join(requested, domain.SkillFileNameSKILLMD)]; !ok {
			return "", fmt.Errorf("no %s found at requested GitHub subpath %q: %w", domain.SkillFileNameSKILLMD, requested, errGitHubSkillSubpathNotFound)
		}
		return requested, nil
	}

	tiers := [][]string{
		a.candidates(func(parts []string) bool { return len(parts) == 1 }),
		a.candidates(func(parts []string) bool { return len(parts) == 3 && parts[0] == "skills" }),
		a.candidates(func(parts []string) bool { return len(parts) == 4 && parts[0] == ".agents" && parts[1] == "skills" }),
		a.candidates(func(parts []string) bool { return len(parts) == 2 }),
	}
	if len(tiers[0]) > 0 {
		var deeper []string
		for _, candidates := range tiers[1:] {
			deeper = append(deeper, candidates...)
		}
		if len(deeper) > 0 {
			return "", multipleSkillCandidatesError(append(tiers[0], deeper...))
		}
	}
	for _, candidates := range tiers {
		if len(candidates) == 0 {
			continue
		}
		if len(candidates) > 1 {
			return "", multipleSkillCandidatesError(candidates)
		}
		if err := a.rejectUnsafeSelectedEntries(candidates[0]); err != nil {
			return "", err
		}
		return candidates[0], nil
	}
	return "", fmt.Errorf("no %s found in the repository root, skills/*, .agents/skills/*, or one directory deep", domain.SkillFileNameSKILLMD)
}

func multipleSkillCandidatesError(candidates []string) error {
	display := append([]string(nil), candidates...)
	for index, candidate := range display {
		if candidate == "" {
			display[index] = "."
		}
	}
	sort.Strings(display)
	return fmt.Errorf("multiple skills found in GitHub repository:\n  %s\npass one of these skill subpaths in SOURCE", strings.Join(display, "\n  "))
}

func (a githubArchive) candidates(match func([]string) bool) []string {
	var candidates []string
	for filePath := range a.files {
		parts := strings.Split(filePath, "/")
		if parts[len(parts)-1] != domain.SkillFileNameSKILLMD || !match(parts) {
			continue
		}
		candidate := strings.TrimSuffix(filePath, "/"+domain.SkillFileNameSKILLMD)
		if filePath == domain.SkillFileNameSKILLMD {
			candidate = ""
		}
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return candidates
}

func (a githubArchive) rejectUnsafeSelectedEntries(skillDir string) error {
	var links []string
	for entryPath, kind := range a.links {
		if isWithinSkillDir(skillDir, entryPath) {
			links = append(links, fmt.Sprintf("%s (%s)", relativeSkillPath(skillDir, entryPath), kind))
		}
	}
	if len(links) > 0 {
		sort.Strings(links)
		return fmt.Errorf("selected skill directory contains link entries that cannot be installed: %s", strings.Join(links, ", "))
	}
	var unsupported []string
	for entryPath, kind := range a.unsupported {
		if isWithinSkillDir(skillDir, entryPath) {
			unsupported = append(unsupported, fmt.Sprintf("%s (%s)", relativeSkillPath(skillDir, entryPath), kind))
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return fmt.Errorf("selected skill directory contains unsupported archive entries: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

//nolint:funlen // Selection, frontmatter parse, and bundling stay one auditable unit.
func (a githubArchive) toSkill(source githubSource, skillDir, nameOverride string) (fetchedGitHubSkill, error) {
	selected := make(map[string]githubArchiveFile)
	for filePath, file := range a.files {
		if isWithinSkillDir(skillDir, filePath) {
			selected[relativeSkillPath(skillDir, filePath)] = file
		}
	}
	var binaryPaths []string
	for filePath, file := range selected {
		if !isSkillText(file.content) {
			binaryPaths = append(binaryPaths, filePath)
		}
	}
	if len(binaryPaths) > 0 {
		sort.Strings(binaryPaths)
		return fetchedGitHubSkill{}, fmt.Errorf("skill contains non-UTF-8 or NUL-bearing files; binary content is not supported: %s", strings.Join(binaryPaths, ", "))
	}

	document, ok := selected[domain.SkillFileNameSKILLMD]
	if !ok {
		return fetchedGitHubSkill{}, fmt.Errorf("selected skill directory does not contain %s", domain.SkillFileNameSKILLMD)
	}
	metadata, body, err := parseSkillDocument(document.content)
	if err != nil {
		return fetchedGitHubSkill{}, err
	}
	if err := validateFrontmatterText("name", metadata.Name, false); err != nil {
		return fetchedGitHubSkill{}, err
	}
	if err := validateFrontmatterText("description", metadata.Description, true); err != nil {
		return fetchedGitHubSkill{}, err
	}
	name := metadata.Name
	if name == "" {
		name = path.Base(skillDir)
		if skillDir == "" || name == "." {
			name = source.Repo
		}
	}
	if nameOverride != "" {
		name = nameOverride
	}
	if err := domain.ValidateSkillName(name); err != nil {
		return fetchedGitHubSkill{}, err
	}
	if strings.TrimSpace(metadata.Description) == "" {
		return fetchedGitHubSkill{}, fmt.Errorf("skill description in %s must not be empty", domain.SkillFileNameSKILLMD)
	}

	files := make([]domain.SkillFile, 0, len(selected)-1)
	for filePath, file := range selected {
		if filePath == domain.SkillFileNameSKILLMD {
			continue
		}
		if err := validateSkillFileDestination(filePath); err != nil {
			return fetchedGitHubSkill{}, fmt.Errorf("bundled skill file %q: %w", filePath, err)
		}
		files = append(files, domain.SkillFile{
			Path:       filePath,
			Content:    string(file.content),
			Executable: file.executable,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	provenancePath := skillDir
	provenance := "github.com/" + source.Owner + "/" + source.Repo
	if provenancePath != "" {
		provenance += "/" + provenancePath
	}
	provenance += "@" + source.Ref
	return fetchedGitHubSkill{
		Name:                   name,
		Description:            metadata.Description,
		Content:                string(body),
		Files:                  files,
		Source:                 provenance,
		DroppedFrontmatterKeys: append([]string(nil), metadata.DroppedKeys...),
	}, nil
}

func parseSkillDocument(data []byte) (skillFrontmatter, []byte, error) {
	firstLine, next, ok := nextDocumentLine(data, 0)
	if !ok || string(bytes.TrimSuffix(firstLine, []byte("\r"))) != "---" {
		return skillFrontmatter{}, data, nil
	}
	frontmatterStart := next
	for offset := next; ; {
		line, following, found := nextDocumentLine(data, offset)
		if !found {
			return skillFrontmatter{}, nil, fmt.Errorf("%s frontmatter is missing its closing --- delimiter", domain.SkillFileNameSKILLMD)
		}
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			var metadata skillFrontmatter
			frontmatter := data[frontmatterStart:offset]
			if err := yaml.Unmarshal(frontmatter, &metadata); err != nil {
				return skillFrontmatter{}, nil, fmt.Errorf("parse %s frontmatter: %w", domain.SkillFileNameSKILLMD, err)
			}
			var fields map[string]yaml.Node
			if err := yaml.Unmarshal(frontmatter, &fields); err != nil {
				return skillFrontmatter{}, nil, fmt.Errorf("parse %s frontmatter keys: %w", domain.SkillFileNameSKILLMD, err)
			}
			for key := range fields {
				if key != "name" && key != "description" {
					metadata.DroppedKeys = append(metadata.DroppedKeys, key)
				}
			}
			sort.Strings(metadata.DroppedKeys)
			return metadata, data[following:], nil
		}
		offset = following
	}
}

func validateFrontmatterText(field, value string, allowNewline bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s frontmatter %s must be valid UTF-8 text", domain.SkillFileNameSKILLMD, field)
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(allowNewline && r == '\n') {
			return fmt.Errorf("%s frontmatter %s contains a control character", domain.SkillFileNameSKILLMD, field)
		}
	}
	return nil
}

func nextDocumentLine(data []byte, offset int) (line []byte, next int, ok bool) {
	if offset >= len(data) {
		return nil, offset, false
	}
	if newline := bytes.IndexByte(data[offset:], '\n'); newline >= 0 {
		end := offset + newline
		return data[offset:end], end + 1, true
	}
	return data[offset:], len(data), true
}

func isWithinSkillDir(skillDir, entryPath string) bool {
	return skillDir == "" || entryPath == skillDir || strings.HasPrefix(entryPath, skillDir+"/")
}

func relativeSkillPath(skillDir, entryPath string) string {
	if skillDir == "" {
		return entryPath
	}
	return strings.TrimPrefix(entryPath, skillDir+"/")
}
