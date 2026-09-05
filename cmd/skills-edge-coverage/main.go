package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var errNotReady = errors.New("skills edge evidence is incomplete")

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: skills-edge-coverage catalog|extract|readiness")
	}
	switch args[0] {
	case "catalog":
		return writeJSON(stdout, registry.CanonicalCatalog())
	case "extract":
		return extract(args[1:], stdin, stdout)
	case "readiness":
		return readiness(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func extract(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("extract", flag.ContinueOnError)
	repository := flags.String("repository", "", "loom or fleet")
	revision := flags.String("revision", "", "tested commit SHA")
	backend := flags.String("backend", "", "redis or postgres")
	provider := flags.String("provider", "", "local, minio, or gcs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	report, err := registry.ParseGoTestJSON(stdin, registry.Repository(*repository), *revision, registry.Backend(*backend), registry.Provider(*provider))
	if err != nil {
		return err
	}
	return registry.WriteEvidenceReport(stdout, report)
}

func readiness(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("readiness", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("readiness requires at least one evidence report")
	}
	reports := make([]registry.EvidenceReport, 0, flags.NArg())
	for _, path := range flags.Args() {
		// #nosec G304 -- readiness intentionally reads operator-selected report paths.
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		report, decodeErr := registry.DecodeEvidenceReport(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("%s: %w", path, decodeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		reports = append(reports, report)
	}
	result, err := registry.ValidateReadiness(registry.CanonicalCatalog(), reports)
	if err != nil {
		return err
	}
	if err := writeJSON(stdout, result); err != nil {
		return err
	}
	if !result.Ready {
		return errNotReady
	}
	return nil
}

func writeJSON(w io.Writer, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", body)
	return err
}
