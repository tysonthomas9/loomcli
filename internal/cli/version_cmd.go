package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

var (
	versionJSON   bool
	versionRecord bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print build provenance (version, commit, ref, source PRs)",
	Long: `Print this binary's build provenance.

  loom version              human one-liner
  loom version --json       structured, including the host's deploy record and
                            any skew between it and the binary being run
  loom version --record     record THIS binary as the host's deployed build
                            (the deployer calls this after installing)

Ref, source PRs and build time are stamped at build time with
-ldflags -X github.com/tysonthomas9/loomcli/internal/cli.Ref=... etc. An
unstamped field prints as absent rather than being guessed at.`,
	Args: cobra.NoArgs,
	RunE: runVersion,
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "JSON output, including deploy record and skew")
	versionCmd.Flags().BoolVar(&versionRecord, "record", false, "Record this binary as the host's deployed build")
	RegisterCommand(versionCmd)
}

func runVersion(_ *cobra.Command, _ []string) error {
	if versionRecord {
		path, err := WriteDeployRecord(time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("record deployed build: %w", err)
		}
		fmt.Printf("Recorded deployed build %s (%s) in %s\n", Version, Build, path)
		return nil
	}

	info := CurrentVersionInfo()
	if versionJSON {
		return cmdstore.WriteJSON(info)
	}
	fmt.Println(info.String())
	if info.Skew != "" {
		// stderr: a skew warning is diagnostic, so it must not corrupt a
		// caller that is parsing the version line out of stdout.
		fmt.Fprintln(os.Stderr, VersionSkewWarning())
	}
	return nil
}
