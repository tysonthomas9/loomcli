package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const producerFilename = "fleet_action_contract_producer.json"

type producerManifest struct {
	Version        int    `json:"version"`
	Repository     string `json:"repository"`
	Revision       string `json:"revision"`
	Source         string `json:"source"`
	ContractSHA256 string `json:"contract_sha256"`
}

type generationOptions struct {
	fleetDB, output, manifest, revision string
	check, upstream, update             bool
}

func generateContract(options generationOptions) error {
	if options.update && (options.check || options.upstream) || options.check && options.upstream {
		return fmt.Errorf("choose only one of -check, -check-upstream, or -update-producer")
	}
	if options.update != (options.revision != "") {
		return fmt.Errorf("-update-producer and -revision must be supplied together")
	}
	if options.manifest == "" {
		options.manifest = filepath.Join(filepath.Dir(options.output), producerFilename)
	}
	manifest, err := selectedProducer(options)
	if err != nil {
		return err
	}
	contract, err := contractAtRevision(options.fleetDB, manifest.Revision)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(contract))
	if !options.update && !options.upstream && manifest.ContractSHA256 != digest {
		return fmt.Errorf("pinned producer contract hash mismatch: regenerate with an explicit -update-producer -revision")
	}
	if options.check || options.upstream {
		committed, err := os.ReadFile(options.output) //nolint:gosec // Explicit generator output path.
		if err != nil {
			return err
		}
		if !bytes.Equal(committed, contract) {
			return fmt.Errorf("FleetDB action contract drift at %s; regenerate and review consumer parity", manifest.Revision)
		}
		fmt.Printf("FleetDB action contract verified at %s (%s)\n", manifest.Revision, digest)
		return nil
	}
	if options.update {
		manifest.ContractSHA256 = digest
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(options.manifest, append(encoded, '\n'), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(options.output, contract, 0o644)
}

func selectedProducer(options generationOptions) (producerManifest, error) {
	if options.update {
		if !validRevision(options.revision) {
			return producerManifest{}, fmt.Errorf("producer revision must be a full lowercase 40-character commit SHA")
		}
		return producerManifest{Version: 1, Repository: "BrowserOperator/fleet-db", Revision: options.revision, Source: fleetEventPath}, nil
	}
	manifest, err := readProducer(options.manifest)
	if err != nil {
		return producerManifest{}, err
	}
	if options.upstream {
		manifest.Revision, err = gitRevision(options.fleetDB)
	}
	return manifest, err
}

func readProducer(path string) (producerManifest, error) {
	var manifest producerManifest
	source, err := os.ReadFile(path) //nolint:gosec // Explicit producer manifest path.
	if err != nil {
		return manifest, err
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, fmt.Errorf("producer manifest must contain exactly one JSON object")
	}
	if manifest.Version != 1 || manifest.Repository != "BrowserOperator/fleet-db" || manifest.Source != fleetEventPath || !validRevision(manifest.Revision) {
		return manifest, fmt.Errorf("invalid producer manifest identity or full commit revision")
	}
	digest, err := hex.DecodeString(manifest.ContractSHA256)
	if err != nil || len(digest) != sha256.Size || strings.ToLower(manifest.ContractSHA256) != manifest.ContractSHA256 {
		return manifest, fmt.Errorf("invalid producer contract SHA256")
	}
	return manifest, nil
}

func validRevision(revision string) bool {
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == 20 && strings.ToLower(revision) == revision
}

func contractAtRevision(repo, revision string) ([]byte, error) {
	if !validRevision(revision) {
		return nil, fmt.Errorf("producer revision must be a full lowercase 40-character commit SHA")
	}
	// Require a commit object, rather than permitting a tag or tree SHA to be
	// recorded as the tested producer. Never read dirty working-tree sources.
	kind, err := exec.Command("git", "-C", repo, "cat-file", "-t", revision).Output() //nolint:gosec,norawexec // Fixed git query with a validated object ID.
	if err != nil || strings.TrimSpace(string(kind)) != "commit" {
		return nil, fmt.Errorf("producer revision %s is not an available commit object", revision)
	}
	source, err := exec.Command("git", "-C", repo, "show", revision+":"+fleetEventPath).Output() //nolint:gosec,norawexec // Fixed canonical source path at a validated commit ID.
	if err != nil {
		return nil, fmt.Errorf("read pinned FleetDB event model: %w", err)
	}
	rows, err := parseFleetActionContract(source)
	if err != nil {
		return nil, err
	}
	return renderContract(rows)
}
