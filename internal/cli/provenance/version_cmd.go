package provenance

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

// NewVersionCmd builds the `loom version` command. stamp is an accessor
// rather than a value so the caller keeps ownership of the ldflags-set vars
// (they must live in internal/cli for -X compatibility) while the command
// itself lives with the logic it drives.
func init() {
	cli.RegisterCommand(NewVersionCmd(stampFromCLI))
	// One assembly for every entry point: the root --version flag and the
	// controlled-lead banner render through these.
	cli.VersionLine = func() string { return Current(stampFromCLI()).String() }
	cli.VersionSkewWarning = func() string { return SkewWarning(stampFromCLI()) }
}

func NewVersionCmd(stamp func() Stamp) *cobra.Command {
	var (
		versionJSON   bool
		versionRecord bool
	)
	cmd := &cobra.Command{
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
		RunE: func(_ *cobra.Command, _ []string) error {
			s := stamp()
			if versionRecord {
				path, err := WriteDeployRecord(s, time.Now().UTC().Format(time.RFC3339))
				if err != nil {
					return fmt.Errorf("record deployed build: %w", err)
				}
				fmt.Printf("Recorded deployed build %s (%s) in %s\n", s.Version, s.Commit, path)
				return nil
			}

			info := Current(s)
			if versionJSON {
				return cmdstore.WriteJSON(info)
			}
			fmt.Println(info.String())
			if info.Skew != "" {
				// stderr: a skew warning is diagnostic, so it must not corrupt
				// a caller parsing the version line out of stdout.
				fmt.Fprintln(os.Stderr, SkewWarning(s))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&versionJSON, "json", false, "JSON output, including deploy record and skew")
	cmd.Flags().BoolVar(&versionRecord, "record", false, "Record this binary as the host's deployed build")
	return cmd
}
