package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

var (
	workflowPackageDist        string
	workflowPackageOut         string
	workflowPackageLoomSDK     string
	workflowPackageFlueCommit  string
	workflowPackageNodeVersion string
	workflowPackageTarget      string
	workflowPackageAllowDrift  bool
	workflowPackageRequireAll  bool
	workflowPackageJSON        bool
)

var workflowPackageBuiltinCmd = &cobra.Command{
	Use:   "package-builtin <name>",
	Short: "Package a built-in workflow's Flue dist into a verifiable builtin-workflows resource tree",
	Long: `Stage a Flue-built dist for a built-in workflow into <out>/<name>/dist (with the
@loom/sdk runtime nested under node_modules), audit it statically, and record
it in <out>/index.json. The printed index_digest is what a packaged loom build
bakes via -ldflags -X .../internal/workflows/packaged.ExpectedIndexDigest.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowPackageBuiltin,
}

func init() {
	f := workflowPackageBuiltinCmd.Flags()
	f.StringVar(&workflowPackageDist, "dist", "", "Flue-built dist directory for the workflow (required)")
	f.StringVar(&workflowPackageOut, "out", "", "builtin-workflows resource root to write (required)")
	f.StringVar(&workflowPackageLoomSDK, "loom-sdk", "", "@loom/sdk runtime directory (default: $LOOM_SDK_ROOT, else ./sdk)")
	f.StringVar(&workflowPackageFlueCommit, "flue-commit", "", "Flue commit the dist was built with (default: the pinned FLUE_COMMIT)")
	f.StringVar(&workflowPackageNodeVersion, "node-version", "", "Node version the artifact targets (default: the pinned NODE_VERSION)")
	f.StringVar(&workflowPackageTarget, "target", "", "Target triple recorded in index.json (default: the host triple)")
	f.BoolVar(&workflowPackageAllowDrift, "allow-pin-drift", false, "Allow --flue-commit/--node-version to differ from the pins")
	f.BoolVar(&workflowPackageRequireAll, "require-all", false, "Fail unless every required built-in is in the resulting index")
	f.BoolVar(&workflowPackageJSON, "json", false, "JSON output")
	_ = workflowPackageBuiltinCmd.MarkFlagRequired("dist")
	_ = workflowPackageBuiltinCmd.MarkFlagRequired("out")
}

type packageBuiltinOutput struct {
	Name                 string      `json:"name"`
	Out                  string      `json:"out"`
	Dist                 string      `json:"dist"`
	ArtifactDigest       string      `json:"artifact_digest"`
	SourceDigest         string      `json:"source_digest"`
	SourceDigestAttested bool        `json:"source_digest_attested"`
	IndexDigest          string      `json:"index_digest"`
	Target               string      `json:"target"`
	FlueCommit           string      `json:"flue_commit"`
	NodeVersion          string      `json:"node_version"`
	Audit                auditReport `json:"audit"`
}

type packageBuiltinInputs struct {
	name         string
	dist         string
	out          string
	sdkDir       string
	flueCommit   string
	nodeVersion  string
	target       string
	sourceDigest string
	entrypoint   string
	runners      []driverpkg.DriverRunnerSpec
	attested     bool
}

func invalidArtifact(format string, args ...any) error {
	return fmt.Errorf("builtin_artifact_invalid: "+format+": %w", append(args, domain.ErrInvalid)...)
}

// runWorkflowPackageBuiltin validates everything first, stages into a temp
// dir under --out, audits, and only then commits <out>/<name> + index.json.
func runWorkflowPackageBuiltin(_ *cobra.Command, args []string) error {
	in, err := resolvePackageBuiltinInputs(args[0])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(in.out, 0o755); err != nil {
		return fmt.Errorf("create --out: %w", err)
	}
	tmp, err := os.MkdirTemp(in.out, ".package-builtin-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := stagePackagedDist(in, tmp); err != nil {
		return err
	}
	audit, err := auditArtifact(filepath.Join(tmp, "dist"))
	if err != nil {
		return err
	}
	artifactDigest, err := driverpkg.DigestDirectory(filepath.Join(tmp, "dist"))
	if err != nil {
		return err
	}
	encoded, err := buildPackageIndex(in, artifactDigest)
	if err != nil {
		return err
	}
	if err := commitPackagedBuiltin(in, tmp, encoded); err != nil {
		return err
	}
	keepTmp = true
	return printPackageBuiltinOutput(packageBuiltinOutput{
		Name:                 in.name,
		Out:                  in.out,
		Dist:                 in.dist,
		ArtifactDigest:       artifactDigest,
		SourceDigest:         in.sourceDigest,
		SourceDigestAttested: in.attested,
		IndexDigest:          packaged.IndexDigest(encoded),
		Target:               in.target,
		FlueCommit:           in.flueCommit,
		NodeVersion:          in.nodeVersion,
		Audit:                audit,
	})
}

// buildPackageIndex merges this artifact into the existing index (if any),
// enforces --require-all, and returns the canonical index bytes. Nothing is
// written yet.
func buildPackageIndex(in *packageBuiltinInputs, artifactDigest string) ([]byte, error) {
	idx, err := loadOrInitPackageIndex(in)
	if err != nil {
		return nil, err
	}
	idx.Builtins[in.name] = packaged.Entry{
		Path:           in.name,
		Entrypoint:     in.entrypoint,
		SourceDigest:   in.sourceDigest,
		ArtifactDigest: artifactDigest,
		Runners:        in.runners,
	}
	if workflowPackageRequireAll {
		for _, required := range packaged.RequiredBuiltins {
			if _, ok := idx.Builtins[required]; !ok {
				return nil, invalidArtifact("required built-in %s is not packaged", required)
			}
		}
	}
	return packaged.EncodeIndex(idx)
}

// commitPackagedBuiltin swaps the staged tree into <out>/<name> and writes
// index.json atomically, keeping the previous tree aside until both are in
// place: a failed rename leaves the old tree and index untouched, and a
// failed index write swaps the old tree back. On success tmp no longer exists.
func commitPackagedBuiltin(in *packageBuiltinInputs, tmp string, encoded []byte) error {
	final := filepath.Join(in.out, in.name)
	previous := ""
	if _, err := os.Lstat(final); err == nil {
		previous = tmp + ".previous"
		if err := os.Rename(final, previous); err != nil {
			return fmt.Errorf("set aside %s: %w", final, err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		restorePreviousArtifact(previous, final)
		return fmt.Errorf("commit %s: %w", final, err)
	}
	if err := writeFileAtomic(filepath.Join(in.out, packaged.IndexFileName), encoded); err != nil {
		_ = os.RemoveAll(final)
		restorePreviousArtifact(previous, final)
		return err
	}
	if previous != "" {
		// The new tree and index are durable at this point; a stuck old tree
		// is residue to report, not a packaging failure.
		if err := os.RemoveAll(previous); err != nil {
			slog.Warn("package-builtin: previous artifact left behind", "path", previous, "error", err)
		}
	}
	return nil
}

func restorePreviousArtifact(previous, final string) {
	if previous != "" {
		_ = os.Rename(previous, final)
	}
}

func resolvePackageBuiltinInputs(name string) (*packageBuiltinInputs, error) {
	name = strings.TrimSpace(name)
	spec, ok := workflows.BuiltinWorkflow(name)
	if !ok {
		return nil, fmt.Errorf("built-in workflow %q: %w", name, domain.ErrNotFound)
	}
	dist, out, err := resolvePackagePaths()
	if err != nil {
		return nil, err
	}
	sourceDigest, runners, _ := workflows.BuiltinArtifactExpectation(name)
	attested, err := checkSourceDigestAttestation(dist, sourceDigest)
	if err != nil {
		return nil, err
	}
	sdkDir, err := resolveLoomSDKDir(workflowPackageLoomSDK)
	if err != nil {
		return nil, err
	}
	flueCommit, nodeVersion, err := resolvePackagePins()
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(workflowPackageTarget)
	if target == "" {
		target = packaged.HostTargetTriple()
	}
	return &packageBuiltinInputs{
		name:         name,
		dist:         dist,
		out:          out,
		sdkDir:       sdkDir,
		flueCommit:   flueCommit,
		nodeVersion:  nodeVersion,
		target:       target,
		sourceDigest: sourceDigest,
		entrypoint:   spec.Entrypoint,
		runners:      runners,
		attested:     attested,
	}, nil
}

// resolvePackagePaths validates --dist (must hold server.mjs) and --out
// (must not be inside --dist) and returns both as absolute paths.
func resolvePackagePaths() (dist, out string, err error) {
	dist, err = filepath.Abs(strings.TrimSpace(workflowPackageDist))
	if err != nil {
		return "", "", fmt.Errorf("resolve --dist: %w", err)
	}
	out, err = filepath.Abs(strings.TrimSpace(workflowPackageOut))
	if err != nil {
		return "", "", fmt.Errorf("resolve --out: %w", err)
	}
	if info, err := os.Stat(filepath.Join(dist, "server.mjs")); err != nil || !info.Mode().IsRegular() {
		return "", "", invalidArtifact("dist missing server.mjs at %s", dist)
	}
	if pathWithin(resolvedPath(out), resolvedPath(dist)) {
		return "", "", invalidArtifact("--out must not be inside --dist")
	}
	return dist, out, nil
}

// resolvedPath follows symlinks on the longest existing prefix of path so a
// link into --dist cannot dodge the containment check.
func resolvedPath(path string) string {
	missing := ""
	for {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(real, missing)
		}
		parent, base := filepath.Dir(path), filepath.Base(path)
		if parent == path {
			return filepath.Join(path, missing)
		}
		missing = filepath.Join(base, missing)
		path = parent
	}
}

func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// checkSourceDigestAttestation compares an optional <dist>/source-digest.txt
// against the embedded digest. Missing is allowed (attested=false); drift is
// refused before any write.
func checkSourceDigestAttestation(dist, sourceDigest string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dist, "source-digest.txt")) //nolint:gosec // operator-provided dist path.
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read source-digest.txt: %w", err)
	}
	if got := strings.TrimSpace(string(raw)); got != sourceDigest {
		return false, invalidArtifact("source digest drift (dist %s, embedded %s); rebuild with scripts/rebuild-builtin-bundle.sh", got, sourceDigest)
	}
	return true, nil
}

// resolvePackagePins applies the flag defaults and refuses pin drift unless
// --allow-pin-drift is set.
func resolvePackagePins() (flueCommit, nodeVersion string, err error) {
	flueCommit = strings.TrimSpace(workflowPackageFlueCommit)
	if flueCommit == "" {
		flueCommit = workflows.PinnedFlueCommit
	}
	nodeVersion = strings.TrimSpace(workflowPackageNodeVersion)
	if nodeVersion == "" {
		nodeVersion = workflows.PinnedNodeVersion
	}
	if workflowPackageAllowDrift {
		return flueCommit, nodeVersion, nil
	}
	if flueCommit != workflows.PinnedFlueCommit {
		return "", "", invalidArtifact("pin drift: --flue-commit %s != pinned %s (pass --allow-pin-drift to override)", flueCommit, workflows.PinnedFlueCommit)
	}
	if nodeVersion != workflows.PinnedNodeVersion {
		return "", "", invalidArtifact("pin drift: --node-version %s != pinned %s (pass --allow-pin-drift to override)", nodeVersion, workflows.PinnedNodeVersion)
	}
	return flueCommit, nodeVersion, nil
}

// resolveLoomSDKDir is a BUILD-TIME input to the packager (mirrors the
// authoring lane's loomSDKRoot order) — never a runtime lookup.
func resolveLoomSDKDir(flag string) (string, error) {
	candidate := strings.TrimSpace(flag)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("LOOM_SDK_ROOT"))
	}
	if candidate == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(cwd, "sdk")
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	for _, rel := range packaged.LoomSDKRuntimeFiles {
		if info, err := os.Stat(filepath.Join(candidate, rel)); err != nil || !info.Mode().IsRegular() {
			return "", invalidArtifact("@loom/sdk runtime files not found at %s (missing %s); pass --loom-sdk", candidate, rel)
		}
	}
	raw, err := os.ReadFile(filepath.Join(candidate, "package.json")) //nolint:gosec // operator-provided sdk path.
	if err != nil {
		return "", invalidArtifact("@loom/sdk runtime files not found at %s; pass --loom-sdk", candidate)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Name != "@loom/sdk" {
		return "", invalidArtifact("@loom/sdk runtime files not found at %s (package.json name is not @loom/sdk); pass --loom-sdk", candidate)
	}
	return candidate, nil
}

// stagePackagedDist copies dist → tmp/dist dereferencing symlinks, replaces
// node_modules/@loom/sdk wholesale with the SDK runtime files, and carries
// source-digest.txt alongside when present.
func stagePackagedDist(in *packageBuiltinInputs, tmp string) error {
	stagedDist := filepath.Join(tmp, "dist")
	if err := copyDereferenced(in.dist, stagedDist); err != nil {
		return fmt.Errorf("stage dist: %w", err)
	}
	sdkDest := filepath.Join(stagedDist, "node_modules", "@loom", "sdk")
	if err := os.RemoveAll(sdkDest); err != nil {
		return fmt.Errorf("replace nested @loom/sdk: %w", err)
	}
	if err := os.MkdirAll(sdkDest, 0o755); err != nil {
		return fmt.Errorf("create nested @loom/sdk: %w", err)
	}
	for _, rel := range packaged.LoomSDKRuntimeFiles {
		data, err := os.ReadFile(filepath.Join(in.sdkDir, rel)) //nolint:gosec // validated sdk dir.
		if err != nil {
			return fmt.Errorf("read @loom/sdk %s: %w", rel, err)
		}
		if err := os.WriteFile(filepath.Join(sdkDest, rel), data, 0o644); err != nil { //nolint:gosec // artifact files are world-readable by design.
			return fmt.Errorf("write @loom/sdk %s: %w", rel, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(in.dist, "source-digest.txt")); err == nil { //nolint:gosec // operator-provided dist path.
		if err := os.WriteFile(filepath.Join(tmp, "source-digest.txt"), data, 0o644); err != nil { //nolint:gosec // informational file.
			return fmt.Errorf("write source-digest.txt: %w", err)
		}
	}
	return nil
}

// copyDereferenced copies src → dst following symlinks. Anything that is not
// a directory or regular file after dereferencing (dangling link, device) is
// an error, so the packaged tree never contains a symlink. Directory symlinks
// are walked at their resolved path; a link cycle is reported, not recursed.
func copyDereferenced(src, dst string) error {
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	c := &dereferencingCopier{seen: map[string]struct{}{}}
	return c.copyTree(real, dst)
}

type dereferencingCopier struct {
	seen map[string]struct{} // resolved directories on the current descent path
}

// copyTree copies the resolved directory src into dst. A directory symlink
// whose target is already being walked (an ancestor) is a cycle; two links
// to the same directory elsewhere in the tree are fine and copied twice.
func (c *dereferencingCopier) copyTree(src, dst string) error {
	if _, dup := c.seen[src]; dup {
		return fmt.Errorf("%s: symlink cycle", src)
	}
	c.seen[src] = struct{}{}
	defer delete(c.seen, src)
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		switch {
		case info.IsDir():
			if entry.Type()&fs.ModeSymlink != 0 {
				// Directory symlink: WalkDir does not descend, so copy the
				// resolved subtree (walking the link path itself would
				// re-yield the link as the root and never terminate).
				real, err := filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("%s: %w", rel, err)
				}
				return c.copyTree(real, target)
			}
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			return copyFileDereferenced(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("%s: unsupported file type %s", rel, info.Mode().Type())
		}
	})
}

func copyFileDereferenced(path, target string, perm os.FileMode) error {
	in, err := os.Open(path) //nolint:gosec // path comes from walking the dist.
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // target is under the staging dir.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// loadOrInitPackageIndex reads <out>/index.json when present (merge) and
// validates its target/pins against this run's effective values.
func loadOrInitPackageIndex(in *packageBuiltinInputs) (packaged.Index, error) {
	idx := packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    in.flueCommit,
		NodeVersion:   in.nodeVersion,
		Target:        in.target,
		Builtins:      map[string]packaged.Entry{},
	}
	raw, err := os.ReadFile(filepath.Join(in.out, packaged.IndexFileName)) //nolint:gosec // operator-provided out path.
	if errors.Is(err, os.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return idx, fmt.Errorf("read existing index: %w", err)
	}
	var existing packaged.Index
	if err := json.Unmarshal(raw, &existing); err != nil {
		return idx, invalidArtifact("existing index.json is not parseable: %v", err)
	}
	if existing.SchemaVersion != packaged.SchemaVersion {
		return idx, invalidArtifact("existing index schema_version %q != %q", existing.SchemaVersion, packaged.SchemaVersion)
	}
	if existing.Target != in.target {
		return idx, invalidArtifact("index target mismatch (existing %s, --target %s)", existing.Target, in.target)
	}
	if (existing.FlueCommit != in.flueCommit || existing.NodeVersion != in.nodeVersion) && !workflowPackageAllowDrift {
		return idx, invalidArtifact("existing index pins differ (flue_commit %s/%s, node_version %s/%s); pass --allow-pin-drift to overwrite",
			existing.FlueCommit, in.flueCommit, existing.NodeVersion, in.nodeVersion)
	}
	for name, entry := range existing.Builtins {
		idx.Builtins[name] = entry
	}
	return idx, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil { //nolint:gosec // index.json is world-readable by design.
		_ = os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func printPackageBuiltinOutput(out packageBuiltinOutput) error {
	if workflowPackageJSON {
		return cmdstore.WriteJSON(out)
	}
	lines := []string{
		"name=" + out.Name,
		"out=" + out.Out,
		"dist=" + out.Dist,
		"artifact_digest=" + out.ArtifactDigest,
		"source_digest=" + out.SourceDigest,
		fmt.Sprintf("source_digest_attested=%t", out.SourceDigestAttested),
		"target=" + out.Target,
		"flue_commit=" + out.FlueCommit,
		"node_version=" + out.NodeVersion,
		"audit.native_files=" + strings.Join(out.Audit.NativeFiles, ","),
		"audit.bare_specifiers=" + strings.Join(out.Audit.BareSpecifiers, ","),
		"audit.dynamic_bare_specifiers=" + strings.Join(out.Audit.DynamicBareSpecifiers, ","),
		fmt.Sprintf("audit.dlopen=%t", out.Audit.Dlopen),
		fmt.Sprintf("audit.create_require_count=%d", out.Audit.CreateRequireCount),
		"audit.symlinks=" + strings.Join(out.Audit.Symlinks, ","),
	}
	lines = append(lines, "index_digest="+out.IndexDigest)
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}
