package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/authoringkit"
)

var (
	workflowAuthoringKitOut   string
	workflowAuthoringKitRoots []string
	workflowAuthoringKitJSON  bool
)

var workflowPackageAuthoringKitCmd = &cobra.Command{
	Use:   "package-authoring-kit",
	Short: "Assemble and audit the offline custom-workflow authoring kit",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowPackageAuthoringKit,
}

func init() {
	workflowPackageAuthoringKitCmd.Flags().StringVar(&workflowAuthoringKitOut, "out", "", "Output authoring-kit directory")
	workflowPackageAuthoringKitCmd.Flags().StringArrayVar(&workflowAuthoringKitRoots, "root", nil, "Source directory to copy as <name> (repeatable name=path)")
	workflowPackageAuthoringKitCmd.Flags().BoolVar(&workflowAuthoringKitJSON, "json", false, "JSON output")
	_ = workflowPackageAuthoringKitCmd.MarkFlagRequired("out")
	workflowCmd.AddCommand(workflowPackageAuthoringKitCmd)
}

func runWorkflowPackageAuthoringKit(_ *cobra.Command, _ []string) error {
	if len(workflowAuthoringKitRoots) == 0 {
		return fmt.Errorf("at least one --root name=path is required")
	}
	if err := packageAuthoringKit(workflowAuthoringKitOut, workflowAuthoringKitRoots); err != nil {
		return err
	}
	result := map[string]any{"out": workflowAuthoringKitOut, "manifest": filepath.Join(workflowAuthoringKitOut, "kit-manifest.json")}
	if workflowAuthoringKitJSON {
		return cmdstore.WriteJSON(result)
	}
	fmt.Printf("Packaged authoring kit at %s\n", workflowAuthoringKitOut)
	return nil
}

func packageAuthoringKit(out string, roots []string) error {
	parent := filepath.Dir(filepath.Clean(out))
	tmp, err := os.MkdirTemp(parent, ".authoring-kit-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	entries := []authoringkit.FileEntry{}
	for _, spec := range roots {
		name, src, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf("invalid --root %q; want name=path", spec)
		}
		dst := filepath.Join(tmp, filepath.Base(name))
		if filepath.Base(name) != name {
			return fmt.Errorf("unsafe kit root name %q", name)
		}
		if err := copyKitTree(src, dst, filepath.Base(name), &entries); err != nil {
			return err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	m := authoringkit.Manifest{SchemaVersion: authoringkit.SchemaVersion, FlueCommit: workflows.PinnedFlueCommit, NodeVersion: workflows.PinnedNodeVersion, Files: entries}
	canonical, err := authoringkit.CanonicalBytes(m)
	if err != nil {
		return err
	}
	m.KitDigest = authoringkit.DigestBytes(canonical)
	manifest, err := authoringkit.CanonicalBytes(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "kit-manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("output already exists: %w", os.ErrExist)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, out)
}

func copyKitTree(src, dst, prefix string, entries *[]authoringkit.FileEntry) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in kit input %s", path)
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(path) //nolint:gosec // path is produced by Walk under the explicit source root.
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		*entries = append(*entries, authoringkit.FileEntry{Path: filepath.ToSlash(filepath.Join(prefix, rel)), Kind: "data", SHA256: "sha256:" + hex.EncodeToString(sum[:])})
		return nil
	})
}
